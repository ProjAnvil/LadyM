//go:build !enterprise

// Body-decoding validation across the data-plane endpoints: malformed JSON
// is a 400 before any engine call, missing required fields are a 400, and an
// empty body decodes as "no args" (EOF is not an error) for the endpoints
// that take none.

package api_test

import (
	"net/http"
	"testing"
)

func TestEndpointMalformedJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	cases := []struct {
		name string
		path string
		body string
	}{
		{"remember", "/api/remember", `{"content":`},
		{"record_event", "/api/record_event", `{"agent":`},
		{"search_code", "/api/search_code", `{"query":`},
		{"index_code", "/api/index_code", `{"root":`},
		{"link", "/api/link", `{"src":`},
		{"forget", "/api/forget", `{"memory_id":`},
		{"cjk_dict_download", "/api/cjk_dict/download", `{"dict":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.path, "", "", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST %s with malformed JSON = %d (%s), want 400", tc.path, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRecordEventMissingAgentAction: agent and action are required; the
// handler rejects before touching the engine.
func TestRecordEventMissingAgentAction(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/record_event", "", "", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("record_event without agent/action = %d, want 400", rec.Code)
	}
}

// TestStatsEmptyBody: an empty request body decodes as "no args" — stats
// needs none and answers 200.
func TestStatsEmptyBody(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/stats", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("stats with empty body = %d (%s), want 200", rec.Code, rec.Body.String())
	}
}
