package observability

import (
	"strings"
	"sync"
	"testing"
)

func TestCountersHistogramAndRender(t *testing.T) {
	r := New()
	r.IncRequests("/api/recall", "2xx")
	r.IncRequests("/api/recall", "2xx")
	r.IncRequests("/api/recall", "4xx")
	r.IncRequests("/api/stats", "2xx")
	r.ObserveDuration("/api/recall", 0.007) // bucket le=0.01
	r.ObserveDuration("/api/recall", 3.0)   // bucket le=5
	r.IncInFlight()
	r.IncInFlight()
	r.DecInFlight()
	r.IncSystem2Cycle("ran")
	r.IncSystem2Cycle("skipped")

	var sb strings.Builder
	r.Render(&sb)
	out := sb.String()

	for _, want := range []string{
		"# HELP ladym_http_requests_total ",
		"# TYPE ladym_http_requests_total counter",
		`ladym_http_requests_total{endpoint="/api/recall",status="2xx"} 2`,
		`ladym_http_requests_total{endpoint="/api/recall",status="4xx"} 1`,
		`ladym_http_requests_total{endpoint="/api/stats",status="2xx"} 1`,
		"# TYPE ladym_http_request_duration_seconds histogram",
		`ladym_http_request_duration_seconds_bucket{endpoint="/api/recall",le="0.005"} 0`,
		`ladym_http_request_duration_seconds_bucket{endpoint="/api/recall",le="0.01"} 1`,
		`ladym_http_request_duration_seconds_bucket{endpoint="/api/recall",le="5"} 2`, // cumulative
		`ladym_http_request_duration_seconds_bucket{endpoint="/api/recall",le="+Inf"} 2`,
		`ladym_http_request_duration_seconds_count{endpoint="/api/recall"} 2`,
		"ladym_http_request_duration_seconds_sum{endpoint=\"/api/recall\"} 3.007",
		"# TYPE ladym_http_requests_in_flight gauge",
		"ladym_http_requests_in_flight 1",
		"# TYPE ladym_system2_cycles_total counter",
		`ladym_system2_cycles_total{result="ran"} 1`,
		`ladym_system2_cycles_total{result="skipped"} 1`,
		"# TYPE ladym_go_goroutines gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n---\n%s", want, out)
		}
	}

	// Buckets must render in ascending le order.
	idx005 := strings.Index(out, `le="0.005"`)
	idxInf := strings.Index(out, `le="+Inf"`)
	if idx005 < 0 || idxInf < idx005 {
		t.Errorf("buckets not ordered ascending:\n%s", out)
	}
}

func TestLabelEscaping(t *testing.T) {
	r := New()
	r.IncRequests("/api/weird\"\\\npath", "2xx")
	var sb strings.Builder
	r.Render(&sb)
	if !strings.Contains(sb.String(), `endpoint="/api/weird\"\\\npath"`) {
		t.Errorf("label value not escaped:\n%s", sb.String())
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := New()
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.IncRequests("/api/recall", "2xx")
			r.ObserveDuration("/api/recall", 0.001)
			r.IncInFlight()
			r.DecInFlight()
			r.IncSystem2Cycle("ran")
			var sb strings.Builder
			r.Render(&sb)
		}()
	}
	wg.Wait()

	stats := r.EndpointStatsSnapshot()
	if got := stats["/api/recall"].Requests; got != n {
		t.Errorf("requests = %d, want %d", got, n)
	}
	if _, count := r.DurationSumCount("/api/recall"); count != n {
		t.Errorf("duration count = %d, want %d", count, n)
	}
	if got := r.System2Cycles()["ran"]; got != n {
		t.Errorf("cycles ran = %d, want %d", got, n)
	}
	var sb strings.Builder
	r.Render(&sb)
	if !strings.Contains(sb.String(), "ladym_http_requests_in_flight 0") {
		t.Errorf("in-flight should be back to 0:\n%s", sb.String())
	}
}
