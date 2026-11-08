package storage

import (
	"errors"
	"slices"
	"testing"
)

func TestMigrationAutomaticFinalizeFence(t *testing.T) {
	migrationBackends(t, func(t *testing.T, store Store) {
		migrationSeed(t, store)
		target := migrationIdentity("new", 8)
		generation, err := store.BeginAutomaticEmbeddingMigrationGeneration(target)
		if err != nil {
			t.Fatal(err)
		}
		status, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if !status.AutoFinalize || status.Generation != generation {
			t.Fatal("automatic intent was not persisted with generation acquisition")
		}
		if err := store.FinalizeAutomaticEmbeddingMigration(generation, target.Fingerprint()); !errors.Is(err, ErrEmbeddingMigration) {
			t.Fatalf("finalized incomplete staging: %v", err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, store, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		if err := store.ActivateEmbeddingMigration(generation); err != nil {
			t.Fatal(err)
		}
		for _, fence := range [][2]string{
			{"", target.Fingerprint()},
			{generation, ""},
			{"abandoned", target.Fingerprint()},
			{generation, migrationIdentity("other", 8).Fingerprint()},
		} {
			if err := store.FinalizeAutomaticEmbeddingMigration(fence[0], fence[1]); !errors.Is(err, ErrEmbeddingMigration) {
				t.Fatalf("invalid finalize fence accepted: %v", err)
			}
		}
		if err := store.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		next, err := store.BeginAutomaticEmbeddingMigrationGeneration(target)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, store, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		if err := store.ActivateEmbeddingMigration(next); err != nil {
			t.Fatal(err)
		}
		if err := store.FinalizeAutomaticEmbeddingMigration(generation, target.Fingerprint()); !errors.Is(err, ErrEmbeddingMigration) {
			t.Fatalf("old generation finalized a later automatic generation: %v", err)
		}
		if err := store.FinalizeAutomaticEmbeddingMigration(next, target.Fingerprint()); err != nil {
			t.Fatal(err)
		}
		status, err = store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "idle" || status.AutoFinalize || status.RollbackAvailable {
			t.Fatalf("automatic completion did not clear intent and backup: %+v", status)
		}
	})
}

func TestMigrationExplicitRollbackPolicyCannotBeAdoptedAutomatically(t *testing.T) {
	migrationBackends(t, func(t *testing.T, store Store) {
		migrationSeed(t, store)
		target := migrationIdentity("new", 8)
		generation, err := store.BeginEmbeddingMigrationGeneration(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.BeginAutomaticEmbeddingMigrationGeneration(target); !errors.Is(err, ErrEmbeddingMigration) {
			t.Fatalf("automatic resume took ownership of explicit migration: %v", err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, store, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		if err := store.ActivateEmbeddingMigration(generation); err != nil {
			t.Fatal(err)
		}
		if err := store.FinalizeAutomaticEmbeddingMigration(generation, target.Fingerprint()); !errors.Is(err, ErrEmbeddingMigration) {
			t.Fatalf("automatic finalizer erased explicitly retained backup: %v", err)
		}
		if err := store.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		// Explicit administrative finalize selects the current window in its
		// transaction, rather than inheriting any abandoned runner's generation.
		next := migrationIdentity("later", 8)
		generation, err = store.BeginEmbeddingMigrationGeneration(next)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, store, id, []float32{0, 0, 1, 0, 0, 0, 0, 0})
		}
		if err := store.ActivateEmbeddingMigration(generation); err != nil {
			t.Fatal(err)
		}
		if err := store.FinalizeEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		vector, err := store.GetEmbedding("a")
		if err != nil || !slices.Equal(vector, []float32{0, 0, 1, 0, 0, 0, 0, 0}) {
			t.Fatalf("explicit finalize did not select current active vectors: %v %v", vector, err)
		}
	})
}
