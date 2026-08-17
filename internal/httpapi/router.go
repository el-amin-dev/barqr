package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/el-amin-dev/barqr/internal/version"
)

// routes builds the router. Every route lives under /v1 so that a future
// /v2 can coexist without breaking clients, and the operational endpoints
// are versioned along with everything else for consistency.
//
// The middleware stack is deliberately minimal here: recovery is a backstop
// against a panic escaping a handler, not a strategy. The full stack — request
// id, logging, metrics, auth, rate limiting, body limits, timeouts, CORS, and
// caching — is layered on in the request-layer milestone.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/healthz", s.handleHealthz)
		r.Get("/readyz", s.handleReadyz)
		r.Get("/version", s.handleVersion)
	})

	return r
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
