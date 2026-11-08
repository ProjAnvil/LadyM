//go:build enterprise

package storage

import "testing"

func maintenanceInjectFault(t *testing.T, store Store) { maintenancePGFault(t, store.(*PostgresStore)) }
func maintenanceHoldWriter(t *testing.T, store Store) func() {
	return maintenancePGWriter(t, store.(*PostgresStore))
}

func maintenanceBegin(t *testing.T, store Store) migrationTransaction {
	t.Helper()
	tx, err := store.(*PostgresStore).beginMemoryTransaction(MemoryBatch{})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
