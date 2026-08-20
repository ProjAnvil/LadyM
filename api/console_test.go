//go:build !enterprise

package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embedded console is served at "/" with SPA fallback, and — unlike
// /api/* — never behind auth (the login page must load unauthenticated).

func TestConsoleStaticAndSPAFallback(t *testing.T) {
	h := newTestHandler(t, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// GET / serves index.html.
	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("GET / content-type = %q, want text/html", ct)
	}
	if !strings.Contains(string(body), `<div id="app">`) {
		t.Error("GET / body does not look like the console index.html")
	}

	// An unknown client-side route falls back to index.html (SPA).
	res, err = http.Get(srv.URL + "/users")
	if err != nil {
		t.Fatalf("GET /users: %v", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `<div id="app">`) {
		t.Errorf("GET /users: status=%d, want 200 + index.html", res.StatusCode)
	}

	// The hashed bundle under /assets/ is served from the embed FS.
	res, err = http.Get(srv.URL + "/assets/")
	if err != nil {
		t.Fatalf("GET /assets/: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusMovedPermanently {
		t.Errorf("GET /assets/ status = %d, want 200/301", res.StatusCode)
	}

	// Unknown /api paths get a JSON 404, not the SPA fallback.
	res, err = http.Get(srv.URL + "/api/nope")
	if err != nil {
		t.Fatalf("GET /api/nope: %v", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/nope status = %d, want 404", res.StatusCode)
	}
	if !strings.Contains(string(body), `"error"`) {
		t.Errorf("GET /api/nope body = %q, want JSON error", body)
	}
}

func TestConsoleNotAuthGated(t *testing.T) {
	// Auth on with users seeded: /api/* 401s without credentials, but the
	// console shell stays reachable.
	h := newBasicAuthHandler(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("GET / with auth on: status = %d, want 200", res.StatusCode)
	}

	res, err = http.Post(srv.URL+"/api/stats", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/stats: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /api/stats without credentials: status = %d, want 401", res.StatusCode)
	}
}
