package storage

// SQLite-specific store tests. Backend-agnostic behaviour cases live in the
// parametrised suite (store_suite_test.go); this file keeps what only applies
// to SQLiteStore: the in-memory vector index sync, PRAGMA-adjacent internals,
// BLOB warm-up and index rebuild behaviour.

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func putTestMemory(t *testing.T, s *SQLiteStore, mem *schema.Memory, vector []float32) {
	t.Helper()
	if err := s.PutMemory(mem, vector); err != nil {
		t.Fatal(err)
	}
}

// TestSQLiteVectorIndexSync pins the process-local vector index bookkeeping
// that has no PostgresStore counterpart (there the HNSW index follows the
// table rows).
func TestSQLiteVectorIndexSync(t *testing.T) {
	s := openTestStore(t)
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m.Content = "event"
	putTestMemory(t, s, m, []float32{1, 0, 0, 0, 0, 0, 0, 0})
	if s.vectorIndex.Len() != 1 {
		t.Errorf("index len = %d, want 1", s.vectorIndex.Len())
	}

	// A vector-less upsert must not insert a new index entry.
	m.Content = "updated"
	putTestMemory(t, s, m, nil)
	if s.vectorIndex.Len() != 1 {
		t.Errorf("index len after vector-less upsert = %d, want 1", s.vectorIndex.Len())
	}

	// Delete removes the index entry too; deleting a missing id is a no-op.
	if err := s.DeleteMemory(m.ID); err != nil {
		t.Fatal(err)
	}
	if s.vectorIndex.Len() != 0 {
		t.Errorf("index len after delete = %d, want 0", s.vectorIndex.Len())
	}

	// Dim mismatch propagates from the index.
	if err := s.PutMemory(m, []float32{1, 2}); err == nil {
		t.Error("expected dim-mismatch error from PutMemory")
	}
}

func TestStoreMisc(t *testing.T) {
	s := openTestStore(t)
	if s.UsingSQLiteVec() {
		t.Error("UsingSQLiteVec should always be false")
	}
	if s.db == nil {
		t.Error("db is nil")
	}

	// RebuildVectorIndex resets the index at a new dim.
	putTestMemory(t, s, schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent),
		[]float32{1, 0, 0, 0, 0, 0, 0, 0})
	s.RebuildVectorIndex(4)
	if s.Dim != 4 {
		t.Errorf("Dim after rebuild = %d, want 4", s.Dim)
	}
	if s.vectorIndex.Len() != 0 {
		t.Errorf("index len after rebuild = %d, want 0", s.vectorIndex.Len())
	}
}

func TestWarmIndexFromBlobs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "warm.db")

	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	good := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	putTestMemory(t, s, good, []float32{1, 0, 0, 0, 0, 0, 0, 0})
	noVec := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	putTestMemory(t, s, noVec, nil)
	// A blob of the wrong length must be skipped on warm.
	bad := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if err := s.PutMemory(bad, []float32{1, 2}); err == nil {
		t.Fatal("expected dim-mismatch error")
	}
	// PutMemory failed before index upsert? It actually fails at index upsert,
	// after the SQL write — so the row exists with a 2-dim blob.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// Only the good 8-dim vector is warmed; the 2-dim blob is skipped.
	if s2.vectorIndex.Len() != 1 {
		t.Errorf("warmed index len = %d, want 1", s2.vectorIndex.Len())
	}
	hits := s2.vectorIndex.Search([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 1)
	if len(hits) != 1 || hits[0].ID != good.ID {
		t.Errorf("warm search hits = %v, want [%s]", hits, good.ID)
	}
}
