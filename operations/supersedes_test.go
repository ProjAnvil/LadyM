package operations

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func newTestStore(t *testing.T) storage.Store {
	t.Helper()
	s, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 16, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRetireAndLatestInChain(t *testing.T) {
	store := newTestStore(t)
	old := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	old.Content = "v1"
	newm := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	newm.Content = "v2"
	if err := store.PutMemory(old, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(newm, nil); err != nil {
		t.Fatal(err)
	}

	if err := Retire(store, old, newm.ID); err != nil {
		t.Fatal(err)
	}
	if !IsRetired(old) {
		t.Error("old should be retired after Retire with new_id")
	}
	head, err := LatestInChain(store, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if head != newm.ID {
		t.Errorf("LatestInChain = %s, want %s", head, newm.ID)
	}
}

func TestRetireDeleteStyle(t *testing.T) {
	store := newTestStore(t)
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.Content = "stale"
	if err := store.PutMemory(m, nil); err != nil {
		t.Fatal(err)
	}
	if err := Retire(store, m, ""); err != nil {
		t.Fatal(err)
	}
	if !IsRetired(m) {
		t.Error("memory should be retired (superseded=true)")
	}
	if m.MetaString("superseded_by") != "" {
		t.Error("DELETE-style retire should not set superseded_by")
	}
}
