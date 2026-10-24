//go:build !enterprise

package client

// recall_ops SDK tests (plan 2026-10-24-recall-ops-http): round-trip against
// a real api.NewHandler (newTestServer), wire-shape pinning and error/gated
// mapping against fixture servers, mirroring client_test.go's style.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperationsRoundTrip(t *testing.T) {
	srv := newTestServer(t, false)
	c := New(srv.URL)
	ctx := t.Context()

	rec, err := c.RecordOperation(ctx, "", RecordOperationInput{
		Action: "updated", Resources: []string{"src/roundtrip.go"},
		Observation: "sdk roundtrip fixture", Outcome: "success", Agent: "tester",
	})
	if err != nil {
		t.Fatalf("RecordOperation: %v", err)
	}
	if rec.ID == "" || rec.Dropped {
		t.Fatalf("RecordOperation result = %+v, want persisted id, dropped=false", rec)
	}

	res, err := c.RecallOperations(ctx, OperationsQuery{Resource: "src/roundtrip.go"})
	if err != nil {
		t.Fatalf("RecallOperations: %v", err)
	}
	if res.Fallback || res.Resource == "" {
		t.Fatalf("RecallOperations = %+v, want resolved pivot", res)
	}
	found := false
	for _, it := range res.Items {
		if it.Memory != nil && it.Memory.ID == rec.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("RecallOperations did not return recorded id %s: %+v", rec.ID, res)
	}

	miss, err := c.RecallOperations(ctx, OperationsQuery{Resource: "src/never-recorded-xyz.go"})
	if err != nil {
		t.Fatalf("RecallOperations miss: %v", err)
	}
	if !miss.Fallback || miss.Resource != "" {
		t.Errorf("unknown-resource recall = %+v, want fallback with empty resource", miss)
	}
}

// The request bodies carry exactly the wire keys the api handlers decode.
func TestOperationsWireShape(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/recall_ops":
			fmt.Fprint(w, `{"resource":"res-1","fallback":false,"items":[{"score":0.5,"tier":1,"via":["res-1"],"memory":{"id":"ep-1","layer":"L1_episodic","type":"episode","summary":"s","content":"c","source":"api","tags":["t"]}}]}`)
		case "/api/record_operation":
			fmt.Fprint(w, `{"id":"ep-2","dropped":false}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)

	if _, err := c.RecallOperations(t.Context(), OperationsQuery{
		Workspace: "acme", Resource: "src/a.go", Action: "update",
		Since: 1.5, Until: 2.5, Outcome: "success", Limit: 7,
	}); err != nil {
		t.Fatalf("RecallOperations: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("recall_ops body not JSON: %q (%v)", gotBody, err)
	}
	if len(m) != 7 {
		t.Errorf("recall_ops body keys = %v, want exactly workspace/resource/action/since/until/outcome/limit", m)
	}
	for k, want := range map[string]any{
		"workspace": "acme", "resource": "src/a.go", "action": "update",
		"since": 1.5, "until": 2.5, "outcome": "success", "limit": float64(7),
	} {
		if m[k] != want {
			t.Errorf("recall_ops body[%q] = %v, want %v", k, m[k], want)
		}
	}

	res, err := c.RecallOperations(t.Context(), OperationsQuery{Resource: "src/a.go"})
	if err != nil {
		t.Fatalf("RecallOperations decode: %v", err)
	}
	if res.Resource != "res-1" || res.Fallback || len(res.Items) != 1 ||
		res.Items[0].Memory == nil || res.Items[0].Memory.ID != "ep-1" {
		t.Errorf("RecallOperations decode = %+v, want res-1 with one ep-1 item", res)
	}

	rec, err := c.RecordOperation(t.Context(), "acme", RecordOperationInput{
		Action: "updated", Resources: []string{"src/a.go"},
		Observation: "obs", Outcome: "success", Agent: "woca", Tags: []string{"t"},
	})
	if err != nil {
		t.Fatalf("RecordOperation: %v", err)
	}
	if rec.ID != "ep-2" || rec.Dropped {
		t.Errorf("RecordOperation decode = %+v, want ep-2 not dropped", rec)
	}
	clear(m)
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("record_operation body not JSON: %q (%v)", gotBody, err)
	}
	if len(m) != 7 {
		t.Errorf("record_operation body keys = %v, want exactly workspace/action/resources/observation/outcome/agent/tags", m)
	}
	for k, want := range map[string]any{
		"workspace": "acme", "action": "updated", "observation": "obs",
		"outcome": "success", "agent": "woca",
	} {
		if m[k] != want {
			t.Errorf("record_operation body[%q] = %v, want %v", k, m[k], want)
		}
	}
	if resources, _ := m["resources"].([]any); len(resources) != 1 || resources[0] != "src/a.go" {
		t.Errorf("record_operation resources = %v, want [src/a.go]", m["resources"])
	}
	if tags, _ := m["tags"].([]any); len(tags) != 1 || tags[0] != "t" {
		t.Errorf("record_operation tags = %v, want [t]", m["tags"])
	}
}

func TestRecordOperationGated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":null,"dropped":true,"reason":"noise"}`)
	}))
	t.Cleanup(srv.Close)

	rec, err := New(srv.URL).RecordOperation(t.Context(), "", RecordOperationInput{
		Action: "updated", Resources: []string{"src/a.go"},
	})
	if err != nil {
		t.Fatalf("RecordOperation gated: %v", err)
	}
	if !rec.Dropped || rec.ID != "" || rec.Reason != "noise" {
		t.Errorf("gated result = %+v, want dropped reason=noise empty id", rec)
	}
}

func TestOperationsErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"boom"}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL)

	_, err := c.RecallOperations(t.Context(), OperationsQuery{Resource: "src/a.go"})
	cerr := asError(t, err)
	if cerr.StatusCode != 400 || cerr.Message != "boom" {
		t.Errorf("RecallOperations error = %v, want 400 boom", cerr)
	}

	_, err = c.RecordOperation(t.Context(), "", RecordOperationInput{Resources: []string{"src/a.go"}})
	cerr = asError(t, err)
	if cerr.StatusCode != 400 || cerr.Message != "boom" {
		t.Errorf("RecordOperation error = %v, want 400 boom", cerr)
	}
}
