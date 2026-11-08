package storage

import (
	"math"
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func migrationIdentity(model string, dim int) schema.EmbeddingIdentity {
	return schema.EmbeddingIdentity{Provider: "test", Model: model, Dim: dim, Tokenizer: "v1"}
}

func migrationSeed(t *testing.T, s Store) {
	t.Helper()
	for _, id := range []string{"a", "b", "c", "d"} {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID, m.Content, m.Workspace = id, id, "test"
		if err := s.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AdoptEmbeddingIdentity(migrationIdentity("old", 8)); err != nil {
		t.Fatal(err)
	}
}

func migrationStage(t *testing.T, s Store, id string, vector []float32) {
	t.Helper()
	status, err := s.EmbeddingMigrationStatus()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StageEmbeddingMigration(id, vector, status.TargetFingerprint, status.Generation); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationFingerprintAndResume(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		target := migrationIdentity("new", 8)
		if err := s.BeginEmbeddingMigration(target); err != nil {
			t.Fatal(err)
		}
		first, _ := s.EmbeddingMigrationStatus()
		for _, id := range []string{"a", "b"} {
			migrationStage(t, s, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		if err := s.BeginEmbeddingMigration(target); err != nil {
			t.Fatal(err)
		}
		status, _ := s.EmbeddingMigrationStatus()
		if status.Cursor != "b" || status.Processed != 2 || status.Generation != first.Generation {
			t.Fatalf("resume lost checkpoint: %+v", status)
		}
		batch, err := s.EmbeddingMigrationBatch(status.Generation, 2)
		if err != nil || len(batch) != 2 || batch[0].ID != "c" || batch[1].ID != "d" {
			t.Fatalf("keyset batch: %v %v", batch, err)
		}
		if err := s.BeginEmbeddingMigration(migrationIdentity("other", 8)); err == nil {
			t.Fatal("another target inherited migration")
		}
		if err := s.StageEmbeddingMigration("d", make([]float32, 8), status.TargetFingerprint, status.Generation); err == nil {
			t.Fatal("out of order stage accepted")
		}
		bad := make([]float32, 8)
		bad[0] = float32(math.NaN())
		if err := s.StageEmbeddingMigration("c", bad, status.TargetFingerprint, status.Generation); err == nil {
			t.Fatal("non-finite stage accepted")
		}
		if err := s.ActivateEmbeddingMigration(status.Generation); err == nil {
			t.Fatal("incomplete activation accepted")
		}
		if err := s.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginEmbeddingMigration(target); err != nil {
			t.Fatal(err)
		}
		next, _ := s.EmbeddingMigrationStatus()
		if next.Generation == first.Generation || next.Cursor != "" || next.Processed != 0 {
			t.Fatalf("new generation reused checkpoint: %+v", next)
		}
		if err := s.StageEmbeddingMigration("a", make([]float32, 8), next.TargetFingerprint, first.Generation); err == nil {
			t.Fatal("abandoned generation published")
		}
	})
}

func TestMigrationAtomicActivateRollback(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 3)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0})
		}
		if v, err := s.GetEmbedding("a"); err != nil || !slices.Equal(v, []float32{1, 0, 0, 0, 0, 0, 0, 0}) {
			t.Fatalf("staging changed active vector: %v %v", v, err)
		}
		if err := s.UpdateMemoryContent("a", "changed", "", nil, nil, 1); err == nil {
			t.Fatal("staging allowed content write")
		}
		if err := s.TouchMemories([]string{"a"}, 123); err != nil {
			t.Fatal(err)
		}
		status, _ := s.EmbeddingMigrationStatus()
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
		if v, err := s.GetEmbedding("a"); err != nil || !slices.Equal(v, []float32{0, 1, 0}) {
			t.Fatalf("activation vector: %v %v", v, err)
		}
		hits, err := s.SearchMemories([]float32{0, 1, 0}, 4, MemoryFilter{})
		if err != nil || len(hits) != 4 {
			t.Fatalf("new index: %v %v", hits, err)
		}
		m, _ := s.GetMemory("a")
		m.Metadata["scope"] = "changed"
		if err := s.PutMemory(m, nil); err == nil {
			t.Fatal("rollback window allowed trust/scope write")
		}
		if err := s.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		status, _ = s.EmbeddingMigrationStatus()
		if status.ActiveFingerprint != migrationIdentity("old", 8).Fingerprint() || status.RollbackAvailable {
			t.Fatalf("rollback identity: %+v", status)
		}
		if v, _ := s.GetEmbedding("a"); !slices.Equal(v, []float32{1, 0, 0, 0, 0, 0, 0, 0}) {
			t.Fatalf("rollback vector: %v", v)
		}
		m, _ = s.GetMemory("a")
		if m.LastAccessAt != 123 {
			t.Fatal("rollback undid Touch")
		}
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 3)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0})
		}
		status, _ = s.EmbeddingMigrationStatus()
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
		if err := s.FinalizeEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateMemoryContent("a", "changed", "", nil, []float32{1, 0, 0}, 1); err != nil {
			t.Fatal(err)
		}
		if err := s.RollbackEmbeddingMigration(); err == nil {
			t.Fatal("finalized migration rolled back")
		}
	})
}

