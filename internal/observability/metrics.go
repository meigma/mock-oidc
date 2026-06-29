package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a private Prometheus registry and the HTTP server metrics. It is
// constructed per server (no package globals), so tests can build independent
// instances without duplicate-registration panics.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

// NewMetrics constructs a Metrics with Go runtime, process, and HTTP collectors.
func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests, labeled by method, route, and status.",
		},
		[]string{"method", "route", "status"},
	)
	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds, labeled by method and route.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)
	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_requests_in_flight",
		Help: "Number of HTTP requests currently being served.",
	})

	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		requests,
		duration,
		inFlight,
	)

	return &Metrics{
		registry: registry,
		requests: requests,
		duration: duration,
		inFlight: inFlight,
	}
}

// Middleware records request count, duration, and the in-flight gauge per request.
func (m *Metrics) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.inFlight.Inc()
			defer m.inFlight.Dec()

			start := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r)

			route := routePattern(r)
			method := normalizeMethod(r.Method)
			m.duration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			m.requests.WithLabelValues(method, route, strconv.Itoa(wrapped.Status())).Inc()
		})
	}
}

// Handler returns the HTTP handler exposing the registered metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// routePattern returns the matched chi route template to keep metric label
// cardinality bounded, falling back to a constant for unmatched requests.
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}

	return "unmatched"
}

// methodOther is the bucket for unrecognized HTTP method tokens.
const methodOther = "other"

// normalizeMethod bounds the method label's cardinality. The HTTP server accepts
// arbitrary method tokens, so any unrecognized method collapses to methodOther to
// stop scanners from creating an unbounded number of time series.
func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace:
		return method
	default:
		return methodOther
	}
}
