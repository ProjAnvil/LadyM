package storage

// Postgres error branches of the re-index cleanup pipeline: each statement
// of DeleteCodeArtifactsByFile fails in isolation (dropped table / view in
// place of the projection table), plus SweepDanglingEdges on a closed pool.
// Pattern mirrors store_postgres_scan_errors_test.go and the SQLite-side
// store_sqlite_error_gaps_test.go: run only when LADYM_TEST_PG_DSN is set.

import (
	"strings"
	"testing"
)

// TestPostgresDeleteCodeArtifactsRefsDeleteFails: without code_refs the very
// first DELETE fails and the error surfaces.
func TestPostgresDeleteCodeArtifactsRefsDeleteFails(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	if _, err := s.pool.Exec(t.Context(), "DROP TABLE code_refs"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "code_refs") {
		t.Fatalf("err = %v, want code_refs delete failure", err)
	}
}

// TestPostgresDeleteCodeArtifactsSymbolsDeleteFails: a view named
// code_symbols keeps the refs-delete subquery working but rejects the
// projection DELETE. The view must be DISTINCT: PostgreSQL auto-updatable
// simple views would silently forward the DELETE to the base table.
func TestPostgresDeleteCodeArtifactsSymbolsDeleteFails(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	ctx := t.Context()
	if _, err := s.pool.Exec(ctx, "ALTER TABLE code_symbols RENAME TO code_symbols_real"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		"CREATE VIEW code_symbols AS SELECT DISTINCT memory_id, file_path, symbol_kind, qualified_name FROM code_symbols_real"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "code_symbols") {
		t.Fatalf("err = %v, want code_symbols delete failure", err)
	}
}

// TestPostgresSweepDanglingEdgesOnClosedPool: the DELETE fails on a closed
// pool and the error surfaces (covers SweepDanglingEdges' Exec error branch).
func TestPostgresSweepDanglingEdgesOnClosedPool(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	s.Close()
	if _, err := s.SweepDanglingEdges(); err == nil {
		t.Error("SweepDanglingEdges on closed pool: expected error")
	}
}
