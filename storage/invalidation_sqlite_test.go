//go:build !enterprise

package storage

import (
	"fmt"
	"testing"
)

func contractTransaction(s Store) (memoryTransaction, error) {
	if sqlite, ok := s.(*SQLiteStore); ok {
		return sqlite.beginMemoryTransaction()
	}
	return s.(*PostgresStore).beginMemoryTransaction(MemoryBatch{})
}

func failDescendantWrite(t *testing.T, s Store, id string) {
	t.Helper()
	if sqlite, ok := s.(*SQLiteStore); ok {
		if _, err := sqlite.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_invalidation BEFORE UPDATE ON memories
			WHEN OLD.id='%s' BEGIN SELECT RAISE(ABORT,'invalidation failure'); END`, id)); err != nil {
			t.Fatal(err)
		}
		return
	}
	failPGDescendantWrite(t, s.(*PostgresStore), id)
}
