//go:build !enterprise

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteJSONMarshalError: a value encoding/json cannot marshal (headers
// are already written by then) falls back to an inline error document
// instead of crashing the handler.
func TestWriteJSONMarshalError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]any{"bad": func() {}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (status is written before marshal)", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"error"`) {
		t.Errorf("fallback body = %q, want an inline error document", body)
	}
}
