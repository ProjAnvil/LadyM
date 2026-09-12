//go:build !enterprise

// NewMux exposes the bare data-plane mux (no console mount at "/") plus the
// standard middleware wrapper, for callers that mount their own top-level
// handlers (the enterprise ladymconsole binary).

package api_test

import (
	"net/http"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
)

// TestNewMuxBareMux: the bare mux serves /healthz and the /api/* routes with
// NOTHING at "/" — the console mount is the caller's job. Serving an
// instrumented route without the observability wrapper also exercises the
// recStatus fallback for a plain (non-recorder) ResponseWriter.
func TestNewMuxBareMux(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)
	mux, _ := api.NewMux(eng, cfg)

	if rec := doReq(t, mux, http.MethodGet, "/healthz", "", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz on bare mux = %d, want 200", rec.Code)
	}
	if rec := doReq(t, mux, http.MethodGet, "/", "", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET / on bare mux = %d, want 404 (nothing mounted)", rec.Code)
	}
	// Instrumented /api/* route reached without the wrapper: the recorder is
	// not a statusRecorder, so recStatus takes its StatusOK fallback.
	if rec := do(t, mux, "/api/stats", "", "", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /api/stats on bare mux = %d (%s), want 200", rec.Code, rec.Body.String())
	}
}

// TestNewMuxWrapperAppliesAuth: the returned wrapper adds the standard
// middleware chain — with auth enabled, /api/* rejects missing credentials
// and accepts a valid admin.
func TestNewMuxWrapperAppliesAuth(t *testing.T) {
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	mux, wrap := api.NewMux(eng, cfg)
	h := wrap(mux)

	if rec := do(t, h, "/api/stats", "", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrapped mux without credentials = %d, want 401", rec.Code)
	}
	if rec := do(t, h, "/api/stats", "root", "s3cret-admin", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("wrapped mux with admin credentials = %d (%s), want 200", rec.Code, rec.Body.String())
	}
}
