//go:build !enterprise

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
)

// TestCJKDictStatusEndpoint: any authenticated caller can read the dict
// status and the downloadable variant list.
func TestCJKDictStatusEndpoint(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := doReq(t, h, http.MethodGet, "/api/cjk_dict", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/cjk_dict = %d (%s)", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	status, ok := body["status"].(map[string]any)
	if !ok {
		t.Fatalf("body missing status object: %v", body)
	}
	for _, key := range []string{"available", "source", "variant", "dir", "version"} {
		if _, ok := status[key]; !ok {
			t.Errorf("status missing %q: %v", key, status)
		}
	}
	variants, ok := body["variants"].([]any)
	if !ok || len(variants) < 4 {
		t.Fatalf("variants = %v, want the zh/zh_s/zh_t/jp enumeration", body["variants"])
	}
}

// TestCJKDictDownloadUnknownVariant: an unknown dict name is a 400 listing
// where to find valid names, not a download attempt.
func TestCJKDictDownloadUnknownVariant(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/cjk_dict/download", "", "", `{"dict": "klingon"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("download klingon = %d (%s), want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown dict") {
		t.Errorf("error body = %s", rec.Body.String())
	}
}

// isolateDictDir points storage's dict dir at a temp dir for one test and
// restores it after — WITHOUT this, dict-mutating endpoint tests would
// delete the developer's real ~/.ladyM/dict.
func isolateDictDir(t *testing.T) {
	t.Helper()
	prev := storage.CJKDictStatusNow().Dir
	storage.SetCJKDictDir(t.TempDir())
	t.Cleanup(func() { storage.SetCJKDictDir(prev) })
}

// TestCJKDictDownloadBadMirror: a mirror_base that serves the wrong bytes
// fails closed with 502 — hermetic coverage of the endpoint wiring; the
// happy path (sha256 match, live reload) is covered in storage tests.
func TestCJKDictDownloadBadMirror(t *testing.T) {
	isolateDictDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered content"))
	}))
	defer srv.Close()

	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/cjk_dict/download", "", "",
		`{"mirror_base": "`+srv.URL+`/"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("POST /api/cjk_dict/download = %d (%s), want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sha256 mismatch") {
		t.Errorf("error body should mention sha256 mismatch: %s", rec.Body.String())
	}
}

func TestCJKDictRemoveEndpoint(t *testing.T) {
	isolateDictDir(t)
	h := newTestHandler(t, nil)
	rec := doReq(t, h, http.MethodDelete, "/api/cjk_dict", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/cjk_dict = %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCJKDictAdminGating: with auth on, download/remove require an admin;
// status stays open to regular users.
func TestCJKDictAdminGating(t *testing.T) {
	isolateDictDir(t) // the admin-path download attempt must not touch the real dict dir
	h := newBasicAuthHandler(t)

	if rec := doReq(t, h, http.MethodGet, "/api/cjk_dict", "alice", "pw-alice", ""); rec.Code != http.StatusOK {
		t.Fatalf("non-admin GET status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "/api/cjk_dict/download", "alice", "pw-alice", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin download = %d, want 403", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/cjk_dict", "alice", "pw-alice", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin remove = %d, want 403", rec.Code)
	}
	// Admin gets past the gate (the download itself fails against the
	// default mirrors only when they are unreachable — assert non-403).
	rec := do(t, h, "/api/cjk_dict/download", "root", "s3cret-admin",
		`{"mirror_base": "http://127.0.0.1:1/"}`)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin download = 403, want past the admin gate")
	}
}
