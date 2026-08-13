package ladym

import (
	"path/filepath"
	"testing"
)

func TestOneShotRememberRecall(t *testing.T) {
	db := filepath.Join(t.TempDir(), "mem.db")
	m, err := Remember("auth uses JWT with 24h expiry", db, "ws", []string{"auth"}, "sdk")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" {
		t.Fatal("remember returned empty id")
	}
	resp, err := Recall("how does authentication work", db, "ws", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected recall results")
	}
}

func TestNamedOps(t *testing.T) {
	ops := NamedOps()
	if len(ops) != 5 {
		t.Fatalf("NamedOps len = %d, want 5", len(ops))
	}
	if ops[0] != "consolidate" {
		t.Errorf("NamedOps[0] = %q", ops[0])
	}
}
