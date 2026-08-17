package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/el-amin-dev/barqr/internal/version"
)

// routes builds the router.
//
// Every route lives under /v1 so a future /v2 can coexist without breaking
// clients. The middleware order matters and is chosen deliberately:
//
//	recover     a panic must never take the process down
//	requestID   everything below it logs and reports the same id
//	metrics     measures the whole stack, including rejections
//	logging     one record per request, whatever the outcome
//	cors        preflights are answered before auth, since a browser
//	            preflight carries no credentials by design
//	bodyLimit   cap the body before anything reads it
//	attempts    throttle key guesses BEFORE auth, since auth answers 401
//	            without calling the next handler and so a limiter placed
//	            after it would never see a failed attempt
//	auth        reject unauthenticated callers before spending work
//	rateLimit   buckets by authenticated key, so it runs after auth
//	timeout     bound the handler
//	concurrency queue behind the semaphore, respecting that deadline
//
// The operational endpoints sit outside auth: a liveness probe cannot be
// expected to hold an API key, and they disclose nothing sensitive.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(withRequestID)
	r.Use(s.withMetrics)
	r.Use(s.withLogging)
	r.Use(s.withCORS)

	r.NotFound(s.handleNotFound)
	r.MethodNotAllowed(s.handleMethodNotAllowed)

	// The documentation UI sits outside the JSON API group: a browser cannot
	// put an API key on a top-level navigation, so these routes run their own
	// key resolution (header, cookie, or one-shot query) and answer with an
	// HTML prompt instead of a JSON 401. The landing page is public — it
	// discloses what the service is, which /v1/version already does.
	if s.cfg.Docs {
		// The landing page is public: it names the service and links to the
		// documentation, and now carries only the public capability payload.
		r.Get("/", s.handleLanding)
		r.Head("/", s.handleLanding)

		// The docs views check a key themselves — a browser cannot put one on
		// a navigation — so they get the same body limit, attempt throttle,
		// timeout and semaphore as the API. Without the throttle these routes
		// would be an unauthenticated, unmetered oracle for guessing a key.
		r.Group(func(r chi.Router) {
			r.Use(s.withBodyLimit)
			r.Use(s.withAttemptLimit)
			r.Use(s.withTimeout)
			r.Use(s.withConcurrency)

			r.Get("/v1/docs", s.handleDocs)
			r.Post("/v1/docs", s.handleDocs)
			r.Get("/v1/docs/*", s.handleDocs)
		})
	}

	r.Route("/v1", func(r chi.Router) {
		// Unauthenticated: probes and build identity.
		r.Get("/healthz", s.handleHealthz)
		r.Get("/readyz", s.handleReadyz)
		r.Get("/version", s.handleVersion)

		// Authenticated: everything that does work.
		r.Group(func(r chi.Router) {
			r.Use(s.withBodyLimit)
			r.Use(s.withAttemptLimit)
			r.Use(s.withAuth)
			r.Use(s.withRateLimit)
			r.Use(s.withTimeout)
			r.Use(s.withConcurrency)

			r.Get("/symbologies", s.handleSymbologies)
			r.Get("/openapi.json", s.handleOpenAPI)

			r.Get("/qr", s.handleQR)
			r.Post("/qr", s.handleQR)

			r.Get("/barcode/{symbology}", s.handleBarcode)
			r.Post("/barcode/{symbology}", s.handleBarcode)

			r.Get("/build/{type}", s.handleBuild)
			r.Post("/build/{type}", s.handleBuild)

			r.Post("/validate", s.handleValidate)
			r.Post("/decode", s.handleDecode)
			r.Post("/batch", s.handleBatch)

			r.Post("/sheet", s.handleSheet)
			r.Get("/sheet/templates", s.handleSheetTemplates)

			r.Get("/preset", s.handlePresetList)
			r.Get("/preset/{name}", s.handlePreset)
			r.Post("/preset/{name}", s.handlePreset)
		})
	})

	if s.metrics != nil {
		// Behind the same key as the API. It reports heap size, goroutine and
		// file-descriptor counts and per-route request rates — useful to an
		// operator and equally useful to someone sizing up the process.
		r.Group(func(r chi.Router) {
			r.Use(s.withAttemptLimit)
			r.Use(s.withAuth)
			r.Handle("/metrics", s.metrics.handler())
		})
	}

	return r
}

// routePattern is the chi route template for a request, used as a metrics
// label. Falling back to "other" keeps an unmatched path from creating a new
// time series per URL.
func routePattern(r *http.Request) string {
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			return p
		}
	}
	return "other"
}

// handleHealthz reports process liveness. It answers 200 for as long as the
// process can serve requests at all, including while draining, because a
// failing liveness probe means "restart me", not "stop sending me traffic".
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz reports whether this replica wants traffic. It flips to 503 as
// soon as shutdown begins so that a load balancer can drain the replica before
// in-flight requests are cut.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		s.writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"status": "draining"})
		return
	}
	s.writeJSON(w, r, http.StatusOK, map[string]string{"status": "ready"})
}

// handleVersion reports the build identity of the running binary.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, r, http.StatusOK, version.Get())
}

// handleNotFound answers an unrouted path in the standard error shape, so a
// client parsing errors never has to handle chi's plain-text default.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	f := newFault(http.StatusNotFound, CodeNotFound, "no such endpoint: %s", r.URL.Path)
	f.Hint = "see /v1/openapi.json for the available endpoints"
	s.fail(w, r, f)
}

// handleMethodNotAllowed answers a wrong method in the standard error shape.
func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	s.fail(w, r, newFault(http.StatusMethodNotAllowed, CodeMethodNotAllowed,
		"%s is not allowed on %s", r.Method, r.URL.Path))
}

// writeJSON serialises v as the response body.
//
// The payload is marshalled before any header is written so that a marshalling
// failure still produces a valid status code rather than a truncated 200. A
// write failure after that point means the client is gone; it is logged at
// debug level and otherwise ignored.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		s.log.Error("encoding response failed",
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()))
		status, body = http.StatusInternalServerError,
			[]byte(`{"error":{"code":"INTERNAL","message":"response encoding failed"}}`)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.log.Debug("writing response failed",
			slog.String("path", r.URL.Path),
			slog.String("error", err.Error()))
	}
}
