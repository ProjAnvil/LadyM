package storage

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

func migrationSQL(t *testing.T, s Store, query string, args ...any) {
	t.Helper()
	var err error
	switch s := s.(type) {
	case *PostgresStore:
		_, err = s.pool.Exec(context.Background(), pgMigrationQuery(query), args...)
	default:
		err = migrationSQLiteExec(s, query, args...)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationActivationFailureAndCompleteness(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 3)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0})
		}
		status, _ := s.EmbeddingMigrationStatus()
		migrationSQL(t, s, "UPDATE embedding_staging SET id='orphan' WHERE id='d'")
		if err := s.ActivateEmbeddingMigration(status.Generation); err == nil {
			t.Fatal("equal counts accepted missing eligible ID")
		}
		migrationSQL(t, s, "UPDATE embedding_staging SET id='d' WHERE id='orphan'")
		cleanup := migrationActivationFault(t, s)
		if err := s.ActivateEmbeddingMigration(status.Generation); err == nil {
			t.Fatal("activation database failure swallowed")
		}
		after, _ := s.EmbeddingMigrationStatus()
		if after.State != "staging" || after.ActiveFingerprint != migrationIdentity("old", 8).Fingerprint() {
			t.Fatalf("failed activation switched metadata: %+v", after)
		}
		if v, _ := s.GetEmbedding("a"); !slices.Equal(v, []float32{1, 0, 0, 0, 0, 0, 0, 0}) {
			t.Fatalf("failed activation changed vector: %v", v)
		}
		if hits, err := s.SearchMemories([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 4, MemoryFilter{}); err != nil || len(hits) != 4 {
			t.Fatalf("failed activation damaged index: %v %v", hits, err)
		}
		cleanup()
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMigrationRawSQLGate(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			"UPDATE memories SET content='changed' WHERE id='a'",
			"UPDATE memories SET metadata='{\"scope\":\"changed\"}' WHERE id='a'",
			"UPDATE memories SET embedding=NULL WHERE id='a'",
			"UPDATE meta SET value='3' WHERE key='embedding_dim'",
			"INSERT INTO memories SELECT 'e',layer,type,content,summary,tags,metadata,source,workspace,created_at,updated_at,last_access_at,access_count,activation,content_hash,embedding FROM memories WHERE id='a'",
		} {
			var err error
			switch s := s.(type) {
			case *PostgresStore:
				_, err = s.pool.Exec(t.Context(), query)
			default:
				err = migrationSQLiteExec(s, query)
			}
			if err == nil {
				t.Fatalf("raw SQL bypassed freeze: %s", query)
			}
		}
		migrationSQL(t, s, "UPDATE memories SET last_access_at=123 WHERE id='a'")
	})
}

func migrationActivationFault(t *testing.T, s Store) func() {
	t.Helper()
	if _, ok := s.(*PostgresStore); ok {
		migrationSQL(t, s, `CREATE FUNCTION migration_test_fault() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN IF NEW.id='c' THEN RAISE EXCEPTION 'activation fault'; END IF; RETURN NEW; END $$`)
		migrationSQL(t, s, "CREATE TRIGGER migration_test_fault BEFORE UPDATE OF embedding ON memories FOR EACH ROW EXECUTE FUNCTION migration_test_fault()")
		return func() {
			migrationSQL(t, s, "DROP TRIGGER migration_test_fault ON memories")
			migrationSQL(t, s, "DROP FUNCTION migration_test_fault()")
		}
	}
	migrationSQL(t, s, "CREATE TRIGGER migration_test_fault BEFORE UPDATE OF embedding ON memories WHEN NEW.id='c' BEGIN SELECT RAISE(ABORT,'activation fault'); END")
	return func() { migrationSQL(t, s, "DROP TRIGGER migration_test_fault") }
}

func TestMigrationStageCheckpointFailure(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		migrationStage(t, s, "a", []float32{0, 1, 0, 0, 0, 0, 0, 0})
		if _, ok := s.(*PostgresStore); ok {
			migrationSQL(t, s, `CREATE FUNCTION migration_checkpoint_fault() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN IF NEW.data::jsonb->>'cursor'='b' THEN RAISE EXCEPTION 'checkpoint fault'; END IF; RETURN NEW; END $$`)
			migrationSQL(t, s, "CREATE TRIGGER migration_checkpoint_fault BEFORE UPDATE ON embedding_migration FOR EACH ROW EXECUTE FUNCTION migration_checkpoint_fault()")
		} else {
			migrationSQL(t, s, "CREATE TRIGGER migration_checkpoint_fault BEFORE UPDATE ON embedding_migration WHEN json_extract(NEW.data,'$.cursor')='b' BEGIN SELECT RAISE(ABORT,'checkpoint fault'); END")
		}
		status, _ := s.EmbeddingMigrationStatus()
		if err := s.StageEmbeddingMigration("b", make([]float32, 8), status.TargetFingerprint, status.Generation); err == nil {
			t.Fatal("checkpoint failure swallowed")
		}
		after, err := s.EmbeddingMigrationStatus()
		if err != nil || after.Cursor != "a" || after.Processed != 1 {
			t.Fatalf("failed checkpoint advanced: %+v %v", after, err)
		}
		if _, ok := s.(*PostgresStore); ok {
			migrationSQL(t, s, "DROP TRIGGER migration_checkpoint_fault ON embedding_migration")
			migrationSQL(t, s, "DROP FUNCTION migration_checkpoint_fault()")
		} else {
			migrationSQL(t, s, "DROP TRIGGER migration_checkpoint_fault")
		}
		migrationStage(t, s, "b", []float32{0, 1, 0, 0, 0, 0, 0, 0})
	})
}

func TestMigrationConcurrentReaders(t *testing.T) {
	migrationBackends(t, func(t *testing.T, s Store) {
		migrationSeed(t, s)
		if err := s.BeginEmbeddingMigration(migrationIdentity("new", 8)); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			migrationStage(t, s, id, []float32{0, 1, 0, 0, 0, 0, 0, 0})
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() {
				for range 10 {
					hits, err := s.SearchMemories([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 4, MemoryFilter{})
					if err != nil {
						errs <- err
						return
					}
					if len(hits) != 4 {
						errs <- errors.New("reader observed partial vectors")
						return
					}
					for _, hit := range hits {
						if hit.Similarity != hits[0].Similarity {
							errs <- errors.New("reader mixed old and new vectors")
							return
						}
					}
				}
			})
		}
		status, _ := s.EmbeddingMigrationStatus()
		if err := s.ActivateEmbeddingMigration(status.Generation); err != nil {
			t.Fatal(err)
		}
		if err := s.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
	})
}
