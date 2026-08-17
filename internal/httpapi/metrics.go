package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics holds the Prometheus collectors for one server.
//
// They live on the Server rather than in package-level variables so that two
// servers in one process — which the tests do routinely — do not fight over a
// global registry. It also keeps the "no global mutable state" rule intact.
type metrics struct {
	registry *prometheus.Registry

	requests  *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	renders   *prometheus.CounterVec
	bytesOut  *prometheus.CounterVec
	inFlight  prometheus.Gauge
	rateLimit prometheus.Counter
}

// newMetrics builds and registers the collectors.
func newMetrics() *metrics {
	reg := prometheus.NewRegistry()

	m := &metrics{
		registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "barqr_http_requests_total",
			Help: "HTTP requests by route, method, and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "barqr_http_request_duration_seconds",
			Help: "Request latency by route.",
			// A plain QR render is sub-millisecond, so the buckets start far
			// below the Prometheus defaults; the tail matters for large
			// canvases and batch jobs.
			Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10},
		}, []string{"route"}),
		renders: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "barqr_renders_total",
			Help: "Codes rendered by symbology and output format.",
		}, []string{"symbology", "format"}),
		bytesOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "barqr_output_bytes_total",
			Help: "Bytes of rendered output by format.",
		}, []string{"format"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "barqr_requests_in_flight",
			Help: "Requests currently being served.",
		}),
		rateLimit: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "barqr_rate_limited_total",
			Help: "Requests rejected by the rate limiter.",
		}),
	}

	reg.MustRegister(
		m.requests, m.duration, m.renders, m.bytesOut, m.inFlight, m.rateLimit,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// handler serves the exposition format.
func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		// An error while gathering must not take the endpoint down: a partial
		// scrape is far more useful than a 500.
		ErrorHandling: promhttp.ContinueOnError,
	})
}

// observe records one completed request.
func (m *metrics) observe(route, method string, status int, took time.Duration) {
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(route).Observe(took.Seconds())
}

// observeRender records one rendered code.
func (m *metrics) observeRender(symbology, format string, size int) {
	m.renders.WithLabelValues(symbology, format).Inc()
	m.bytesOut.WithLabelValues(format).Add(float64(size))
}

// withMetrics instruments a request.
//
// The route label is the chi pattern, not the raw path, so a per-symbology
// path cannot explode the label cardinality.
func (s *Server) withMetrics(next http.Handler) http.Handler {
	if s.metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}

		s.metrics.inFlight.Inc()
		defer s.metrics.inFlight.Dec()

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		if rec.status == http.StatusTooManyRequests {
			s.metrics.rateLimit.Inc()
		}
		s.metrics.observe(routePattern(r), r.Method, rec.status, time.Since(start))
	})
}
