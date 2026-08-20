package console_test

// Tests for the embedded console mount: Dist exposes the built assets, the
// SPA handler serves static files, falls back to index.html for client-side
// routes (any method), and answers unknown /api paths with a JSON 404.

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/console"
)

// mountedConsole returns a mux with the console mounted at "/".
func mountedConsole(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	console.Mount(mux)
	return mux
}

// indexHTML reads the embedded index.html for body comparisons.
func indexHTML(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(console.Dist(), "index.html")
	if err != nil {
		t.Fatalf("embedded index.html unreadable: %v", err)
	}
	return string(b)
}

func TestDistEmbedsConsoleAssets(t *testing.T) {
	idx := indexHTML(t)
	if !strings.Contains(idx, "<html") {
		t.Errorf("index.html does not look like HTML: %.80q", idx)
	}
	entries, err := fs.ReadDir(console.Dist(), "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("embedded assets/ missing or empty: %v (%d entries)", err, len(entries))
	}
}

func TestMountServesIndexAtRoot(t *testing.T) {
	mux := mountedConsole(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}
	if rec.Body.String() != indexHTML(t) {
		t.Errorf("GET / body is not the embedded index.html")
	}
}

// Client-side routes (unknown non-/api paths) get the SPA fallback so the
// Vue router can resolve them; this holds for any HTTP method because the
// mount is registered without a method constraint.
func TestMountSPAFallback(t *testing.T) {
	mux := mountedConsole(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/memories/some-client-route", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /memories/some-client-route: %d, want 200 (SPA fallback)", method, rec.Code)
		}
		if rec.Body.String() != indexHTML(t) {
			t.Errorf("%s SPA fallback body is not index.html", method)
		}
	}
}

// Hashed bundles under assets/ must be served as files, not fall back to
// index.html. The exact hash changes per build, so the asset name is
// discovered from the embed FS.
func TestMountServesStaticAsset(t *testing.T) {
	entries, err := fs.ReadDir(console.Dist(), "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no embedded assets to test with: %v", err)
	}
	name := entries[0].Name()
	want, err := fs.ReadFile(console.Dist(), "assets/"+name)
	if err != nil {
		t.Fatal(err)
	}

	mux := mountedConsole(t)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/%s: %d", name, rec.Code)
	}
	got, _ := io.ReadAll(rec.Result().Body)
	if !bytes.Equal(got, want) {
		t.Errorf("GET /assets/%s served %d bytes that differ from the embedded file (%d bytes)", name, len(got), len(want))
	}
}

// Unknown /api paths must NOT fall back to the SPA: they get the JSON 404
// mirroring the api package's error shape (the api mux answers known ones
// first; only unknowns reach this handler).
func TestMountUnknownAPIPathIsJSON404(t *testing.T) {
	mux := mountedConsole(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/api/definitely-not-an-endpoint", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s /api/definitely-not-an-endpoint: %d, want 404", method, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("404 Content-Type = %q, want application/json", ct)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("404 body is not JSON: %q", rec.Body.String())
		}
		if body["error"] != "not found: /api/definitely-not-an-endpoint" {
			t.Errorf("404 error = %q, want the not-found path echoed", body["error"])
		}
	}
}
