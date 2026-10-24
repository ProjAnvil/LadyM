//go:build !enterprise

package api_test

// recall_ops on the HTTP data plane (plan 2026-10-24-recall-ops-http):
// POST /api/recall_ops mirrors the MCP recall_ops output shape (resource as
// the resolved entity id, null on fallback); POST /api/record_operation
// exposes the structured L1 write (no MCP counterpart — engine/CLI only).

import (
	"net/http"
	"strings"
	"testing"
)

func TestRecallOpsRoundTrip(t *testing.T) {
	h := newTestHandler(t, nil)

	rec := do(t, h, "/api/record_operation", "", "",
		`{"action": "updated", "resources": ["src/a.go"], "observation": "rewrote the handler", "outcome": "success", "agent": "tester"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("record_operation: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["dropped"] != false {
		t.Errorf("record_operation dropped = %v, want false: %v", m["dropped"], m)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("record_operation response missing id: %v", m)
	}

	rec = do(t, h, "/api/recall_ops", "", "", `{"resource": "src/a.go"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall_ops: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	if m["fallback"] != false {
		t.Errorf("recall_ops fallback = %v, want resolved pivot: %v", m["fallback"], m)
	}
	if res, _ := m["resource"].(string); res == "" {
		t.Errorf("recall_ops resource = %v, want resolved entity id", m["resource"])
	}
	items, _ := m["items"].([]any)
	found := false
	for _, it := range items {
		im := it.(map[string]any)["memory"].(map[string]any)
		if im["id"] == id {
			found = true
		}
	}
	if !found {
		t.Errorf("recall_ops did not return recorded id %s: %v", id, m)
	}
}

func TestRecallOpsValidation(t *testing.T) {
	h := newTestHandler(t, nil)

	if rec := do(t, h, "/api/recall_ops", "", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("recall_ops missing resource: %d, want 400", rec.Code)
	}
	if rec := do(t, h, "/api/recall_ops", "", "", `{"resource": "x", "limit": -1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("recall_ops negative limit: %d, want 400", rec.Code)
	}
	if rec := do(t, h, "/api/record_operation", "", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("record_operation missing resources: %d, want 400", rec.Code)
	}
}

// limit 0 rides the engine default (10); a request above 100 is clamped —
// observable only with more than 100 linked episodes for one resource.
func TestRecallOpsLimitDefaultAndCap(t *testing.T) {
	h := newTestHandler(t, nil)
	for i := 0; i < 101; i++ {
		rec := do(t, h, "/api/record_operation", "", "",
			`{"action": "read", "resources": ["src/cap.go"], "observation": "capped pivot fixture"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("record_operation %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}

	rec := do(t, h, "/api/recall_ops", "", "", `{"resource": "src/cap.go"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall_ops default limit: %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := decodeBody(t, rec)["items"].([]any); len(items) != 10 {
		t.Errorf("default-limit items = %d, want 10", len(items))
	}

	rec = do(t, h, "/api/recall_ops", "", "", `{"resource": "src/cap.go", "limit": 500}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall_ops cap: %d %s", rec.Code, rec.Body.String())
	}
	if items, _ := decodeBody(t, rec)["items"].([]any); len(items) != 100 {
		t.Errorf("capped items = %d, want 100", len(items))
	}
}

func TestRecallOpsWorkspaceBinding(t *testing.T) {
	h := newTestHandler(t, nil)

	if rec := do(t, h, "/api/record_operation", "", "",
		`{"action": "updated", "resources": ["src/ws.go"], "observation": "workspace binding fixture", "workspace": "ws-a"}`); rec.Code != http.StatusOK {
		t.Fatalf("record ws-a: %d %s", rec.Code, rec.Body.String())
	}

	rec := do(t, h, "/api/recall_ops", "", "", `{"resource": "src/ws.go", "workspace": "ws-b"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall ws-b: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["fallback"] != true || m["resource"] != nil {
		t.Errorf("ws-b recall = %v, want fallback with null resource", m)
	}
	if items, _ := m["items"].([]any); len(items) != 0 {
		t.Errorf("ws-b items = %v, want empty", items)
	}

	rec = do(t, h, "/api/recall_ops", "", "", `{"resource": "src/ws.go", "workspace": "ws-a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall ws-a: %d %s", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["fallback"] != false {
		t.Errorf("ws-a recall fallback = %v, want pivot hit: %v", m["fallback"], m)
	}
}

// A non-admin user's body workspace is ignored on both routes (forced
// workspace wins); an admin picks freely.
func TestRecallOpsForcedWorkspace(t *testing.T) {
	h := newBasicAuthHandler(t)

	rec := do(t, h, "/api/record_operation", "alice", "pw-alice",
		`{"action": "updated", "resources": ["src/forced.go"], "observation": "forced workspace fixture", "workspace": "not-acme"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("alice record: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Ladym-Workspace"); got != "acme" {
		t.Errorf("X-Ladym-Workspace = %q, want acme", got)
	}

	rec = do(t, h, "/api/recall_ops", "root", "s3cret-admin", `{"resource": "src/forced.go", "workspace": "acme"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("root recall acme: %d %s", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["fallback"] != false {
		t.Errorf("root acme recall fallback = %v, want pivot hit: %v", m["fallback"], m)
	}

	rec = do(t, h, "/api/recall_ops", "nopw", "", `{"resource": "src/forced.go", "workspace": "globex"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("nopw recall globex: %d %s", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["fallback"] != true {
		t.Errorf("globex recall fallback = %v, want miss: %v", m["fallback"], m)
	}
}

func TestRecallOpsInstrumented(t *testing.T) {
	h := newTestHandler(t, nil)

	if rec := do(t, h, "/api/recall_ops", "", "", `{"resource": "src/metrics.go"}`); rec.Code != http.StatusOK {
		t.Fatalf("recall_ops: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "/api/recall_ops", "", "", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("recall_ops missing resource: %d, want 400", rec.Code)
	}

	rec := doReq(t, h, http.MethodGet, "/metrics", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`ladym_http_requests_total{endpoint="/api/recall_ops",status="2xx"} 1`,
		`ladym_http_requests_total{endpoint="/api/recall_ops",status="4xx"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
