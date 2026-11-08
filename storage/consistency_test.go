//go:build !enterprise

package storage

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func consistencyStores(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "consistency.db")
	reader, err := NewStore(path, suiteDim, false, true)
	if err != nil {
		t.Fatal(err)
	}

	writer, err := NewStore(path, suiteDim, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	return reader, writer
}

func putLegacyForeignReference(t *testing.T, s Store, m *schema.Memory, source string) {
	t.Helper()
	m.Metadata = nil
	suitePutMemory(t, s, m, nil)
	db := s.(*SQLiteStore)
	if _, err := db.db.Exec("UPDATE memories SET metadata=json_object('source_episode',?) WHERE id=?", source, m.ID); err != nil {
		t.Fatal(err)
	}
}

func beginSQLiteContractTransaction(t *testing.T, s Store) memoryTransaction {
	t.Helper()
	tx, err := s.(*SQLiteStore).beginMemoryTransaction()
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func installSQLiteContractFault(t *testing.T, s Store) {
	t.Helper()
	if _, err := s.(*SQLiteStore).db.Exec(`
		CREATE TRIGGER fail_contract_edge BEFORE INSERT ON edges
		WHEN NEW.relation='fail_contract' BEGIN SELECT RAISE(ABORT,'injected contract fault'); END;
		CREATE TRIGGER fail_contract_delete BEFORE DELETE ON memories
		BEGIN SELECT RAISE(ABORT,'injected contract fault'); END;`); err != nil {
		t.Fatal(err)
	}
}

func TestSafeDeleteSQLiteLegacyDependencyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-evidence.db")
	seed, err := NewStore(path, suiteDim, false, false)
	if err != nil {
		t.Fatal(err)
	}
	source, dependant := contractMemory(schema.TypeEvent), contractMemory(schema.TypeFact)
	suitePutMemory(t, seed, source, contractVector())
	dependant.Metadata["source_episodes"] = []string{source.ID}
	suitePutMemory(t, seed, dependant, contractVector())
	if _, err := seed.db.Exec("DROP TRIGGER memories_evidence_insert; DROP TRIGGER memories_evidence_update; DROP TABLE memory_evidence_sources"); err != nil {
		t.Fatal(err)
	}
	seed.Close()
	reader, err := NewStore(path, suiteDim, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if reader.vectorIndex.Len() != 0 {
		t.Fatal("dependency backfill warmed vectors")
	}
	if deleted, protected, err := reader.DeleteMemorySafely(source.ID, 2); err != nil || deleted || !protected {
		t.Fatalf("legacy dependency backfill = %v %v %v", deleted, protected, err)
	}
}

func TestSafeDeleteSQLiteLegacyDependencyIndex(t *testing.T) {
	reader, writer := consistencyStores(t)
	first, second := contractMemory(schema.TypeEvent), contractMemory(schema.TypeEvent)
	dependant := contractMemory(schema.TypeFact)
	suitePutMemory(t, writer, first, nil)
	suitePutMemory(t, writer, second, nil)
	dependant.Metadata["source_episode"] = first.ID
	dependant.Metadata["source_episodes"] = []string{first.ID}
	suitePutMemory(t, writer, dependant, nil)
	check := func(source string, want int) {
		t.Helper()
		var count int
		if err := reader.db.QueryRow("SELECT count(*) FROM memory_evidence_sources WHERE memory_id=? AND source_id=?", dependant.ID, source).Scan(&count); err != nil || count != want {
			t.Fatalf("dependency lookup source=%s count=%d err=%v want=%d", source, count, err, want)
		}
	}
	check(first.ID, 1)
	if _, err := writer.db.Exec("UPDATE memories SET metadata=json_object('source_episodes',json_array(?)) WHERE id=?", second.ID, dependant.ID); err != nil {
		t.Fatal(err)
	}
	check(first.ID, 0)
	check(second.ID, 1)
	if deleted, protected, err := reader.DeleteMemorySafely(second.ID, 2); err != nil || deleted || !protected {
		t.Fatalf("direct SQL reference not protected: %v %v %v", deleted, protected, err)
	}
	if err := writer.DeleteMemory(dependant.ID); err != nil {
		t.Fatal(err)
	}
	check(second.ID, 0)
}

func TestSQLiteSearchDelayedLocalMutation(t *testing.T) {
	reader, writer := consistencyStores(t)
	m := contractMemory(schema.TypeFact)
	suitePutMemory(t, reader, m, contractVector())
	if _, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{}); err != nil {
		t.Fatal(err)
	}
	suitePutMemory(t, writer, m, []float32{0, 1, 0, 0, 0, 0, 0, 0})
	if _, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{}); err != nil {
		t.Fatal(err)
	}
	// A delayed post-commit cache update must not poison a newer refresh.
	if err := reader.updateLocalVector(m.ID, contractVector()); err != nil {
		t.Fatal(err)
	}
	hits, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{})
	if err != nil || len(hits) != 1 || hits[0].Similarity != 0 {
		t.Fatalf("delayed local vector poisoned the latest snapshot: %v %v", hits, err)
	}
}

