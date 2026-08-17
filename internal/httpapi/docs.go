package httpapi

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/el-amin-dev/barqr/internal/builder"
	"github.com/el-amin-dev/barqr/internal/config"
	"github.com/el-amin-dev/barqr/internal/encoder"
	"github.com/el-amin-dev/barqr/internal/render"
	"github.com/el-amin-dev/barqr/internal/sheet"
	"github.com/el-amin-dev/barqr/internal/version"
	"github.com/el-amin-dev/barqr/internal/writer"
)

// docsFS holds the documentation UI: a hand-written dashboard plus the Swagger
// UI and ReDoc bundles.
//
// They are embedded rather than loaded from a CDN because the runtime image is
// distroless and may have no egress at all, and because a docs page that only
// works with internet access is not documentation an operator can rely on at
// three in the morning. The bundles are stored pre-gzipped: 3.1 MB of
// JavaScript becomes 853 KB in the binary and is served without recompressing
// it on every request.
//
//go:embed docsui
var docsFS embed.FS

// docsCookie carries the API key across page navigations.
//
// A browser cannot attach an X-API-Key header to a top-level navigation, so
// the key has to travel some other way for the docs pages to be reachable at
// all. It is scoped to /v1, marked SameSite=Strict so it is never sent from
// another site, and marked Secure whenever the request arrived over TLS.
//
// It is deliberately NOT HttpOnly: the dashboard's "try it" panel has to read
// the key back to send it as a header on its own fetch calls. That is a real
// trade-off, and the reason BARQR_DOCS exists — an operator who does not want
// a key sitting in a cookie can turn the whole UI off.
const docsCookie = "barqr_key"

// docsKeyParam lets a key arrive in the URL once, on the way to the cookie.
// The dashboard strips it from the address bar immediately. barqr never logs
// query strings, so it does not reach the request log.
const docsKeyParam = "key"

// docsCSP is the policy for the documentation pages.
//
// It is looser than the one on rendered codes because Swagger UI and ReDoc
// genuinely need inline styles and a blob worker, but it still pins scripts to
// this origin: nothing is loaded from a CDN and nothing external can be
// fetched, which is what makes an embedded bundle worth the bytes.
const docsCSP = "default-src 'none'; " +
	"script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"worker-src 'self' blob:; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'self'"

// docsAsset is one embedded file and how to serve it.
type docsAsset struct {
	path       string
	mime       string
	compressed bool
}

// docsAssets maps a route suffix onto an embedded file.
var docsAssets = map[string]docsAsset{
	"swagger-ui.css":    {"docsui/swagger-ui.css.gz", "text/css; charset=utf-8", true},
	"swagger-ui.js":     {"docsui/swagger-ui.js.gz", "text/javascript; charset=utf-8", true},
	"swagger-preset.js": {"docsui/swagger-preset.js.gz", "text/javascript; charset=utf-8", true},
	"redoc.js":          {"docsui/redoc.js.gz", "text/javascript; charset=utf-8", true},
}

// docsAuthorize resolves the API key for a documentation request.
//
// It accepts the same header forms the rest of the API does, plus a cookie and
// a one-shot query parameter, because a browser navigating to a page can send
// neither header. It returns the key it accepted so the caller can refresh the
// cookie.
func (s *Server) docsAuthorize(r *http.Request) (key string, ok bool) {
	if s.cfg.AuthMode == config.AuthOpen {
		return "", true
	}

	candidates := []string{
		r.Header.Get("X-API-Key"),
		strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")),
		r.URL.Query().Get(docsKeyParam),
	}
	if c, err := r.Cookie(docsCookie); err == nil {
		candidates = append(candidates, c.Value)
	}
	if r.Method == http.MethodPost {
		// The unlock form posts the key rather than putting it in the URL.
		if err := r.ParseForm(); err == nil {
			candidates = append(candidates, r.PostFormValue(docsKeyParam))
		}
	}

	for _, c := range candidates {
		if c != "" && s.cfg.AuthorizeKey(c) {
			return c, true
		}
	}
	return "", false
}

