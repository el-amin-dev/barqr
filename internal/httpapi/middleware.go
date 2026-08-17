package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/el-amin-dev/barqr/internal/config"
)

// ctxKey is the private key type for values this package puts on a context.
type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxAPIKeyID
)

// requestIDHeader is both read and written: an upstream proxy that already
// assigns one keeps its value, so a trace spans both hops.
const requestIDHeader = "X-Request-Id"

// maxInboundRequestID bounds an upstream-supplied id. An unbounded value from
// a client would end up in every log line for that request.
const maxInboundRequestID = 64

// requestIDFrom returns the id assigned to this request, or empty.
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxRequestID).(string)
	return id
}

// apiKeyIDFrom returns a stable, non-secret identifier for the authenticated
// key: its position in the configured list. It is what rate limiting buckets
// on and what appears in logs, so the key itself never does.
func apiKeyIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxAPIKeyID).(string)
	return id
}

// withRequestID assigns or adopts a request id and echoes it on the response.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

// sanitiseRequestID keeps only characters that are safe to embed in a log
// line, dropping anything that could forge a second record.
func sanitiseRequestID(raw string) string {
	if len(raw) > maxInboundRequestID {
		raw = raw[:maxInboundRequestID]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// requestIDEncoding is unpadded base32 without ambiguous characters, so an id
// can be read aloud from a log and typed into a search box.
var requestIDEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newRequestID returns a fresh random identifier.
func newRequestID() string {
	var b [10]byte
	// crypto/rand.Read never fails on any platform Go supports.
	_, _ = rand.Read(b[:])
	return requestIDEncoding.EncodeToString(b[:])
}

// statusRecorder captures the status and size for logging and metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
		s.ResponseWriter.WriteHeader(code)
	}
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// withLogging emits one structured record per request.
//
// It logs the path, method, status, duration, and size — never the query
// string and never the body. A barqr payload can contain a Wi-Fi password or
// a TOTP secret, so request content is not log material at any level.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.log.LogAttrs(r.Context(), levelForStatus(rec.status), "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int("bytes", rec.bytes),
			slog.Duration("took", time.Since(start)),
			slog.String("request_id", requestIDFrom(r.Context())),
			slog.String("key", apiKeyIDFrom(r.Context())))
	})
}

// levelForStatus keeps healthy traffic at debug so an info-level deployment
// logs problems rather than a firehose of successful renders.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

// withAuth enforces API-key authentication.
//
// Both the X-API-Key header and an Authorization: Bearer token are accepted,
// because the first is what a curl user reaches for and the second is what an
// HTTP client library makes easy. Comparison is constant time and happens in
// config.AuthorizeKey; this layer never sees a stored key.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AuthMode == config.AuthOpen {
			next.ServeHTTP(w, r)
			return
		}

		presented := r.Header.Get("X-API-Key")
		if presented == "" {
			if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				presented = strings.TrimSpace(token)
			}
		}

		if presented == "" || !s.cfg.AuthorizeKey(presented) {
			// A 401 without a WWW-Authenticate challenge: barqr is a machine
			// API, and a browser popping a basic-auth dialog helps nobody.
			f := newFault(http.StatusUnauthorized, CodeUnauthorized,
				"a valid API key is required")
			f.Hint = "send it as X-API-Key or Authorization: Bearer"
			s.fail(w, r, f)
			return
		}

		ctx := context.WithValue(r.Context(), ctxAPIKeyID, s.cfg.KeyID(presented))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withBodyLimit caps the request body.
//
// http.MaxBytesReader makes the read itself fail past the limit, so a body
// that lies about its Content-Length is caught while streaming rather than
// after it has been buffered.
func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBody)
		}
		next.ServeHTTP(w, r)
	})
}

// withTimeout bounds how long a handler may run.
//
// The context deadline is what handlers honour; it is not a hard kill. Every
// render path checks ctx.Err() between stages, which is enough because no
// single stage is unbounded once the canvas size is capped.
func (s *Server) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withConcurrency bounds work in flight with a semaphore.
//
// Requests queue rather than pile up: a burst of renders on an eight-core box
// runs eight at a time and the rest wait, which keeps latency predictable
// instead of collapsing everything at once. A request that waits past its own
// deadline is shed with 503 rather than starting work nobody is waiting for.
func (s *Server) withConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			f := newFault(http.StatusServiceUnavailable, CodeOverloaded,
				"server is at capacity; the request timed out while queued")
			f.Hint = "retry with backoff, or raise BARQR_CONCURRENCY"
			s.fail(w, r, f)
		}
	})
}

// withRateLimit applies a per-key token bucket.
//
// Buckets are keyed by the authenticated key id, or by remote address when
// auth is open. The address is only a coarse signal behind a proxy, which is
// why the documented deployment keeps auth on.
func (s *Server) withRateLimit(next http.Handler) http.Handler {
	if s.cfg.RateLimit.Unlimited() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket := apiKeyIDFrom(r.Context())
		if bucket == "" {
			bucket = clientAddr(r)
		}

		if !s.limiter.allow(bucket) {
			f := newFault(http.StatusTooManyRequests, CodeRateLimited,
				"rate limit of %s exceeded", s.cfg.RateLimit)
			w.Header().Set("Retry-After", "1")
			s.fail(w, r, f)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientAddr is the remote address without its port.
//
// X-Forwarded-For is deliberately ignored: barqr does not know whether it sits
// behind a trusted proxy, and trusting a client-settable header would let
// anyone reset their own rate limit.
func clientAddr(r *http.Request) string {
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}

// withCORS answers preflights and adds the configured origin headers.
//
// With no configured origins no CORS headers are emitted at all, which is the
// correct default for a service that is not meant to be called from a browser
// on another origin.
func (s *Server) withCORS(next http.Handler) http.Handler {
	if len(s.cfg.CORSOrigins) == 0 {
		return next
	}
	allowed := make(map[string]bool, len(s.cfg.CORSOrigins))
	wildcard := false
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" {
			wildcard = true
		}
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (wildcard || allowed[origin]) {
			// Echo the specific origin rather than "*" so that a response can
			// still be cached per origin and credentials remain possible.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limiter is a per-bucket token bucket.
//
// It is a plain map behind a mutex rather than a sharded structure: the
// contended path is a single map lookup under a lock held for nanoseconds,
// and barqr's concurrency is already bounded by the semaphore above.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    config.Rate
	// now is injectable so the tests do not sleep.
	now func() time.Time
}

// bucket is one client's allowance.
type bucket struct {
	tokens float64
	last   time.Time
}

// newLimiter builds a limiter for a rate.
func newLimiter(rate config.Rate) *limiter {
	return &limiter{buckets: make(map[string]*bucket), rate: rate, now: time.Now}
}

// allow consumes a token, refilling continuously since the last request.
func (l *limiter) allow(key string) bool {
	if l.rate.Unlimited() {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	capacity := float64(l.rate.Count)
	perSecond := capacity / l.rate.Per.Seconds()

	b, ok := l.buckets[key]
	if !ok {
		// A new bucket starts full, minus the request that created it.
		l.buckets[key] = &bucket{tokens: capacity - 1, last: now}
		return true
	}

	b.tokens = min(capacity, b.tokens+now.Sub(b.last).Seconds()*perSecond)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have been idle long enough to have refilled, so
// the map cannot grow without bound on a service with many callers.
func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-2 * l.rate.Per)
	for key, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}
