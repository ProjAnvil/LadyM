package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaimClient(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/claims/bad" {
			w.WriteHeader(404)
			w.Write([]byte(`{"error":"memory not found"}`))
			return
		}
		if r.URL.Path == "/api/claims/x/feedback" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["expected_revision"] != float64(3) || body["workspace"] != "w" || body["actor"] != "host" {
				t.Errorf("feedback wire=%v", body)
			}
		}
		w.Write([]byte(`{"memory":{"id":"x","content":"safe"},"status":"verified","revision":4,"scope":{"environment":"test","code_version":"v1","conditions":["ready"]},"evidence_refs":[{"memory_id":"e","kind":"test_result","fingerprint":"fp","start_rune":0,"end_rune":0}]}`))
	}))
	defer h.Close()
	c := New(h.URL, WithActor("host"))
	got, err := c.GetClaim(t.Context(), "x", "w")
	if err != nil || got.Revision != 4 || got.Scope.Environment != "test" || len(got.Evidence) != 1 {
		t.Fatalf("decode=%+v %v", got, err)
	}
	if _, err := c.ApplyClaimFeedback(t.Context(), "x", ClaimFeedbackRequest{Workspace: "w", ClaimFeedback: ClaimFeedback{ExpectedRevision: 3, Action: "verify"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetClaim(t.Context(), "bad", "w"); err == nil {
		t.Fatal("404 swallowed")
	}
}