// setDocsCookie stores an accepted key for subsequent navigations.
func setDocsCookie(w http.ResponseWriter, r *http.Request, key string) {
	if key == "" {
		return
	}
	// #nosec G124 -- deliberately readable by the page's own JS, which must send
	// the key as an X-API-Key header on its fetch calls; BARQR_DOCS=false turns
	// the whole surface off for a deployment that will not accept that.
	http.SetCookie(w, &http.Cookie{
		Name:     docsCookie,
		Value:    key,
		Path:     "/v1",
		MaxAge:   int((8 * time.Hour).Seconds()),
		SameSite: http.SameSiteStrictMode,
		Secure:   isSecureRequest(r),
	})
}

// isSecureRequest reports whether the connection reached barqr over TLS,
// including through a terminating proxy.
//
// Every deployment this project documents terminates TLS upstream — compose
// `expose:`, a Kubernetes ClusterIP — so r.TLS is nil in exactly the cases
// that matter, and trusting it alone would write the API key into a cookie
// with no Secure flag. X-Forwarded-Proto is client-settable, but the failure
// direction is safe: a forged header can only make the cookie MORE
// restrictive, never less.
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i] // the left-most hop is the client-facing one
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// handleLanding serves the public entry page.
//
// It needs no API key. It names the service, links to the documentation, and
// says plainly that barqr should not be on the public internet — all of which
// is better said out loud than discovered.
func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	s.serveDocsPage(w, r, "docsui/landing.html", scopePublic)
}

// handleDocs serves the documentation UI.
//
// One route family, three views: a dashboard that builds a request
// interactively, Swagger UI for trying every endpoint, and ReDoc for reading.
// All three consume the same generated /v1/openapi.json, so none of them can
// describe an endpoint this build does not have.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	key, ok := s.docsAuthorize(r)
	if !ok {
		s.serveDocsUnlock(w, r)
		return
	}
	setDocsCookie(w, r, key)

	view := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/docs"), "/")

	if asset, isAsset := docsAssets[view]; isAsset {
		s.serveDocsAsset(w, r, asset)
		return
	}

	page := "docsui/index.html"
	switch view {
	case "", "index.html":
	case "swagger":
		page = "docsui/swagger.html"
	case "redoc":
		page = "docsui/redoc.html"
	default:
		s.fail(w, r, newFault(http.StatusNotFound, CodeNotFound,
			"no such documentation view: %q", view))
		return
	}

	s.serveDocsPage(w, r, page, scopeFull)
}

