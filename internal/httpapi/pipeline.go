package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/config"
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
	// Report is the scannability verdict for the canvas. It is always
	// computed, because "warn" has to have something to report.
	Report render.Report
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
//
// caps is the target symbology's capabilities, because the configured default
// error-correction level is expressed in QR's vocabulary and does not
// necessarily mean anything to another symbology. A linear code has no levels
// at all, and PDF417 numbers its own from "0" to "8" — applying BARQR_DEFAULT_ECC
// unconditionally made a request that named no options whatsoever fail with
// UNSUPPORTED_OPTION on nearly every symbology except QR.
//
// So the default is applied only when the symbology actually offers it. A
// symbology with levels of its own and no match picks its own default, which
// is what leaving ECC empty means to an encoder.
func (s *Server) encodeOpts(req Request, caps encoder.Capabilities) encoder.EncodeOpts {
	o := encoder.AutoEncodeOpts()

	o.ECC = req.Encode.ECC
	if o.ECC == "" && slices.Contains(caps.ECCLevels, s.cfg.DefaultECC) {
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
//
// It takes a context because resolving style.logo may reach the network: a
// remote logo is fetched here, under the guards in internal/fetch, and the
// caller's deadline has to bound that fetch like it bounds everything else.
func (s *Server) style(ctx context.Context, req Request,
	caps encoder.Capabilities) (render.Style, error) {
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
	if v := req.Style.Gradient; v != "" {
		g, err := render.ParseGradient(v)
		if err != nil {
			return render.Style{}, asFault(err).withField("style.gradient")
		}
		st.Gradient = g
	}

	// A logo reference resolves to a data: URI here — an inline one passes
	// through, a remote one is fetched under the SSRF guards — so a logo that
	// is unreachable or disallowed is refused before anything is encoded.
	if v := req.Style.Logo; v != "" {
		uri, err := s.resolveLogo(ctx, v)
		if err != nil {
			return render.Style{}, err
		}
		img, err := render.ParseLogo(uri)
		if err != nil {
			return render.Style{}, asFault(err).withField("style.logo")
		}

		logo := &render.Logo{Image: img, Scale: req.Style.LogoScale}
		if req.Style.Excavate != nil {
			logo.Excavate = *req.Style.Excavate
		}
		if req.Style.LogoPadding != nil {
			logo.Padding = *req.Style.LogoPadding
		}
		st.Logo = logo
	}

	// A caption needs somewhere to sit, and the renderer reserves that band as
	// part of the frame. Asking for one without a frame therefore implies the
	// plainest frame rather than silently dropping the text.
	caption := req.Style.Caption
	if req.Style.Frame != "" || caption != "" {
		frame := &render.Frame{Kind: req.Style.Frame, Caption: caption}
		if frame.Kind == "" {
			frame.Kind = render.FrameBorder
		}
		// Validate the kind here so the rejection names style.frame. The
		// renderer would also catch it, but only as a generic invalid style,
		// and "which field do I fix" is the whole value of the error shape.
		if !slices.Contains(render.FrameKinds(), frame.Kind) {
			f := newFault(http.StatusBadRequest, CodeInvalidValue,
				"unknown frame kind %q", frame.Kind)
			f.Field = "style.frame"
			f.Expected = strings.Join(render.FrameKinds(), ", ")
			f.Got = frame.Kind
			return render.Style{}, f
		}
		if req.Style.FrameWidth != nil {
			frame.Width = *req.Style.FrameWidth
		}
		if v := req.Style.FrameColor; v != "" {
			c, err := render.ParseColor(v)
			if err != nil {
				return render.Style{}, asFault(err).withField("style.frame_color")
			}
			frame.Color = c
		}
		if v := req.Style.CaptionColor; v != "" {
			c, err := render.ParseColor(v)
			if err != nil {
				return render.Style{}, asFault(err).withField("style.caption_color")
			}
			frame.CaptionColor = c
		}
		st.Frame = frame
		st.Caption = caption
	}

	// The renderer needs the resolved level to judge whether a logo fits the
	// error-correction budget; encoder.Matrix does not carry it.
	st.ECC = s.encodeOpts(req, caps).ECC
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
	// Clamp before anything multiplies these together. MaxPixels is checked on
	// the product, and a large enough scale wraps that product to a small
	// residue, passing the check and then panicking inside image.NewNRGBA.
	o.Scale = min(o.Scale, maxScale)
	if req.Output.Size > maxSize {
		o.Size = maxSize
	} else if req.Output.Size > 0 {
		// An explicit size takes precedence over scale in PixelSize, so the
		// default scale must be cleared or it would win.
		o.Size = req.Output.Size
		o.Scale = 0
	}
	if req.Output.Unit != "" {
		o.Unit = writer.Unit(req.Output.Unit)
	}
	if req.Output.DPI > 0 {
		o.DPI = min(req.Output.DPI, maxDPI)
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
	if ctx.Err() != nil {
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

	caps := enc.Caps()

	matrix, err := enc.Encode(data, s.encodeOpts(req, caps))
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, timeoutFault()
	}

	st, err := s.style(ctx, req, caps)
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
	if ctx.Err() != nil {
		return nil, timeoutFault()
	}

	// Scannability is judged on every render, not only on /v1/validate: warn
	// mode needs a verdict to put in the response header, and strict mode needs
	// one to refuse on. Before this was wired, BARQR_STRICT_SCANNABILITY was an
	// option that parsed, validated, and then did nothing.
	report := render.Scannability(canvas)
	if s.cfg.StrictScannability == config.ScannabilityStrict && !report.OK() {
		return nil, unscannableFault(report)
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
		Report:     report,
	}, nil
}

// unscannableFault refuses a render that strict mode judged unreadable.
//
// It names the worst finding rather than the whole list: a caller in strict
// mode needs the one thing to change, and the full report is a /v1/validate
// call away.
func unscannableFault(report render.Report) error {
	f := newFault(http.StatusUnprocessableEntity, CodeUnscannable,
		"this design is unlikely to scan (score %d/100)", report.Score)
	f.Field = "style"
	f.Expected = "a design graded good or better"
	if len(report.Issues) > 0 {
		worst := report.Issues[0]
		f.Message = worst.Message
		f.Hint = worst.Hint
		f.Got = worst.Code
	}
	return f
}

// Upper bounds on the sizing inputs.
//
// These are not the real limit — BARQR_MAX_CANVAS_PX is — but that limit is
// checked on a product, and an unbounded factor can wrap the product to a
// small value that passes. Clamping the factors keeps the arithmetic in a
// range where the real check means what it says.
const (
	maxScale = 1 << 14 // 16384 pixels per module
	maxDPI   = 1 << 16 // far beyond any imagesetter
	maxSize  = 1 << 20 // a million units, whatever the unit
)

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
