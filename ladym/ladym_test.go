package ladym

import (
	"path/filepath"
	"testing"
)

func TestNamedOps(t *testing.T) {
	ops := NamedOps()
	if len(ops) != 8 {
		t.Fatalf("NamedOps len = %d, want 8", len(ops))
	}
	if ops[0] != "consolidate" {
		t.Errorf("NamedOps[0] = %q", ops[0])
	}
	// Mutating the returned slice must not affect the registry.
	ops[0] = "mutated"
	if NamedOps()[0] != "consolidate" {
		t.Error("NamedOps returned slice aliases the registry")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.DBPath == "" {
		t.Error("DefaultConfig left DBPath empty")
	}
}

// TestSDKRecordAndRecallOperations round-trips the one-shot operation
// helpers: RecordOperation persists through a throwaway engine, and a second
// throwaway engine's RecallOperations retrieves it by the pivot key.
func TestSDKRecordAndRecallOperations(t *testing.T) {
	db := filepath.Join(t.TempDir(), "sdk.ladym.db")

	ep, err := RecordOperation(OperationEvent{
		Agent: "sdk", Action: "migrate", Resources: []string{"ladym.db"},
		Observation: "v1 -> v2", Outcome: "success",
	}, db, "sdkws")
	if err != nil {
		t.Fatalf("RecordOperation: %v", err)
	}
	if ep.ID == "" {
		t.Fatal("RecordOperation returned empty id")
	}

	resp, err := RecallOperations(OperationsQuery{
		Resource: "ladym.db", Action: "migrate",
	}, db, "sdkws")
	if err != nil {
		t.Fatalf("RecallOperations: %v", err)
	}
	if resp.Fallback {
		t.Error("Fallback = true, want pivot hit")
	}
	if len(resp.Items) != 1 || resp.Items[0].Memory.ID != ep.ID {
		t.Errorf("items = %+v, want the recorded episode %s", resp.Items, ep.ID)
	}
}
