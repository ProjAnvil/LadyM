package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

type expiryTransaction struct{ migrationTransaction }

func (tx expiryTransaction) put(w MemoryWrite, access bool) error {
	if w.Memory.ID == "job" {
		time.Sleep(70 * time.Millisecond)
	}
	return tx.migrationTransaction.put(w, access)
}

func TestMaintenanceLeaseExpiresInTransaction(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, store Store) {
		batch, memory, _ := maintenanceBatch(t, store)
		job, _ := schema.MaintenanceJobFromMemory(memory)
		job.LeaseUntil = schema.Now() + 0.04
		job.SetMemory(memory)
		if err := store.PutMemory(memory, nil); err != nil {
			t.Fatal(err)
		}
		batch.Guards[1].Expected = memory
		batch.MaintenanceFence.LeaseUntil = job.LeaseUntil
		tx := maintenanceBegin(t, store)
		err := applyMemoryBatch(expiryTransaction{tx}, batch, true)
		if !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("lease expired while writing: %v", err)
		}
		if output, _ := store.GetMemory("output"); output != nil {
			t.Fatal("expired transaction published")
		}
	})
}