// docsPayload is everything the documentation pages render themselves from.
//
// This is the point of the whole design: the pages contain no hand-written
// list of symbologies, formats, shapes, or endpoints. They are built from the
// same registries the router dispatches on, so a capability that exists here
// exists in the service, and one that does not cannot be documented by
// accident. A build tag that drops a symbology drops it from these pages in
// the same commit, with nobody remembering to.
func (s *Server) docsPayload(scope payloadScope) map[string]any {
	formats := make([]map[string]any, 0, len(writer.All()))
	for _, wr := range writer.All() {
		formats = append(formats, map[string]any{
			"name": wr.Name(), "mime": wr.MIME(),
			"extension": wr.Extension(), "binary": wr.Binary(),
		})
	}

	builders := make([]map[string]any, 0, len(builder.All()))
	for _, b := range builder.All() {
		builders = append(builders, map[string]any{
			"name": b.Name(), "fields": b.Fields(),
		})
	}

	templates := make([]map[string]any, 0, len(sheet.Templates()))
	for _, t := range sheet.Templates() {
		templates = append(templates, map[string]any{
			"name": t.Name, "title": t.Title, "per_page": t.Layout.PerPage(),
		})
	}

	caps := encoder.All()
	available := 0
	for _, c := range caps {
		if c.Available {
			available++
		}
	}

	out := map[string]any{
		"version":     version.Get(),
		"auth":        map[string]any{"required": s.cfg.AuthMode == config.AuthRequired},
		"symbologies": caps,
		"formats":     formats,
		"builders":    builders,
		"templates":   templates,
		"defaults": map[string]any{
			"symbology": encoder.QR, "format": s.cfg.DefaultFormat,
			"ecc": s.cfg.DefaultECC, "scale": writer.DefaultScale,
			"units": []string{string(writer.UnitPx), string(writer.UnitMM), string(writer.UnitIn)},
		},
		"counts": map[string]any{
			"symbologies": available, "symbologies_total": len(caps),
			"builders": len(builders), "formats": len(formats),
		},
		"endpoints": s.docsEndpoints(),
		"fields":    knownPaths(),
		"styles": map[string]any{
			"module": render.ModuleShapes(),
			"eye":    render.EyeShapes(),
		},
	}

	if scope == scopeFull {
		// Operator-facing detail: what this instance was configured with. The
		// rate limit in particular tells a caller how to pace a brute-force
		// attempt, so it stays behind the key.
		out["presets"] = s.presets.All()
		out["limits"] = map[string]any{
			"max_canvas_px": s.cfg.MaxCanvasPx, "max_body_bytes": s.cfg.MaxBody,
			"max_batch_items": s.cfg.MaxBatchItems,
			"rate_limit":      s.cfg.RateLimit.String(),
		}
		out["auth"] = map[string]any{
			"mode": string(s.cfg.AuthMode), "required": s.cfg.AuthMode == config.AuthRequired,
		}
	}

	return out
}

// docsEndpoints lists the routes from the same generated OpenAPI document the
// Swagger and ReDoc views consume, rather than from a second hand-kept list.
func (s *Server) docsEndpoints() []map[string]any {
	doc := s.openAPIPaths()
	routes := make([]string, 0, len(doc))
	for route := range doc {
		routes = append(routes, route)
	}
	sort.Strings(routes)

	out := make([]map[string]any, 0, len(routes))
	for _, route := range routes {
		ops, ok := doc[route].(map[string]any)
		if !ok {
			continue
		}
		methods := make([]string, 0, len(ops))
		summary := ""
		for method, op := range ops {
			methods = append(methods, strings.ToUpper(method))
			if o, isMap := op.(map[string]any); isMap && summary == "" {
				if sum, isStr := o["summary"].(string); isStr {
					summary = sum
				}
			}
		}
		sort.Strings(methods)
		out = append(out, map[string]any{
			"route": route, "methods": methods, "summary": summary,
		})
	}
	return out
}

// payloadScope selects how much of the capability payload a page receives.
type payloadScope int

const (
	// scopePublic is what an unauthenticated visitor may see: the product's
	// shape, and nothing about how this instance is configured.
	scopePublic payloadScope = iota
	// scopeFull is the authenticated view, including limits and presets.
	scopeFull
)

// substitute fills a page's placeholders.
//
// It exists so that every page goes through one path: the unlock page used to
// be written raw and shipped a literal {{STYLE}} to the browser, which is
// exactly the kind of drift a shared helper prevents.
func (s *Server) substitute(body []byte, scope payloadScope) ([]byte, error) {
	style, err := docsFS.ReadFile("docsui/theme.css")
	if err != nil {
		return nil, errors.New("documentation stylesheet is missing from the build")
	}

	data, err := json.Marshal(s.docsPayload(scope))
	if err != nil {
		return nil, errors.New("documentation payload could not be built")
	}
	// The payload sits in a <script> block, so a literal "</script>" in any
	// registered name would end it early. Escaping "<" as \u003c closes that
	// off without changing what JSON.parse sees.
	data = bytes.ReplaceAll(data, []byte("<"), []byte(`\u003c`))

	body = bytes.ReplaceAll(body, []byte("{{STYLE}}"), style)
	body = bytes.ReplaceAll(body, []byte("{{DATA}}"), data)
	body = bytes.ReplaceAll(body, []byte("{{VERSION}}"), []byte(version.Version))
	return body, nil
}

