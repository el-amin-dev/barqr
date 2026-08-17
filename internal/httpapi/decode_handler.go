package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/decoder"
	"github.com/el-amin-dev/barqr/internal/encoder"
)

// decodeRequest is the option set for POST /v1/decode.
//
// It is separate from Request because decoding shares nothing with rendering:
// there is no style, no output format, and no symbology to encode into. Reusing
// Request here would have meant a struct where half the fields are meaningless
// depending on the endpoint.
type decodeRequest struct {
	// Image is a data URI, used when no file part was uploaded.
	Image string `json:"image,omitempty"`
	// TryHarder trades speed for a more thorough scan.
	TryHarder bool `json:"try_harder,omitempty"`
	// Multi finds every code in the image rather than the first.
	Multi bool `json:"multi,omitempty"`
	// Symbologies restricts the search. Empty means every available one.
	Symbologies []string `json:"symbologies,omitempty"`
	// Parse runs the decoded string back through the builders, so a Wi-Fi QR
	// comes back as structured fields rather than a WIFI: string.
	Parse bool `json:"parse,omitempty"`
}

// decodedResult is one code found in the image.
type decodedResult struct {
	Symbology string          `json:"symbology"`
	Data      string          `json:"data"`
	Points    []decoder.Point `json:"points,omitempty"`
	// Type and Payload are populated when parse was requested and a builder
	// recognised the string.
	Type    string `json:"type,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

// handleDecode reads codes out of an uploaded image.
//
// This is the most exposed surface in the service: it takes an arbitrary image
// from the network and hands it to a decoder. Every guard lives in
// internal/decoder — a byte cap, a pixel cap read from the image header before
// any pixels are decoded, and a panic backstop around the third-party library.
func (s *Server) handleDecode(w http.ResponseWriter, r *http.Request) {
	req, image, err := s.decodeInput(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	results, err := decoder.Decode(r.Context(), image, decoder.Options{
		TryHarder:   req.TryHarder,
		Multi:       req.Multi,
		Symbologies: req.Symbologies,
		MaxPixels:   s.cfg.MaxCanvasPx,
		MaxBytes:    s.cfg.MaxBody,
	})
	if err != nil {
		s.fail(w, r, decodeFault(err))
		return
	}

	out := make([]decodedResult, 0, len(results))
	for _, res := range results {
		item := decodedResult{Symbology: res.Symbology, Data: res.Data, Points: res.Points}
		if req.Parse {
			if name, payload, ok := parseWithBuilders(res.Data); ok {
				item.Type, item.Payload = name, payload
			}
		}
		out = append(out, item)
	}

	// A decode result describes the uploaded image, so it is never cacheable
	// and never shared.
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"count":   len(out),
		"results": out,
	})
}

// decodeInput extracts the options and the image bytes from any transport.
func (s *Server) decodeInput(r *http.Request) (decodeRequest, []byte, error) {
	req := decodeRequest{
		TryHarder: queryBool(r, "try_harder"),
		Multi:     queryBool(r, "multi"),
		Parse:     queryBool(r, "parse"),
	}
	if v := r.URL.Query().Get("symbologies"); v != "" {
		req.Symbologies = splitList(v)
	}

	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))

	switch mediaType {
	case "multipart/form-data":
		// The body is already capped by withBodyLimit's MaxBytesReader.
		// #nosec G120 -- bounded upstream: withBodyLimit wraps the body in a MaxBytesReader at BARQR_MAX_BODY
		if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
			return req, nil, newFault(http.StatusBadRequest, CodeBadRequest,
				"malformed multipart body")
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()

		applyDecodeForm(&req, r.MultipartForm.Value)

		file, header, err := r.FormFile("image")
		if err != nil {
			return req, nil, newFault(http.StatusBadRequest, CodeMissingData,
				"no file part named \"image\"")
		}
		defer func() { _ = file.Close() }()

		data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxBody+1))
		if err != nil {
			return req, nil, newFault(http.StatusBadRequest, CodeBadRequest,
				"could not read the uploaded image")
		}
		if int64(len(data)) > s.cfg.MaxBody {
			return req, nil, newFault(http.StatusRequestEntityTooLarge, CodeBodyTooLarge,
				"image exceeds the %d-byte limit", s.cfg.MaxBody)
		}
		_ = header
		return req, data, nil

	case "application/json":
		if err := decodeJSONBody(r.Body, &req); err != nil {
			return req, nil, err
		}
		if req.Image == "" {
			return req, nil, newFault(http.StatusBadRequest, CodeMissingData,
				"set \"image\" to a data URI, or upload a multipart file part named \"image\"")
		}
		data, _, err := decoder.DataFromURI(req.Image)
		if err != nil {
			return req, nil, newFault(http.StatusBadRequest, CodeInvalidValue,
				"the \"image\" field is not a readable data URI")
		}
		return req, data, nil

	default:
		// A bare body is the image itself, which is what
		// `curl --data-binary @code.png` sends.
		data, err := io.ReadAll(io.LimitReader(r.Body, s.cfg.MaxBody+1))
		if err != nil {
			return req, nil, newFault(http.StatusBadRequest, CodeBadRequest, "could not read body")
		}
		if int64(len(data)) > s.cfg.MaxBody {
			return req, nil, newFault(http.StatusRequestEntityTooLarge, CodeBodyTooLarge,
				"image exceeds the %d-byte limit", s.cfg.MaxBody)
		}
		if len(data) == 0 {
			return req, nil, newFault(http.StatusBadRequest, CodeMissingData, "no image supplied")
		}
		return req, data, nil
	}
}

// applyDecodeForm reads the decode options out of multipart string fields.
func applyDecodeForm(req *decodeRequest, values map[string][]string) {
	first := func(key string) string {
		if v := values[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	if v := first("try_harder"); v != "" {
		req.TryHarder, _ = parseBool(v)
	}
	if v := first("multi"); v != "" {
		req.Multi, _ = parseBool(v)
	}
	if v := first("parse"); v != "" {
		req.Parse, _ = parseBool(v)
	}
	if v := first("symbologies"); v != "" {
		req.Symbologies = splitList(v)
	}
}

// decodeJSONBody reads a JSON body into v, rejecting unknown fields.
func decodeJSONBody(body io.Reader, v any) error {
	if err := strictJSON(body, v); err != nil {
		if field, ok := unknownJSONField(err); ok {
			return unknownFieldError(field)
		}
		return newFault(http.StatusBadRequest, CodeBadRequest, "malformed JSON body")
	}
	return nil
}

// decodeFault maps the decoder's sentinels onto the wire error shape.
func decodeFault(err error) error {
	switch {
	case errors.Is(err, decoder.ErrNoCodeFound):
		f := newFault(http.StatusNotFound, CodeNotFound, "no code found in the image")
		f.Hint = "try try_harder=true, or crop the image closer to the code"
		return f
	case errors.Is(err, decoder.ErrImageTooLarge):
		return newFault(http.StatusRequestEntityTooLarge, CodeCanvasTooLarge, "%s", err)
	case errors.Is(err, decoder.ErrUnsupportedImage):
		f := newFault(http.StatusBadRequest, CodeInvalidValue, "%s", err)
		f.Hint = "supported formats: png, jpeg, gif, webp, bmp, tiff"
		return f
	case errors.Is(err, decoder.ErrDecodeFailed):
		return newFault(http.StatusUnprocessableEntity, CodeInvalidData, "%s", err)

	// The decoder reuses the encoder's sentinels when the caller restricts the
	// search to a symbology it cannot read. That is a bad request, not a
	// server fault, so it must be mapped here rather than falling through.
	case errors.Is(err, encoder.ErrUnknownSymbology):
		f := newFault(http.StatusBadRequest, CodeUnknownSymbology, "%s", err)
		f.Field = "symbologies"
		return f
	case errors.Is(err, encoder.ErrUnavailable):
		f := newFault(http.StatusNotImplemented, CodeUnavailable, "%s", err)
		f.Field = "symbologies"
		f.Hint = "this build cannot decode that symbology; omit it to search the rest"
		return f
	default:
		return err
	}
}

// parseWithBuilders finds the first builder that recognises a decoded string.
//
// Builders are tried in registry order, and `raw` and `text` are skipped
// because they match everything: they would shadow every structured type.
func parseWithBuilders(raw string) (string, any, bool) {
	for _, b := range builder.All() {
		switch b.Name() {
		case "raw", "text":
			continue
		}
		if payload, ok := b.Parse(raw); ok {
			return b.Name(), payload, true
		}
	}
	return "", nil, false
}

// queryBool reads a boolean query parameter, treating a bare flag as true.
func queryBool(r *http.Request, key string) bool {
	if !r.URL.Query().Has(key) {
		return false
	}
	v, err := parseBool(r.URL.Query().Get(key))
	return err == nil && v
}

// splitList splits a comma-separated value, trimming and dropping empties.
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
