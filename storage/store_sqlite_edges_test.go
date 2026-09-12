//go:build !enterprise

package storage

// SQLite error paths that the drop-table suite cannot reach: scan failures
// from NULL/type-mismatched columns (SQLite's flexible typing stores what
// the schema would normally forbid), closed-DB user-store methods, and
// NewStore setup failures past the MkdirAll step.

import (
	"path/filepath"
	"testing"
)

// recreateMemoriesNullable replaces the memories table with a constraint-free
// twin so tests can plant rows (NULL content/layer/workspace) that the real
// schema rejects — the state a corrupted or externally-managed db can hold.
func recreateMemoriesNullable(t *testing.T, s *SQLiteStore) {
	t.Helper()
	if _, err := s.db.Exec("ALTER TABLE memories RENAME TO memories_orig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE memories (
		id TEXT PRIMARY KEY, layer TEXT, type TEXT, content TEXT, summary TEXT,
		tags TEXT, metadata TEXT, source TEXT, workspace TEXT,
		created_at DOUBLE PRECISION, updated_at DOUBLE PRECISION,
		last_access_at DOUBLE PRECISION, access_count INTEGER,
		activation DOUBLE PRECISION, content_hash TEXT, embedding BLOB)`); err != nil {
		t.Fatal(err)
	}
}

// TestSQLiteUpdateMemoryContentOnClosedDB: both the nil-vector and the
// vector branches of UpdateMemoryContent surface the closed-DB error.
func TestSQLiteUpdateMemoryContentOnClosedDB(t *testing.T) {
	s := closedDBStore(t)
	if err := s.UpdateMemoryContent("x", "c", "s", nil, nil, 0); err == nil {
		t.Error("UpdateMemoryContent (nil vector) on closed DB: expected error")
	}
	if err := s.UpdateMemoryContent("x", "c", "s", nil, []float32{1}, 0); err == nil {
		t.Error("UpdateMemoryContent (with vector) on closed DB: expected error")
	}
}

// TestSQLiteUserStoreOnClosedDB: the user CRUD methods were missing from
// the closed-DB suite; every one must surface the driver error.
func TestSQLiteUserStoreOnClosedDB(t *testing.T) {
	s := closedDBStore(t)
	if _, err := s.GetUser("u"); err == nil {
		t.Error("GetUser on closed DB: expected error")
	}
	if _, err := s.ListUsers(); err == nil {
		t.Error("ListUsers on closed DB: expected error")
	}
}

// TestSQLiteUserScanTypeMismatch: a TEXT value in the BOOLEAN admin column
// (SQLite stores it, affinity aside) breaks the user row scan.
func TestSQLiteUserScanTypeMismatch(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, workspace, admin, created_at)
		 VALUES ('u', 'h', 'w', 'notabool', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUser("u"); err == nil {
		t.Error("GetUser: expected scan error")
	}
	if _, err := s.ListUsers(); err == nil {
		t.Error("ListUsers: expected scan error")
	}
}

// TestSQLiteEpisodicContentsSinceScanError: a NULL content row in the
// episodic window fails the string scan.
func TestSQLiteEpisodicContentsSinceScanError(t *testing.T) {
	s := openTestStore(t)
	recreateMemoriesNullable(t, s)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, workspace, created_at)
		 VALUES ('e1', 'L1_episodic', 'event', NULL, 'w', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EpisodicContentsSince("w", 0); err == nil {
		t.Error("EpisodicContentsSince: expected scan error for NULL content")
	}
}

// TestSQLiteCountScanError: a NULL layer row fails the COUNT grouping scan.
func TestSQLiteCountScanError(t *testing.T) {
	s := openTestStore(t)
	recreateMemoriesNullable(t, s)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, workspace, created_at)
		 VALUES ('e1', NULL, 'event', 'x', 'w', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Count(""); err == nil {
		t.Error("Count: expected scan error for NULL layer")
	}
}

// TestSQLiteWorkspacesScanError: a NULL workspace row fails the DISTINCT
// scan.
func TestSQLiteWorkspacesScanError(t *testing.T) {
	s := openTestStore(t)
	recreateMemoriesNullable(t, s)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, workspace, created_at)
		 VALUES ('e1', 'L1_episodic', 'event', 'x', NULL, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Workspaces(); err == nil {
		t.Error("Workspaces: expected scan error for NULL workspace")
	}
}

// TestSQLiteNeighborCountsScanError: a NULL endpoint fails the scan inside
// the UNION ALL neighbour count.
func TestSQLiteNeighborCountsScanError(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE edges RENAME TO edges_orig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TABLE edges (
		id TEXT PRIMARY KEY, src_id TEXT, relation TEXT, dst_id TEXT,
		weight DOUBLE PRECISION, valid_from DOUBLE PRECISION,
		valid_to DOUBLE PRECISION, metadata TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from)
		 VALUES ('e1', NULL, 'rel', 'b', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NeighborCounts(); err == nil {
		t.Error("NeighborCounts: expected scan error for NULL src_id")
	}
}

// TestSQLiteRefsForSymbolScanError: NULL endpoints in code_refs fail the
// scan on both the "out" and the "in" direction.
func TestSQLiteRefsForSymbolScanError(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec("ALTER TABLE code_refs RENAME TO code_refs_orig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		"CREATE TABLE code_refs (src_symbol TEXT, dst_symbol TEXT, ref_kind TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO code_refs (src_symbol, dst_symbol, ref_kind) VALUES
		 ('a', NULL, 'calls'), (NULL, 'b', 'calls')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RefsForSymbol("a", "out"); err == nil {
		t.Error("RefsForSymbol(out): expected scan error for NULL dst_symbol")
	}
	if _, err := s.RefsForSymbol("b", "in"); err == nil {
		t.Error("RefsForSymbol(in): expected scan error for NULL src_symbol")
	}
}

// TestWarmIndexSkipsUnscannableRows: warmIndexFromBlobs skips rows whose id
// fails to scan (SQLite permits NULL in a TEXT PRIMARY KEY) and still warms
// the valid ones.
func TestWarmIndexSkipsUnscannableRows(t *testing.T) {
	s := openTestStore(t)
	blob := make([]byte, s.Dim*4)
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, created_at, updated_at, last_access_at, embedding)
		 VALUES (NULL, 'L1_episodic', 'event', 'x', 0, 0, 0, ?)`, blob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, created_at, updated_at, last_access_at, embedding)
		 VALUES ('good', 'L1_episodic', 'event', 'x', 0, 0, 0, ?)`, blob); err != nil {
		t.Fatal(err)
	}
	s.warmIndexFromBlobs()
	if n := s.vectorIndex.Len(); n != 1 {
		t.Errorf("index len after warm = %d, want 1 (the NULL-id row skipped)", n)
	}
}

// TestNewStoreDBPathIsDirectory: sql.Open is lazy, so the first PRAGMA hits
// the "unable to open database file" error when dbPath is a directory.
func TestNewStoreDBPathIsDirectory(t *testing.T) {
	if _, err := NewStore(t.TempDir(), 8, false, false); err == nil {
		t.Error("NewStore with a directory as dbPath should fail at PRAGMA busy_timeout")
	}
}

// TestNewStoreMemoriesIsView: a pre-existing VIEW named memories satisfies
// CREATE TABLE IF NOT EXISTS but cannot be ALTERed, so the embedding-column
// migration fails deterministically.
func TestNewStoreMemoriesIsView(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "view.db")
	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the table with a same-named view (simulates an externally
	// managed/corrupted db) — keep the original around so the file is valid.
	if _, err := s.db.Exec("ALTER TABLE memories RENAME TO memories_orig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE VIEW memories AS SELECT 'x' AS id"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewStore(dbPath, 8, false, false); err == nil {
		t.Error("NewStore with memories as a view should fail (schema or migration step)")
	}
}
