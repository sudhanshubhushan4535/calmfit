package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// count returns the histogram sample count for one label set (0 if absent).
func count(t *testing.T, reg *prometheus.Registry, route, method, code string) uint64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "calmfit_http_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labels(m) == route+"|"+method+"|"+code {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func labels(m *dto.Metric) string {
	v := map[string]string{}
	for _, l := range m.GetLabel() {
		v[l.GetName()] = l.GetValue()
	}
	return v["route"] + "|" + v["method"] + "|" + v["code"]
}

func TestHTTPLabelsByRoutePattern(t *testing.T) {
	reg := NewRegistry()
	m := NewHTTP(reg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /workouts/{id}/sets", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) })
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusInternalServerError) })
	mux.HandleFunc("GET /plain", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv := httptest.NewServer(m.Wrap(mux))
	defer srv.Close()

	for _, req := range []struct{ method, path string }{
		{"POST", "/workouts/11111111-1111-1111-1111-111111111111/sets"},
		{"POST", "/workouts/22222222-2222-2222-2222-222222222222/sets"},
		{"GET", "/boom"},
		{"GET", "/plain"},
		{"GET", "/no/such/route"},
	} {
		r, _ := http.NewRequest(req.method, srv.URL+req.path, nil)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	cases := []struct {
		route, method, code string
		want                uint64
	}{
		// Two different ids, one series: the label is the pattern, not the path.
		{"POST /workouts/{id}/sets", "POST", "2xx", 2},
		{"GET /boom", "GET", "5xx", 1},
		{"GET /plain", "GET", "2xx", 1}, // Write without WriteHeader is a 200
		{"unmatched", "GET", "4xx", 1},
	}
	for _, c := range cases {
		if got := count(t, reg, c.route, c.method, c.code); got != c.want {
			t.Errorf("%s %s %s: count %d, want %d", c.route, c.method, c.code, got, c.want)
		}
	}
}

func TestStartServesMetrics(t *testing.T) {
	reg := NewRegistry()
	m := NewHTTP(reg)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {})
	m.Wrap(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr, err := Start(ctx, "127.0.0.1:0", reg)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		`calmfit_http_request_duration_seconds_count{code="2xx",method="GET",route="GET /x"} 1`,
		"go_goroutines",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
