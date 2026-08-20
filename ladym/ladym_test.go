package ladym

import (
	"testing"
)

func TestNamedOps(t *testing.T) {
	ops := NamedOps()
	if len(ops) != 5 {
		t.Fatalf("NamedOps len = %d, want 5", len(ops))
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
