package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// result is everything a render produced, ready to be served.
type result struct {
	// Body is the serialised code.
	Body []byte
	// MIME is the Content-Type to serve it with.
	MIME string
	// Filename is the download name, extension included.
	Filename string
	// Attachment switches Content-Disposition from inline to attachment.
	Attachment bool
	// Data is the raw string that was encoded, for diagnostics.
	Data string
	// Symbology and Format record what was produced, for metrics.
	Symbology string
	Format    string
	// Canvas is the rendered form, kept for /v1/validate diagnostics.
	Canvas render.Canvas
}

// build resolves the request's payload into the raw string to encode.
//
// A Type selects a builder and Payload is its input; otherwise Data is taken
// verbatim. Supplying both is an error rather than a silent precedence rule,
// because guessing wrong here produces a perfectly valid code containing the
// wrong thing.
func (s *Server) build(req Request) (string, error) {
	hasType := req.Type != "" && req.Type != "raw"

	switch {
	case hasType && req.Data != "":
		f := newFault(http.StatusBadRequest, CodeBadRequest,
			"set either type+payload or data, not both")
		f.Field = "data"
		f.Hint = "drop `data`, or drop `type` and `payload`"
		return "", f

	case hasType:
		b, err := builder.Get(req.Type)
		if err != nil {
			f := newFault(http.StatusBadRequest, CodeUnknownType, "%s", err)
			f.Field = "type"
			if best, ok := closest(req.Type, builder.Names()); ok {
				f.Hint = fmt.Sprintf("did you mean %q?", best)
			}
			return "", f
		}
		raw, err := b.Build(req.Payload)
		if err != nil {
			f := newFault(http.StatusBadRequest, CodeInvalidPayload, "%s", err)
			f.Field = "payload"
			return "", f
		}
		return raw, nil

	case req.Data != "":
		return req.Data, nil

	case req.Type == "raw" && req.Payload != nil:
		// The raw builder is a passthrough; let it report its own missing
		// field rather than duplicating the message here.
		b, err := builder.Get("raw")
		if err != nil {
			return "", err
		}
		return b.Build(req.Payload)

	default:
		f := newFault(http.StatusBadRequest, CodeMissingData, "nothing to encode")
		f.Field = "data"
		f.Hint = "set `data` to a raw string, or `type` and `payload` to build one"
		return "", f
	}
}

// encodeOpts maps the request onto the encoder's options, filling automatic
// values from configuration.
func (s *Server) encodeOpts(req Request) encoder.EncodeOpts {
	o := encoder.AutoEncodeOpts()

	o.ECC = req.Encode.ECC
	if o.ECC == "" {
		o.ECC = s.cfg.DefaultECC
	}
	if req.Encode.Version.Set {
		o.Version = req.Encode.Version.Value
	}
	if req.Encode.Mask.Set {
		o.Mask = req.Encode.Mask.Value
	}
	if req.Encode.QuietZone != nil {
		o.QuietZone = *req.Encode.QuietZone
	}
	return o
}

// style maps the request onto a render style, starting from the plain
// black-on-white default and overriding only what was asked for.
func (s *Server) style(req Request) (render.Style, error) {
	st := render.DefaultStyle()

	if v := req.Style.Module; v != "" {
		st.Module = v
	}
	if v := req.Style.Eye; v != "" {
		st.Eye = v
	}
	if v := req.Style.EyeBall; v != "" {
		st.EyeBall = v
	}

	if v := req.Style.FG; v != "" {
		c, err := render.ParseColor(v)
		if err != nil {
			return render.Style{}, asFault(err).withField("style.fg")
		}
		st.FG = c
	}
	if v := req.Style.BG; v != "" {
		c, err := render.ParseColor(v)
		if err != nil {
			return render.Style{}, asFault(err).withField("style.bg")
		}
		st.BG = c
	}
	if v := req.Style.EyeFG; v != "" {
		c, err := render.ParseColor(v)
		if err != nil {
			return render.Style{}, asFault(err).withField("style.eye_fg")
		}
		st.EyeFG = &c
	}

	if req.Encode.QuietZone != nil {
		st.QuietZone = *req.Encode.QuietZone
	}
	st.BarHeight = req.Style.BarHeight
	if req.Style.HRI != nil {
		st.HRI = *req.Style.HRI
	}

	return st, nil
}

