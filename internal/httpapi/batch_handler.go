package httpapi

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/el-amin-dev/barqr/internal/batch"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// writerFor looks up an output writer by format name.
func writerFor(format string) (writer.Writer, error) { return writer.Get(format) }

// handleBatch renders many codes in one request.
//
// The heavy lifting lives in internal/batch, which never imports this package:
// it takes a RenderFunc and calls back into the same pipeline a single render
// uses. That keeps one code path for producing an image, so a batch can never
// drift from what /v1/qr would have produced for the same options.
func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	req, err := s.batchRequest(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out, err := batch.Run(r.Context(), req, s.renderBatchItem, batch.RunOptions{
		MaxItems: s.cfg.MaxBatchItems,
		// One batch must not monopolise the process: it renders inside the
		// same concurrency budget every other request shares.
		Concurrency: s.cfg.Concurrency,
	})
	if err != nil {
		s.fail(w, r, batchFault(err, s.cfg.MaxBatchItems))
		return
	}

	if s.metrics != nil {
		for _, res := range out.Results {
			if res.OK {
				s.metrics.observeRender("batch", req.Output, res.Bytes)
			}
		}
	}

	// A batch response is request-specific and often large; caching it would
	// be all cost and no benefit.
	w.Header().Set("Cache-Control", "no-store")

	// The batch package serialises the json form as a bare array. Every other
	// barqr endpoint answers with an object, so it is re-wrapped here rather
	// than making callers special-case this one response shape.
	if req.Output == batch.OutputJSON {
		w.Header().Set("X-Batch-Items", strconv.Itoa(len(out.Results)))
		s.writeJSON(w, r, http.StatusOK, map[string]any{
			"count":     len(out.Results),
			"succeeded": countOK(out.Results),
			"results":   out.Results,
		})
		return
	}

	w.Header().Set("Content-Type", out.MIME)
	w.Header().Set("Content-Length", strconv.Itoa(len(out.Body)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(out.Filename, true))
	w.Header().Set("X-Batch-Items", strconv.Itoa(len(out.Results)))

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(out.Body); err != nil {
		s.log.Debug("writing batch output failed", "error", err.Error())
	}
}

// batchRequest decodes a batch from JSON or from a raw CSV body.
func (s *Server) batchRequest(r *http.Request) (batch.Request, error) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))

	if mediaType == "text/csv" || mediaType == "application/csv" {
		// A raw CSV upload is the shape a spreadsheet export takes, so it is
		// accepted directly rather than requiring it be wrapped in JSON.
		body, err := readLimited(r, s.cfg.MaxBody)
		if err != nil {
			return batch.Request{}, err
		}
		return batch.Request{
			CSV:      string(body),
			Output:   r.URL.Query().Get("output"),
			Defaults: queryDefaults(r),
		}, nil
	}

	var req batch.Request
	if err := decodeJSONBody(r.Body, &req); err != nil {
		return batch.Request{}, err
	}
	if req.Output == "" {
		req.Output = r.URL.Query().Get("output")
	}
	return req, nil
}

