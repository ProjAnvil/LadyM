//go:build !enterprise

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPromMetricsEndpoint covers the auth-exempt Prometheus scrape face:
// text exposition with pattern-labelled samples for requests issued so far,
// and no self-counting of /metrics or /healthz.
func TestPromMetricsEndpoint(t *testing.T) {
	h := newTestHandler(t, nil)

	if rec := do(t, h, "/api/recall", "", "", `{"query": "zeppelins"}`); rec.Code != 200 {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "/api/recall", "", "", `{}`); rec.Code != 400 {
		t.Fatalf("recall missing query: %d, want 400", rec.Code)
	}
	if rec := do(t, h, "/api/stats", "", "", `{}`); rec.Code != 200 {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doReq(t, h, http.MethodGet, "/healthz", "", "", ""); rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}

	scrape := func() string {
		t.Helper()
		rec := doReq(t, h, http.MethodGet, "/metrics", "", "", "")
		if rec.Code != 200 {
			t.Fatalf("/metrics: %d %s", rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
			t.Fatalf("/metrics Content-Type = %q", ct)
		}
		return rec.Body.String()
	}

	body := scrape()
	for _, want := range []string{
		"# TYPE ladym_http_requests_total counter",
		`ladym_http_requests_total{endpoint="/api/recall",status="2xx"} 1`,
		`ladym_http_requests_total{endpoint="/api/recall",status="4xx"} 1`,
		`ladym_http_requests_total{endpoint="/api/stats",status="2xx"} 1`,
		`ladym_http_request_duration_seconds_count{endpoint="/api/recall"} 2`,
		"ladym_http_requests_in_flight 0",
		"ladym_go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q\n---\n%s", want, body)
		}
	}
	if strings.Contains(body, `endpoint="/metrics"`) || strings.Contains(body, `endpoint="/healthz"`) {
		t.Errorf("/metrics must not count itself or /healthz:\n%s", body)
	}
	// Scraping again must not have added a sample for the scrape path itself.
	if body2 := scrape(); strings.Contains(body2, `endpoint="/metrics"`) {
		t.Errorf("second scrape counted /metrics:\n%s", body2)
	}
}

// TestPromMetricsPatternLabels pins bounded label cardinality: requests to
// id-carrying paths collapse to the route pattern, not the raw path.
func TestPromMetricsPatternLabels(t *testing.T) {
	h := newTestHandler(t, nil)
	id, _ := decodeBody(t, mustRec(t, h, "/api/remember", `{"content":"the frobnicate runbook pins release v42"}`))["id"].(string)
	if id == "" {
		t.Fatal("remember returned no id")
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+id, "", "", ""); rec.Code != 200 {
		t.Fatalf("delete memory: %d %s", rec.Code, rec.Body.String())
	}
	rec := doReq(t, h, http.MethodGet, "/metrics", "", "", "")
	body := rec.Body.String()
	if !strings.Contains(body, `endpoint="/api/memories/{id}"`) {
		t.Errorf("id path not collapsed to route pattern:\n%s", body)
	}
	if strings.Contains(body, `endpoint="/api/memories/`+id+`"`) {
		t.Errorf("raw id path leaked into labels:\n%s", body)
	}
}

func mustRec(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := do(t, h, path, "", "", body)
	if rec.Code != 200 {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
	}
	return rec
}
