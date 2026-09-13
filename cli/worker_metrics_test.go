//go:build !enterprise

// Tests for the worker's Prometheus/health endpoint: /metrics exposition,
// /healthz reflecting store liveness (including the store-down 503), and
// startWorkerMetrics address handling (empty no-op, invalid address, real
// listener + shutdown closer).

package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// /healthz reports ok while the store answers Ping; /metrics serves the
// Prometheus exposition.
func TestWorkerMetricsHandler_HealthyStore(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	h := workerMetricsHandler(eng)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("/healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics = %d, want 200", rec.Code)
	}
}

// /healthz turns 503 once the store underneath is gone (closed engine), so
// orchestrators stop routing to a dead worker.
func TestWorkerMetricsHandler_StoreDown_HealthzUnavailable(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	h := workerMetricsHandler(eng)
	eng.Close()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/healthz with closed store = %d, want 503", rec.Code)
	}
}

// startWorkerMetrics: an empty address is a no-op closer, an invalid address
// fails to listen, and a real address serves until the closer shuts it down.
func TestStartWorkerMetrics_AddrHandling(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	closer, err := startWorkerMetrics(eng, "")
	if err != nil {
		t.Fatalf("empty addr should be a no-op: %v", err)
	}
	closer()

	if _, err := startWorkerMetrics(eng, "no-port-here"); err == nil {
		t.Error("invalid addr should fail to listen")
	}

	closer, err = startWorkerMetrics(eng, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start on ephemeral port: %v", err)
	}
	// Give the Serve goroutine a chance to accept before shutdown.
	time.Sleep(20 * time.Millisecond)
	closer()
}