// serveDocsPage writes an embedded HTML page.
//
// Three placeholders are substituted: the shared stylesheet, the running
// version, and the capability payload the page renders itself from. Injecting
// rather than fetching keeps the public landing page working without an API
// key, and means a page can never render a stale snapshot of the API.
func (s *Server) serveDocsPage(w http.ResponseWriter, r *http.Request, name string, scope payloadScope) {
	body, err := docsFS.ReadFile(name)
	if err != nil {
		s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal,
			"documentation page is missing from the build"))
		return
	}

	body, err = s.substitute(body, scope)
	if err != nil {
		s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal, "%s", err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", docsCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// The page embeds no secret, but it is reached with one; a shared cache
	// must not hold it.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		s.log.Debug("writing docs page failed", "error", err.Error())
	}
}

// serveDocsAsset writes an embedded script or stylesheet.
//
// The bundles are stored gzipped, so a client that accepts gzip — every
// browser — gets the stored bytes untouched. The decompressing fallback exists
// for a client that does not, and costs nothing in the normal case.
func (s *Server) serveDocsAsset(w http.ResponseWriter, r *http.Request, a docsAsset) {
	body, err := docsFS.ReadFile(a.path)
	if err != nil {
		s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal,
			"documentation asset is missing from the build"))
		return
	}

	w.Header().Set("Content-Type", a.mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The bundles are immutable for the life of a build, and they are large.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")

	if a.compressed && !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		zr, zerr := gzip.NewReader(bytes.NewReader(body))
		if zerr != nil {
			s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal,
				"documentation asset is corrupt"))
			return
		}
		defer func() { _ = zr.Close() }()

		w.WriteHeader(http.StatusOK)
		// #nosec G110 -- the source is an asset compressed at build time and
		// embedded in this binary, not anything a caller supplied.
		if _, err := io.Copy(w, zr); err != nil {
			s.log.Debug("writing docs asset failed", "error", err.Error())
		}
		return
	}

	if a.compressed {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		s.log.Debug("writing docs asset failed", "error", err.Error())
	}
}

// serveDocsUnlock asks for an API key.
//
// A browser navigating here cannot send a header, so answering with the usual
// JSON 401 would be a dead end. A non-browser client still gets the JSON
// error, which keeps the API contract intact for anything scripted.
func (s *Server) serveDocsUnlock(w http.ResponseWriter, r *http.Request) {
	// Reaching here means a key was absent or wrong. Charge it, or the docs
	// routes are the unthrottled way to guess one.
	s.penaliseAttempt(r)

	if !acceptsHTML(r) {
		f := newFault(http.StatusUnauthorized, CodeUnauthorized,
			"a valid API key is required")
		f.Hint = "send it as X-API-Key, or open /v1/docs in a browser"
		s.fail(w, r, f)
		return
	}

	body, err := docsFS.ReadFile("docsui/unlock.html")
	if err != nil {
		s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal,
			"documentation page is missing from the build"))
		return
	}
	// The unlock page is served to an unauthenticated caller by definition, so
	// it gets the public payload.
	if body, err = s.substitute(body, scopePublic); err != nil {
		s.fail(w, r, newFault(http.StatusInternalServerError, CodeInternal, "%s", err))
		return
	}

	// Clear a stale cookie so a wrong key does not silently persist.
	// #nosec G124 -- an expiry, carrying no value at all.
	http.SetCookie(w, &http.Cookie{
		Name: docsCookie, Value: "", Path: "/v1", MaxAge: -1,
		SameSite: http.SameSiteStrictMode, Secure: isSecureRequest(r),
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", docsCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "private, no-store")

	// 401 with an HTML body: the status is still the truth for anything that
	// reads it, and a browser renders the form.
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(body); err != nil {
		s.log.Debug("writing unlock page failed", "error", err.Error())
	}
}

// acceptsHTML reports whether the client looks like a browser navigating.
func acceptsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