// queryDefaults collects dot-notation options from the query string, so a CSV
// upload can still carry batch-wide styling without a JSON envelope.
func queryDefaults(r *http.Request) map[string]any {
	out := make(map[string]any)
	for key, vals := range r.URL.Query() {
		if key == "output" || len(vals) == 0 {
			continue
		}
		out[key] = vals[len(vals)-1]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// renderBatchItem is the bridge from a batch item to the single-render
// pipeline: it rebuilds a Request from the item's fields plus the batch
// defaults, then runs exactly the code path /v1/qr runs.
func (s *Server) renderBatchItem(
	ctx context.Context, item batch.Item, defaults map[string]any,
) (batch.Rendered, error) {
	req := Request{Symbology: "qr"}

	// Precedence: batch defaults, then the item's own options. The item is
	// the more specific statement, so it wins.
	if err := applyOptions(&req, defaults); err != nil {
		return batch.Rendered{}, err
	}
	if err := applyOptions(&req, item.Options); err != nil {
		return batch.Rendered{}, err
	}

	if item.Type != "" {
		req.Type = item.Type
	}
	if item.Payload != nil {
		req.Payload = item.Payload
	}
	if item.Data != "" {
		req.Data = item.Data
		// An item that carries both is asking for the raw string; the payload
		// belongs to a different item shape and would otherwise trip the
		// "set either type+payload or data" guard for the whole batch.
		if item.Type == "" {
			req.Payload = nil
		}
	}

	res, err := s.pipeline(ctx, req)
	if err != nil {
		return batch.Rendered{}, err
	}

	ext := "bin"
	if w, err := writerFor(res.Format); err == nil {
		ext = w.Extension()
	}
	return batch.Rendered{
		Body:      res.Body,
		MIME:      res.MIME,
		Extension: ext,
		Data:      res.Data,
	}, nil
}

// applyOptions sets dot-notation keys onto a request, as the query transport
// would. It is how a batch's defaults and an item's overrides reach the same
// struct every other transport produces.
func applyOptions(req *Request, opts map[string]any) error {
	if len(opts) == 0 {
		return nil
	}
	values := make(map[string][]string, len(opts))
	for k, v := range opts {
		values[k] = []string{stringify(v)}
	}
	return applyValues(req, values)
}

// stringify renders a JSON-decoded value as the string form the dot-notation
// setter parses. Numbers come back from encoding/json as float64, and
// formatting one with %v would turn 10 into "10" but 1e+06 into "1e+06".
func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case nil:
		return ""
	default:
		return ""
	}
}

// countOK counts the items that rendered, so a caller can tell at a glance
// whether a partially failed batch is worth retrying.
func countOK(results []batch.Result) int {
	n := 0
	for _, r := range results {
		if r.OK {
			n++
		}
	}
	return n
}

// batchFault maps the batch package's structural errors onto the wire shape.
func batchFault(err error, maxItems int) error {
	switch {
	case errors.Is(err, batch.ErrEmptyBatch):
		f := newFault(http.StatusBadRequest, CodeMissingData, "%s", err)
		f.Hint = "set `items` to a list, or `csv` to a document with a header row"
		return f
	case errors.Is(err, batch.ErrTooManyItems):
		f := newFault(http.StatusRequestEntityTooLarge, CodeBadRequest, "%s", err)
		f.Expected = "at most " + strconv.Itoa(maxItems) + " items"
		f.Hint = "split the batch, or raise BARQR_MAX_BATCH_ITEMS"
		return f
	case errors.Is(err, batch.ErrBadCSV):
		f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
		f.Field = "csv"
		return f
	case errors.Is(err, batch.ErrUnsupportedOutput):
		f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
		f.Field = "output"
		f.Expected = "zip or json"
		f.Hint = "for a printable sheet of labels use /v1/sheet"
		return f
	default:
		return err
	}
}

// handlePresetList serves the available presets.
func (s *Server) handlePresetList(w http.ResponseWriter, r *http.Request) {
	s.cacheable(w)
	s.writeJSON(w, r, http.StatusOK, map[string]any{"presets": s.presets.All()})
}

// handlePreset renders using a named preset as the baseline.
//
// The preset supplies the options; anything in the request overrides it. That
// order is what makes a preset a starting point rather than a straitjacket:
// `?preset=print&output.format=svg` is a print preset that happens to emit SVG.
func (s *Server) handlePreset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	p, ok := s.presets.Get(name)
	if !ok {
		f := newFault(http.StatusNotFound, CodeNotFound, "no preset named %q", name)
		f.Field = "preset"
		if best, found := closest(name, s.presets.Names()); found {
			f.Hint = "did you mean \"" + best + "\"?"
		} else {
			f.Hint = "see /v1/preset for the available presets"
		}
		s.fail(w, r, f)
		return
	}

	req := Request{Symbology: "qr"}
	if err := applyOptions(&req, p.Options); err != nil {
		s.fail(w, r, err)
		return
	}

	// Decode over the preset so the request wins on every field it sets.
	if err := decodeOver(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}

	res, err := s.pipeline(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if s.metrics != nil {
		s.metrics.observeRender(res.Symbology, res.Format, len(res.Body))
	}
	w.Header().Set("X-Barqr-Preset", p.Name)
	s.serveResult(w, r, res)
}
