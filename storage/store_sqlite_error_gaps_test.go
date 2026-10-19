//go:build !enterprise

package storage

// Remaining error branches of the SQLite store's construction and write
// paths: NewStore's WAL-enable failure and embedding-migration failure on a
// read-only database, SweepDanglingEdges on a closed connection, and
// DeleteCodeArtifactsByFile's per-statement error branches (refs delete,
// id query, row scan, symbols delete, memories delete) induced by dropping
// or shadowing the underlying tables behind the store's back — the same
// "break the schema" pattern as store_errors_test.go, applied per branch.

import (
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// TestNewStoreReadOnlyWALEnabledFails: a file: URI with mode=ro opens the
// database read-only, so the two connection-local PRAGMAs (busy_timeout,
// foreign_keys) succeed while journal_mode=WAL fails: SQLite cannot convert a
// read-only file to WAL. Covers the enableWAL error branch of NewStore.
func TestNewStoreReadOnlyWALEnabledFails(t *testing.T) {
	// The file: URI makes filepath.Dir a relative "file:…" path, so run in a
	// throwaway cwd: MkdirAll lands in the temp dir instead of the package.
	t.Chdir(t.TempDir())
	dbPath := t.TempDir() + "/ro.db"
	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewStore("file:"+dbPath+"?mode=ro", 8, false, true)
	if err == nil {
		t.Fatal("NewStore(mode=ro, enableWAL) should fail at PRAGMA journal_mode = WAL")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("err = %v, want the read-only-database failure", err)
	}
}

// TestNewStoreReadOnlyMigrationFails: a legacy database without the embedding
// column reopened read-only — the schema statements are no-ops and
// PRAGMA table_info runs fine, but the ALTER TABLE migration cannot write to
// the read-only file. Covers NewStore's embedding-migration error branch.
func TestNewStoreReadOnlyMigrationFails(t *testing.T) {
	t.Chdir(t.TempDir())
	dbPath := t.TempDir() + "/legacy.db"
	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("ALTER TABLE memories DROP COLUMN embedding"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewStore("file:"+dbPath+"?mode=ro", 8, false, false)
	if err == nil {
		t.Fatal("NewStore(mode=ro) on a no-embedding db should fail at the ALTER migration")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("err = %v, want the read-only-database failure", err)
	}
}

// TestSweepDanglingEdgesOnClosedDB: the DELETE fails on a closed connection
// and the error surfaces (covers SweepDanglingEdges' Exec error branch).
func TestSweepDanglingEdgesOnClosedDB(t *testing.T) {
	s := closedDBStore(t)
	if _, err := s.SweepDanglingEdges(); err == nil {
		t.Error("SweepDanglingEdges on closed DB: expected error")
	}
}

// ---- DeleteCodeArtifactsByFile per-statement error branches ----
//
// Each test breaks exactly one schema object so the failure pins a specific
// statement of the function, mirroring the pipeline order refs delete → id
// query/scan → symbols delete → memories delete.

// TestDeleteCodeArtifactsRefsDeleteFails: without code_refs the very first
// DELETE fails.
func TestDeleteCodeArtifactsRefsDeleteFails(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("DROP TABLE code_refs"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "code_refs") {
		t.Fatalf("err = %v, want code_refs delete failure", err)
	}
}

// TestDeleteCodeArtifactsIdQueryFails: the refs delete does not touch
// memories, so it succeeds; the id-collection query then fails with memories
// dropped.
func TestDeleteCodeArtifactsIdQueryFails(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("DROP TABLE memories"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "memories") {
		t.Fatalf("err = %v, want memories query failure", err)
	}
}

// TestDeleteCodeArtifactsIdScanFails: a view named memories whose id column
// is NULL makes the id-collection query return a row whose scan into string
// fails (the PRIMARY KEY column itself cannot be NULL, so the view simulates
// externally corrupted data).
func TestDeleteCodeArtifactsIdScanFails(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE memories RENAME TO memories_real"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE TABLE mem_fake (nid INTEGER, mtype TEXT, msource TEXT, mworkspace TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE VIEW memories AS SELECT NULL AS id, mtype AS type, msource AS source, mworkspace AS workspace FROM mem_fake"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"INSERT INTO mem_fake VALUES (1, ?, 'svc.py', 'ws')", string(schema.TypeCodeSymbol)); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "NULL") {
		t.Fatalf("err = %v, want a NULL-to-string scan failure", err)
	}
}

// TestDeleteCodeArtifactsSymbolsDeleteFails: a view named code_symbols keeps
// the refs-delete subquery working but rejects the projection DELETE.
func TestDeleteCodeArtifactsSymbolsDeleteFails(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE code_symbols RENAME TO code_symbols_real"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE VIEW code_symbols AS SELECT memory_id, file_path, symbol_kind, qualified_name FROM code_symbols_real"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "code_symbols") {
		t.Fatalf("err = %v, want code_symbols delete failure", err)
	}
}

// TestDeleteCodeArtifactsMemoriesDeleteFails: a view named memories keeps the
// id-collection query working but rejects the final memories DELETE.
func TestDeleteCodeArtifactsMemoriesDeleteFails(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE memories RENAME TO memories_real"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE TABLE mem_fake (id TEXT, mtype TEXT, msource TEXT, mworkspace TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE VIEW memories AS SELECT id, mtype AS type, msource AS source, mworkspace AS workspace FROM mem_fake"); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteCodeArtifactsByFile("ws", "svc.py")
	if err == nil || !strings.Contains(err.Error(), "memories") {
		t.Fatalf("err = %v, want memories delete failure", err)
	}
}
