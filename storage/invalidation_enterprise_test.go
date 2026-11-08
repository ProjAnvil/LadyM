//go:build enterprise

package storage

import "testing"

func contractTransaction(s Store) (memoryTransaction, error) {
	return s.(*PostgresStore).beginMemoryTransaction(MemoryBatch{})
}

func failDescendantWrite(t *testing.T, s Store, id string) {
	t.Helper()
	failPGDescendantWrite(t, s.(*PostgresStore), id)
}
