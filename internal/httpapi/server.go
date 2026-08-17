// Package httpapi wires the barqr HTTP surface: routing, middleware, request
// decoding, and error mapping.
//
// The package owns the server lifecycle as well as the routes, so that
// cmd/barqr stays a thin wiring layer: parse config, build a Server, Run it
// until the context is cancelled.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/el-amin-dev/barqr/internal/config"
)

// writeTimeoutMargin is added to the configured request timeout when setting
// the server's write deadline, so that a request cancelled by the timeout
// middleware still has room to write its error response.
const writeTimeoutMargin = 5 * time.Second

// Server is the barqr HTTP service.
//
// A Server is created by New, its routes are fixed at construction, and it is
// driven by Run. It holds no state beyond readiness, in keeping with the
// stateless process model.
type Server struct {
	cfg  *config.Config
	log  *slog.Logger
	http *http.Server

	// ready reports whether the process is willing to receive traffic. It is
	// false before Serve starts and again as soon as shutdown begins, which
	// lets a load balancer drain this replica before connections are cut.
	ready atomic.Bool
}

// New builds a Server from an already validated configuration.
func New(cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{cfg: cfg, log: log}
	s.http = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.RequestTimeout,
		WriteTimeout:      cfg.RequestTimeout + writeTimeoutMargin,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Handler exposes the routed handler for testing and for embedding barqr in
// another server.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Listen binds the configured address and returns the listener. It is
// separate from Serve so that a caller — a test, or a socket-activated
// deployment — can supply its own listener.
func (s *Server) Listen(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp", s.cfg.Addr())
}

// Run binds the configured address and serves until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := s.Listen(ctx)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx is cancelled, then stops
// advertising readiness and drains in-flight requests for at most
// BARQR_SHUTDOWN_GRACE before forcing connections closed.
//
// It returns nil on a clean shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.ready.Store(true)
	s.log.Info("listening",
		slog.String("addr", ln.Addr().String()),
		slog.String("auth_mode", string(s.cfg.AuthMode)),
		slog.Int("api_keys", s.cfg.APIKeyCount()))

	serveErr := make(chan error, 1)
	go func() {
		err := s.http.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		s.ready.Store(false)
		return err
	case <-ctx.Done():
	}

	s.ready.Store(false)
	s.log.Info("shutting down", slog.Duration("grace", s.cfg.ShutdownGrace))

	// Use a background parent: ctx is already cancelled, and Shutdown needs
	// a deadline of its own to bound the drain.
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownGrace)
	defer cancel()

	if err := s.http.Shutdown(drainCtx); err != nil {
		s.log.Warn("graceful shutdown timed out, closing connections",
			slog.String("error", err.Error()))
		if closeErr := s.http.Close(); closeErr != nil {
			return closeErr
		}
	}

	return <-serveErr
}
