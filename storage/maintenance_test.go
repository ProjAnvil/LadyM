package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

func maintenanceBatch(t *testing.T, store Store) (MemoryBatch, *schema.Memory, *schema.Memory) {
	t.Helper()
	input := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	input.ID, input.Workspace, input.Content = "input", "jobs", "source"
	if err := store.PutMemory(input, nil); err != nil {
		t.Fatal(err)
	}
	fp, _ := schema.SourceFingerprint(input)
	job := &schema.MaintenanceJob{ID: "job", Workspace: "jobs", TargetID: input.ID, Operation: "new_episode",
		State: "running", Revision: 2, Attempt: 1, Owner: "owner", LeaseUntil: schema.Now() + 60,
		Args: map[string]any{}, InputIDs: []string{input.ID}, Fingerprints: map[string]string{input.ID: fp}, InputRevisions: map[string]int64{input.ID: 0}}
	memory := schema.NewMemory(schema.LayerEpisodic, schema.TypeMaintenanceJob)
	memory.ID, memory.Workspace = job.ID, job.Workspace
	job.SetMemory(memory)
	if err := store.PutMemory(memory, nil); err != nil {
		t.Fatal(err)
	}
	state, err := store.EmbeddingMigrationStatus()
	if err != nil {
		t.Fatal(err)
	}
	fence := &MaintenanceFence{Context: t.Context(), JobID: job.ID, Owner: job.Owner, Revision: job.Revision, LeaseUntil: job.LeaseUntil,
		Generation: state.Generation, Fingerprint: state.ActiveFingerprint}
	output := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	output.ID, output.Workspace, output.Content = "output", "jobs", "generated"
	stamp := input.Clone()
	stamp.Metadata["maintenance_input_stamp"] = job.ID
	done := memory.Clone()
	job.State, job.Revision, job.Checkpoint = "done", job.Revision+1, map[string]any{"cursor": input.ID}
	job.SetMemory(done)
	return MemoryBatch{MaintenanceFence: fence, Edges: []*schema.Edge{schema.NewEdge(output.ID, "produced_from", input.ID)}, Guards: []MemoryGuard{{Expected: input}, {Expected: memory}},
		Writes: []MemoryWrite{{Memory: output}, {Memory: stamp, PreserveEmbedding: true}, {Memory: done}}}, memory, input
}

func TestMaintenanceAtomicCheckpoint(t *testing.T) {
	for _, condition := range []string{"success", "database_fault", "expired_no_takeover", "old_owner", "input_changed", "generation", "cancelled"} {
		t.Run(condition, func(t *testing.T) {
			runStoreBackends(t, func(t *testing.T, store Store) {
				batch, memory, input := maintenanceBatch(t, store)
				switch condition {
				case "database_fault":
					maintenanceInjectFault(t, store)
				case "expired_no_takeover":
					j, _ := schema.MaintenanceJobFromMemory(memory)
					j.LeaseUntil = schema.Now() - 1
					j.SetMemory(memory)
					if err := store.PutMemory(memory, nil); err != nil {
						t.Fatal(err)
					}
					batch.MaintenanceFence.LeaseUntil = j.LeaseUntil
					batch.Guards[1].Expected = memory
				case "old_owner":
					batch.MaintenanceFence.Owner = "abandoned"
				case "input_changed":
					if err := store.UpdateMemoryContent(input.ID, "new", "", nil, nil, schema.Now()); err != nil {
						t.Fatal(err)
					}
				case "generation":
					batch.MaintenanceFence.Generation = "abandoned_generation"
				case "cancelled":
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					batch.MaintenanceFence.Context = ctx
				}
				err := store.WithContext(t.Context()).ApplyMemoryBatch(batch)
				if condition == "success" {
					if err != nil {
						t.Fatal(err)
					}
					done, _ := store.GetMemory(memory.ID)
					j, _ := schema.MaintenanceJobFromMemory(done)
					source, _ := store.GetMemory(input.ID)
					if j.State != "done" || j.Checkpoint == nil || source.Meta("maintenance_input_stamp") == nil {
						t.Fatal("partial successful commit")
					}
				} else {
					if err == nil {
						t.Fatal("invalid completion succeeded")
					}
					if output, _ := store.GetMemory("output"); output != nil {
						t.Fatal("partial output escaped rollback")
					}
					if count, err := store.CountEdges(); err != nil || count != 0 {
						t.Fatalf("partial edge escaped rollback %d %v", count, err)
					}
					current, _ := store.GetMemory(memory.ID)
					j, _ := schema.MaintenanceJobFromMemory(current)
					source, _ := store.GetMemory(input.ID)
					if j.State != "running" || j.Checkpoint != nil || source.Meta("maintenance_input_stamp") != nil {
						t.Fatal("partial checkpoint escaped rollback")
					}
				}
			})
		})
	}
}

func TestMaintenanceDatabaseCancellation(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, store Store) {
		release := maintenanceHoldWriter(t, store)
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		defer cancel()
		start := time.Now()
		m := schema.NewMemory(schema.LayerEpisodic, schema.TypeMaintenanceJob)
		err := store.WithContext(ctx).PutMemory(m, nil)
		release()
		if err == nil || time.Since(start) > time.Second {
			t.Fatalf("writer wait did not cancel: %v after %s", err, time.Since(start))
		}
		if err := store.Ping(); err != nil {
			t.Fatalf("context mutated root: %v", err)
		}
	})
}

func TestMaintenanceContextView(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, store Store) {
		ctx, cancel := context.WithCancel(t.Context())
		view := store.WithContext(ctx)
		cancel()
		if _, err := view.IterMemories("", "", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("query cancellation %v", err)
		}
		if err := store.Ping(); err != nil {
			t.Fatal("cancelled view poisoned root")
		}
	})
}
