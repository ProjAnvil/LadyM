package console

// Failure-path tests for the embedded console mount. These run in package
// console (not console_test) because they construct a spaHandler with a
// controlled FS to reach the error branch that the committed console/dist can
// never produce: the 500 when index.html is missing from the served FS.
//
// Note: Dist's panic branch (fs.Sub failing on "dist") is untestable — fs.Sub
// only errors on an invalid path, never on a missing directory, so the check
// is dead code kept as a defensive assertion.

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// If the served FS has no index.html, "/" and client-side routes must get a
// 500 ("console assets not embedded") instead of an empty 200 or the SPA
// fallback silently serving nothing.
func TestSPAHandler_ServeHTTP_MissingIndexHTML_Returns500(t *testing.T) {
	for _, path := range []string{"/", "/memories/client-side-route"} {
		h := &spaHandler{fsys: fstest.MapFS{}}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s without index.html: %d, want 500", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "console assets not embedded") {
			t.Errorf("GET %s body = %q, want the not-embedded notice", path, rec.Body.String())
		}
	}
}

// An FS whose index.html exists but is unreadable as a file (here: it is a
// directory) also fails fs.ReadFile and must surface the same 500.
func TestSPAHandler_ServeHTTP_IndexHTMLIsDirectory_Returns500(t *testing.T) {
	h := &spaHandler{fsys: fstest.MapFS{
		"index.html": &fstest.MapFile{Mode: fs.ModeDir},
	}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("GET / with directory index.html: %d, want 500", rec.Code)
	}
}
