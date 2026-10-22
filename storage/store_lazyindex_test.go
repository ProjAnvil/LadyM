//go:build !enterprise

package storage

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// reopenStore writes n memories carrying distinct unit vectors into a fresh
// database, closes it, and reopens it so the test starts from a cold store.
func reopenStore(t *testing.T, n int) (*SQLiteStore, []string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cold.db")

	seed, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		vec := make([]float32, 8)
		vec[i%8] = 1
		if err := seed.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, ids
}

// TestNewStoreDoesNotWarmIndex pins the lazy-index contract: opening a store
// must not scan every embedding into the process-local index. The hook pays
// NewStore once per tool call, so an O(n) open is an O(n) tax per event.
func TestNewStoreDoesNotWarmIndex(t *testing.T) {
	s, _ := reopenStore(t, 3)
	if got := s.vectorIndex.Len(); got != 0 {
		t.Errorf("index len right after open = %d, want 0 (warm must be deferred)", got)
	}
}

// TestVectorSearchWarmsIndex pins the other half: the first VectorSearch must
// warm from the database, and warming must happen exactly once. A second
// search after the database moved on must NOT re-read, which is what keeps the
// deferral from turning into a per-query scan.
func TestVectorSearchWarmsIndex(t *testing.T) {
	s, _ := reopenStore(t, 3)

	hits := s.VectorSearch([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 8)
	if len(hits) != 3 {
		t.Fatalf("first VectorSearch hits = %d, want 3 (index must warm on read)", len(hits))
	}
	if got := s.vectorIndex.Len(); got != 3 {
		t.Errorf("index len after warm = %d, want 3", got)
	}

	// Slip a row into the database behind the store's back. A warm that ran
	// on every search would pick it up; warm-once must not.
	late := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, created_at, updated_at, last_access_at, embedding)
		 VALUES (?, 'L1_episodic', 'event', 'late', 0, 0, 0, ?)`,
		late.ID, encodeVec([]float32{1, 0, 0, 0, 0, 0, 0, 0})); err != nil {
		t.Fatalf("insert row behind the index: %v", err)
	}
	hits = s.VectorSearch([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 8)
	for _, h := range hits {
		if h.ID == late.ID {
			t.Errorf("second VectorSearch returned %s: warm ran more than once", late.ID)
		}
	}
	if len(hits) != 3 {
		t.Errorf("second VectorSearch hits = %d, want 3 (warm-once keeps the first snapshot)", len(hits))
	}
}

// TestLazyIndexKeepsDimValidation guards against the obvious way to implement
// the deferral: dropping index writes while cold. Skipping Upsert would also
// skip the dim check, silently accepting malformed vectors.
func TestLazyIndexKeepsDimValidation(t *testing.T) {
	s, _ := reopenStore(t, 1)
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if err := s.PutMemory(m, []float32{1, 2}); err == nil {
		t.Error("PutMemory with a wrong-dim vector on a cold index: want error, got nil")
	}
}

// TestRebuildVectorIndexRecolds pins that a dim change resets the lazy flag:
// the new index must be reloaded from the database, not left stale.
func TestRebuildVectorIndexRecolds(t *testing.T) {
	s, _ := reopenStore(t, 2)
	if hits := s.VectorSearch([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 8); len(hits) != 2 {
		t.Fatalf("pre-rebuild hits = %d, want 2", len(hits))
	}
	s.RebuildVectorIndex(4)
	if got := s.vectorIndex.Len(); got != 0 {
		t.Errorf("index len after rebuild = %d, want 0", got)
	}
	// Write a 4-dim vector, then confirm a search warms into the new dim.
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if err := s.PutMemory(m, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("PutMemory at new dim: %v", err)
	}
	hits := s.VectorSearch([]float32{1, 0, 0, 0}, 8)
	if len(hits) == 0 || hits[0].ID != m.ID {
		t.Errorf("post-rebuild search = %v, want top hit %s", hits, m.ID)
	}
}