func TestSQLiteSearchSeesOtherConnectionChanges(t *testing.T) {
	reader, writer := consistencyStores(t)
	m := contractMemory(schema.TypeFact)
	suitePutMemory(t, writer, m, contractVector())
	check := func(want int, similarity float64) {
		t.Helper()
		hits, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{})
		if err != nil || len(hits) != want || want > 0 && hits[0].Similarity != similarity {
			t.Fatalf("search = %v %v; want %d sim %v", hits, err, want, similarity)
		}
	}
	check(1, 1)
	suitePutMemory(t, writer, m, []float32{0, 1, 0, 0, 0, 0, 0, 0})
	check(1, 0)
	if err := writer.DeleteMemory(m.ID); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	suitePutMemory(t, writer, m, contractVector())
	check(1, 1)
}

func TestSQLiteSearchVersionSnapshot(t *testing.T) {
	reader, writer := consistencyStores(t)
	m := contractMemory(schema.TypeFact)
	suitePutMemory(t, writer, m, contractVector())
	reader.indexRefreshHook = func() {
		reader.indexRefreshHook = nil
		suitePutMemory(t, writer, m, []float32{0, 1, 0, 0, 0, 0, 0, 0})
	}
	if _, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{}); err != nil {
		t.Fatal(err)
	}
	hits, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{})
	if err != nil || len(hits) != 1 || hits[0].Similarity != 0 {
		t.Fatalf("snapshot cached a newer generation with old vector: %v %v", hits, err)
	}
}

func TestSQLiteSearchBookkeepingNoReload(t *testing.T) {
	reader, writer := consistencyStores(t)
	m := contractMemory(schema.TypeFact)
	suitePutMemory(t, writer, m, contractVector())
	search := func() {
		t.Helper()
		if _, err := reader.SearchMemories(contractVector(), 1, MemoryFilter{}); err != nil {
			t.Fatal(err)
		}
	}
	search()
	before := reader.indexRebuilds
	if err := writer.TouchMemories([]string{m.ID}, 100); err != nil {
		t.Fatal(err)
	}
	if err := writer.SetMeta("stat", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.Exec("UPDATE memories SET activation=10, embedding=embedding WHERE id=?", m.ID); err != nil {
		t.Fatal(err)
	}
	search()
	if reader.indexRebuilds != before {
		t.Fatal("Touch/meta/stat/identical embedding rebuilt vectors")
	}
	got, _ := reader.GetMemory(m.ID)
	if got.LastAccessAt != 100 || got.Activation != 10 {
		t.Fatal("bookkeeping not visible")
	}
	if _, err := writer.db.Exec("UPDATE memories SET embedding=? WHERE id=?", encodeVec([]float32{0, 1, 0, 0, 0, 0, 0, 0}), m.ID); err != nil {
		t.Fatal(err)
	}
	search()
	if reader.indexRebuilds != before+1 {
		t.Fatal("direct vector mutation did not rebuild")
	}
}
