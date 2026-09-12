//go:build !enterprise

// CJK dictionary download with a valid variant name: variant validation
// passes and the downloader fails closed (502) when the mirror serves the
// wrong bytes. The happy path (sha256 match, live reload) is covered in the
// storage package's internal tests, which can pin the dict registry.

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCJKDictDownloadValidVariantBadMirror: a known variant name passes
// validation and reaches the downloader, which rejects the tampered mirror
// content with a 502 (sha256 mismatch).
func TestCJKDictDownloadValidVariantBadMirror(t *testing.T) {
	isolateDictDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered content"))
	}))
	defer srv.Close()

	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/cjk_dict/download", "", "",
		`{"dict": "zh_s", "mirror_base": "`+srv.URL+`/"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("download zh_s from bad mirror = %d (%s), want 502", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sha256 mismatch") {
		t.Errorf("error body should mention sha256 mismatch: %s", rec.Body.String())
	}
}
