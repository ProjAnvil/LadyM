package storage

import (
	"context"
	"testing"
	"time"
)

func maintenancePGFault(t *testing.T, s *PostgresStore) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), `CREATE FUNCTION maintenance_fault_fn() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.id='job' AND NEW.metadata->'job'->>'state'='done' THEN RAISE EXCEPTION 'injected checkpoint fault'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), `CREATE TRIGGER maintenance_fault BEFORE UPDATE ON memories FOR EACH ROW EXECUTE FUNCTION maintenance_fault_fn()`); err != nil {
		t.Fatal(err)
	}
}

func maintenancePGWriter(t *testing.T, s *PostgresStore) func() {
	t.Helper()
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "LOCK TABLE memories IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}
}
