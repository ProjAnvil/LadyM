//go:build !enterprise

package storage

import (
	"context"
	"testing"
)

func maintenanceInjectFault(t *testing.T, store Store) {
	t.Helper()
	if pg, ok := store.(*PostgresStore); ok {
		maintenancePGFault(t, pg)
		return
	}
	s := store.(*SQLiteStore)
	if _, err := s.db.Exec(`CREATE TRIGGER maintenance_fault BEFORE UPDATE ON memories
		WHEN NEW.id='job' AND json_extract(NEW.metadata,'$.job.state')='done'
		BEGIN SELECT RAISE(ABORT,'injected checkpoint fault'); END`); err != nil {
		t.Fatal(err)
	}
}

func maintenanceHoldWriter(t *testing.T, store Store) func() {
	t.Helper()
	if pg, ok := store.(*PostgresStore); ok {
		return maintenancePGWriter(t, pg)
	}
	tx, err := store.(*SQLiteStore).db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE memory_vector_generation SET generation=generation"); err != nil {
		t.Fatal(err)
	}
	return func() { _ = tx.Rollback() }
}

func maintenanceBegin(t *testing.T, store Store) migrationTransaction {
	t.Helper()
	var tx migrationTransaction
	var err error
	if pg, ok := store.(*PostgresStore); ok {
		tx, err = pg.beginMemoryTransaction(MemoryBatch{})
	} else {
		tx, err = store.(*SQLiteStore).beginMemoryTransaction()
	}
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