// outputOpts maps the request onto the writer's options, filling defaults from
// configuration and always applying the canvas cap.
func (s *Server) outputOpts(req Request) writer.OutputOpts {
	format := req.Output.Format
	if format == "" {
		format = s.cfg.DefaultFormat
	}

	o := writer.DefaultOutputOpts(format)
	if req.Output.Scale > 0 {
		o.Scale = req.Output.Scale
	}
	if req.Output.Size > 0 {
		// An explicit size takes precedence over scale in PixelSize, so the
		// default scale must be cleared or it would win.
		o.Size = req.Output.Size
		o.Scale = 0
	}
	if req.Output.Unit != "" {
		o.Unit = writer.Unit(req.Output.Unit)
	}
	if req.Output.DPI > 0 {
		o.DPI = req.Output.DPI
	}
	if req.Output.Quality > 0 {
		o.Quality = req.Output.Quality
	}

	o.MaxPixels = s.cfg.MaxCanvasPx
	o.Filename = req.Meta.Filename
	o.Attachment = req.Meta.Attachment
	return o
}

// pipeline runs the full build to bytes path.
//
// The context is checked between stages rather than threaded into them: each
// stage is bounded work once the canvas cap is applied, so a per-stage check
// gives cancellation the responsiveness it needs without pushing a context
// through pure CPU code.
func (s *Server) pipeline(ctx context.Context, req Request) (*result, error) {
	data, err := s.build(req)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, timeoutFault()
	}

	enc, err := encoder.Get(req.Symbology)
	if err != nil {
		f := asFault(err)
		if best, ok := closest(req.Symbology, encoder.Names()); ok && f.Code == CodeUnknownSymbology {
			f.Hint = fmt.Sprintf("did you mean %q? see /v1/symbologies", best)
		}
		return nil, f
	}

	matrix, err := enc.Encode(data, s.encodeOpts(req))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, timeoutFault()
	}

	st, err := s.style(req)
	if err != nil {
		return nil, err
	}

	renderer, err := render.Get(render.StandardRenderer)
	if err != nil {
		return nil, err
	}
	canvas, err := renderer.Render(matrix, st)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, timeoutFault()
	}

	out := s.outputOpts(req)
	w, err := writer.Get(out.Format)
	if err != nil {
		f := asFault(err)
		if best, ok := closest(out.Format, writer.Formats()); ok {
			f.Hint = fmt.Sprintf("did you mean %q? available: %s",
				best, strings.Join(writer.Formats(), ", "))
		}
		return nil, f
	}

	body, err := w.Write(canvas, out)
	if err != nil {
		return nil, err
	}

	return &result{
		Body:       body,
		MIME:       w.MIME(),
		Filename:   filename(out.Filename, req.Symbology, w.Extension()),
		Attachment: out.Attachment,
		Data:       data,
		Symbology:  req.Symbology,
		Format:     w.Name(),
		Canvas:     canvas,
	}, nil
}

// timeoutFault is the response for a request that ran past its deadline.
func timeoutFault() error {
	f := newFault(http.StatusGatewayTimeout, CodeTimeout, "request exceeded its time limit")
	f.Hint = "reduce output.scale, or raise BARQR_REQUEST_TIMEOUT"
	return f
}

// filename derives a download name, honouring an explicit one but forcing the
// extension to match what was actually produced.
func filename(requested, symbology, ext string) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = symbology
	}
	// Strip any directory component: this value reaches a Content-Disposition
	// header and must never suggest a path.
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	base = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == ';' || r == 0x7f {
			return '_'
		}
		return r
	}, base)
	if base == "" {
		base = "code"
	}
	if strings.HasSuffix(strings.ToLower(base), "."+ext) {
		return base
	}
	return base + "." + ext
}
