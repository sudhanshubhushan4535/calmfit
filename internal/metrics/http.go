package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTP records the latency of every API request by route pattern, method and status class.
type HTTP struct {
	dur *prometheus.HistogramVec
}

// NewHTTP registers the request histogram on reg.
func NewHTTP(reg prometheus.Registerer) *HTTP {
	m := &HTTP{dur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "calmfit_http_request_duration_seconds",
		Help: "API request latency by route pattern, method and status class (2xx/3xx/4xx/5xx).",
		// 5 ms to 5 s: the k6 targets (50–300 ms) fall between buckets that are close together.
		Buckets: []float64{.005, .01, .025, .05, .075, .1, .15, .2, .3, .5, .75, 1, 2.5, 5},
	}, []string{"route", "method", "code"})}
	reg.MustRegister(m.dur)
	return m
}

// Wrap must wrap the ServeMux itself: the mux sets r.Pattern on the request it is given, so the
// route label is the registered pattern ("POST /workouts/{id}/sets"), never the raw path. That
// keeps label cardinality fixed. Requests that match no route are labelled "unmatched".
func (m *HTTP) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.dur.WithLabelValues(route, r.Method, statusClass(rec.status)).Observe(time.Since(start).Seconds())
	})
}

func statusClass(code int) string {
	if code < 100 || code > 599 {
		return "other"
	}
	return strconv.Itoa(code/100) + "xx"
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