func TestMigrationDeleteNeverResurrects(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		m, _ := s.GetMemory("b")
		m.Metadata["source_episode"] = "a"
		if err := s.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		migrationStage(t, s, "a", []float32{0, 1, 0, 0, 0, 0, 0, 0})
		old, _ := s.EmbeddingMigrationStatus()
		if err := s.DeleteMemory("a"); err != nil {
			t.Fatal(err)
		}
		status, _ := s.EmbeddingMigrationStatus()
		if status.State != "idle" {
			t.Fatalf("delete did not cancel stage: %+v", status)
		}
		b, _ := s.GetMemory("b")
		if schema.IsUsable(b) {
			t.Fatal("delete failed recursive invalidation")
		}
		if err := s.StageEmbeddingMigration("b", make([]float32, 8), old.TargetFingerprint, old.Generation); err == nil {
			t.Fatal("deleted generation published")
		}
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		status, _ = s.EmbeddingMigrationStatus()
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteMemory("c"); err != nil {
			t.Fatal(err)
		}
		if err := s.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "c"} {
			m, err := s.GetMemory(id)
			if err != nil || m != nil {
				t.Fatalf("deleted source resurrected: %s %v", id, err)
			}
			if v, err := s.GetEmbedding(id); err != nil || v != nil {
				t.Fatalf("deleted vector resurrected: %s %v", id, err)
			}
		}
	})
}

func TestMigrationPathAndLegacyAdopt(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
		m.ID, m.Content = "path", "/private/path"
		m.Metadata["resource_identity"] = "path"
		if err := s.PutMemory(m, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.AdoptEmbeddingIdentity(migrationIdentity("wrong", 3)); err == nil {
			t.Fatal("adoption reinterpreted dimensions")
		}
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 3)); err != nil {
			t.Fatal(err)
		}
		status, _ := s.EmbeddingMigrationStatus()
		batch, err := s.EmbeddingMigrationBatch(status.Generation, 10)
		if err != nil || len(batch) != 4 {
			t.Fatalf("path eligible: %v %v", batch, err)
		}
		if err := s.StageEmbeddingMigration("path", []float32{1, 0, 0}, status.TargetFingerprint, status.Generation); err == nil {
			t.Fatal("path acquired embedding")
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, s, id, []float32{1, 0, 0})
		}
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
		if v, err := s.GetEmbedding("path"); err != nil || v != nil {
			t.Fatalf("path vector: %v %v", v, err)
		}
		v, _ := s.GetEmbedding("a")
		v[0] = 99
		if v, _ := s.GetEmbedding("a"); v[0] != 1 {
			t.Fatal("GetEmbedding aliases stored data")
		}
	})
}

func TestMigrationClosedStoreErrors(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, err := range []error{
			s.BeginEmbeddingMigration(migrationIdentity("new", 3)),
			s.StageEmbeddingMigration("a", []float32{1, 0, 0}, "fingerprint", "generation"),
			s.ActivateEmbeddingMigration("generation"),
			s.RollbackEmbeddingMigration(), s.FinalizeEmbeddingMigration(),
			s.AdoptEmbeddingIdentity(migrationIdentity("old", 8)),
		} {
			if err == nil {
				t.Fatal("closed store returned success")
			}
		}
		if _, err := s.EmbeddingMigrationStatus(); err == nil {
			t.Fatal("closed status returned success")
		}
		if _, err := s.GetEmbedding("a"); err == nil {
			t.Fatal("closed GetEmbedding returned success")
		}
	})
}

func TestMigrationAdoptEmptyDimension(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		// An empty legacy store has no vectors whose meaning could be changed.
		if err := s.AdoptEmbeddingIdentity(migrationIdentity("adopted", 3)); err != nil {
			t.Fatal(err)
		}
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID, m.Content = "a", "a"
		if err := s.PutMemory(m, []float32{1, 0, 0}); err != nil {
			t.Fatal(err)
		}
		if hits, err := s.SearchMemories([]float32{1, 0, 0}, 1, MemoryFilter{}); err != nil || len(hits) != 1 {
			t.Fatalf("adopted column/index dimension: %v %v", hits, err)
		}
	})
}

func TestMigrationAutomaticDeletionFrozen(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		migrationStage(t, s, "a", []float32{0, 1, 0, 0, 0, 0, 0, 0})
		before, _ := s.EmbeddingMigrationStatus()
		deleted, _, err := s.DeleteMemorySafely("c", schema.Now()+60)
		if err == nil || deleted {
			t.Fatal("automatic cleanup aborted staging and deleted frozen content")
		}
		after, _ := s.EmbeddingMigrationStatus()
		if after.Generation != before.Generation || after.Cursor != "a" {
			t.Fatal("automatic cleanup disturbed the checkpoint")
		}
		if m, _ := s.GetMemory("c"); m == nil {
			t.Fatal("automatic cleanup deleted frozen source")
		}
		for _, id := range []string{"b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		if err := s.ActivateEmbeddingMigration(before.Generation); err != nil {
			t.Fatal(err)
		}
		if deleted, _, err := s.DeleteMemorySafely("c", schema.Now()+60); err == nil || deleted {
			t.Fatal("automatic cleanup mutated rollback window")
		}
		if err := s.DeleteMemory("c"); err != nil {
			t.Fatal(err)
		}
		if err := s.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		if m, _ := s.GetMemory("c"); m != nil {
			t.Fatal("explicitly deleted source resurrected")
		}
	})
}
