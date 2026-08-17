package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// handleQR renders a QR code. It is the endpoint the whole service exists for,
// and the only one with a symbology baked into the path.
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	s.renderAndServe(w, r, encoder.QR)
}

// handleBarcode renders any registered symbology named in the path.
func (s *Server) handleBarcode(w http.ResponseWriter, r *http.Request) {
	s.renderAndServe(w, r, chi.URLParam(r, "symbology"))
}

// renderAndServe decodes, renders, and serves, with caching.
func (s *Server) renderAndServe(w http.ResponseWriter, r *http.Request, symbology string) {
	req, err := Decode(r, symbology)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A body or query may override the endpoint's symbology only where that
	// makes sense; /v1/qr is QR by definition.
	if symbology == encoder.QR {
		req.Symbology = encoder.QR
	} else if req.Symbology == "" {
		req.Symbology = symbology
	}

	res, err := s.pipeline(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	if s.metrics != nil {
		s.metrics.observeRender(res.Symbology, res.Format, len(res.Body))
	}
	s.serveResult(w, r, res)
}

// handleBuild runs a builder and returns the raw encoded string, with no
// rendering at all. It exists so a caller can inspect exactly what would be
// encoded — and so another tool can use barqr's payload formats on its own.
func (s *Server) handleBuild(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "type")

	req, err := Decode(r, encoder.QR)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	req.Type = name

	raw, err := s.build(req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	b, err := builder.Get(name)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.noCacheIfPrivate(w)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"type":   b.Name(),
		"data":   raw,
		"length": len(raw),
	})
}

// handleValidate reports what a request would produce without producing it.
//
// It runs the build, encode, and render stages and then stops: the expensive
// part of a large render is serialisation, so a caller can check a design
// against the scannability rules cheaply and in bulk.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	req, err := Decode(r, encoder.QR)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	res, err := s.pipeline(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	c := res.Canvas
	report := render.Scannability(c)

	s.noCacheIfPrivate(w)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"valid":     true,
		"symbology": res.Symbology,
		"format":    res.Format,
		"data": map[string]any{
			"length": len(res.Data),
		},
		"matrix": map[string]any{
			"cols":       c.Cols,
			"rows":       c.Rows,
			"quiet_zone": c.QuietZone,
			"dark_ratio": float64(c.Dark()) / float64(max(c.Cols*c.Rows, 1)),
		},
		"output_bytes": len(res.Body),
		"scannability": report,
		"strict_mode":  string(s.cfg.StrictScannability),
	})
}

// handleSymbologies serves the machine-readable capability matrix.
//
// It reports what this build can actually do, including the symbologies it
// cannot: an integrator needs to know that `maxicode` exists but is not
// compiled in, which is different from it not existing.
func (s *Server) handleSymbologies(w http.ResponseWriter, r *http.Request) {
	formats := make([]map[string]any, 0, len(writer.All()))
	for _, wr := range writer.All() {
		formats = append(formats, map[string]any{
			"name":      wr.Name(),
			"mime":      wr.MIME(),
			"extension": wr.Extension(),
			"binary":    wr.Binary(),
		})
	}

	builders := make([]map[string]any, 0, len(builder.All()))
	for _, b := range builder.All() {
		builders = append(builders, map[string]any{
			"name":   b.Name(),
			"fields": b.Fields(),
		})
	}

	s.cacheable(w)
	s.writeJSON(w, r, http.StatusOK, map[string]any{
		"symbologies": encoder.All(),
		"formats":     formats,
		"builders":    builders,
		"styles": map[string]any{
			"module": render.ModuleShapes(),
			"eye":    render.EyeShapes(),
		},
		"defaults": map[string]any{
			"symbology": encoder.QR,
			"format":    s.cfg.DefaultFormat,
			"ecc":       s.cfg.DefaultECC,
			"scale":     writer.DefaultScale,
		},
		"limits": map[string]any{
			"max_canvas_px":   s.cfg.MaxCanvasPx,
			"max_body_bytes":  s.cfg.MaxBody,
			"max_batch_items": s.cfg.MaxBatchItems,
		},
	})
}

// serveResult writes a rendered code with cache validators.
//
// GET /v1/qr is pure: the same query always produces the same bytes. A strong
// ETag over the body therefore lets any proxy, gateway, or CDN in front of
// barqr turn repeat renders into 304s, which is the difference between
// rendering a product code once and rendering it a million times.
func (s *Server) serveResult(w http.ResponseWriter, r *http.Request, res *result) {
	etag := strongETag(res.Body)
	w.Header().Set("ETag", etag)

	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
		s.cacheable(w)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", res.MIME)
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Body)))
	w.Header().Set("Content-Disposition", contentDisposition(res.Filename, res.Attachment))
	setRenderSecurityHeaders(w)

	s.cacheable(w)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	// The body is a rendered image, served with a Content-Type this process
	// chose, nosniff, and the sandboxing CSP above — not caller-controlled
	// markup on an HTML content type.
	if _, err := w.Write(res.Body); err != nil { //nolint:gosec // G705: see setRenderSecurityHeaders
		s.log.Debug("writing rendered output failed", "error", err.Error())
	}
}

// renderCSP is the content-security policy served with every rendered code.
//
// SVG is the reason this exists. An SVG document can carry script, and barqr
// serves it inline from the same origin as whatever embeds it — so if any
// injection ever slipped past the writer's escaping, the browser would execute
// it with that origin's privileges. Denying every resource type and sandboxing
// the document makes the whole class of bug unexploitable rather than relying
// on the escaping being perfect forever.
//
// The style allowance exists because SVG presentation attributes are how the
// writer colours a code at all.
const renderCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// setRenderSecurityHeaders applies the headers every rendered response needs.
func setRenderSecurityHeaders(w http.ResponseWriter) {
	// nosniff keeps a browser from re-interpreting a PNG as something
	// executable; the CSP covers the formats that are documents in their own
	// right.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", renderCSP)
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// cacheable sets the caching policy for a deterministic response.
//
// When authentication is on the response is marked private: a shared cache
// must not be able to serve one caller's code to another, even though the
// bytes are a pure function of the query.
func (s *Server) cacheable(w http.ResponseWriter) {
	scope := "public"
	if s.cfg.AuthMode == config.AuthRequired {
		scope = "private"
	}
	w.Header().Set("Cache-Control", scope+", max-age=300")
	w.Header().Add("Vary", "Accept, X-API-Key, Authorization")
}

// noCacheIfPrivate marks a JSON diagnostic response uncacheable when auth is
// on, since it may echo request-specific detail.
func (s *Server) noCacheIfPrivate(w http.ResponseWriter) {
	if s.cfg.AuthMode == config.AuthRequired {
		w.Header().Set("Cache-Control", "private, no-store")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
}

// strongETag is a strong validator over the exact response bytes.
func strongETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
}

// etagMatches implements the If-None-Match comparison, including the "*" form
// and a comma-separated list.
func etagMatches(header, etag string) bool {
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		// A weak validator still identifies the same representation for our
		// purposes: the body is byte-identical or it is not.
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

// contentDisposition builds the header, quoting the filename.
func contentDisposition(name string, attachment bool) string {
	verb := "inline"
	if attachment {
		verb = "attachment"
	}
	return fmt.Sprintf("%s; filename=%q", verb, name)
}
