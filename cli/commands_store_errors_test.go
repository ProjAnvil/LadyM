//go:build !enterprise

// Tests for the data commands' local-store failure paths: writes against a
// read-only db file surface the sqlite error instead of succeeding silently,
// a memories table missing a column fails `stats`, an already-held index lock
// fails `index` fast, and `serve --http` reports an un-openable db.

package cli

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

// makeReadOnly flips the db file to read-only so every write through the
// store fails with "attempt to write a readonly database" while reads and
// engine construction still work.
func makeReadOnly(t *testing.T, db string) {
	t.Helper()
	if err := os.Chmod(db, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(db, 0o644) })
}

// remember on a read-only db fails at the store write.
func TestRememberCmd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	rememberFact(t, db, "seed fact")
	makeReadOnly(t, db)

	if _, err := runCmd(t, rememberCmd(), "--db", db, "another fact"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("remember on read-only db: err = %v, want readonly error", err)
	}
}

// record on a read-only db fails at the store write.
func TestRecordCmd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	rememberFact(t, db, "seed fact")
	makeReadOnly(t, db)

	if _, err := runCmd(t, recordCmd(), "--db", db, "--agent", "a", "--action", "b"); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("record on read-only db: err = %v, want readonly error", err)
	}
}

// forget on a read-only db fails at the DELETE.
func TestForgetCmd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	id := rememberFact(t, db, "doomed fact")
	makeReadOnly(t, db)

	if _, err := runCmd(t, forgetCmd(), "--db", db, id); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("forget on read-only db: err = %v, want readonly error", err)
	}
}

// consolidate with a pending episode tries to write back results and fails on
// a read-only db.
func TestConsolidateCmd_ReadOnlyDB_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	if _, err := runCmd(t, recordCmd(), "--db", db, "--agent", "a", "--action", "b"); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	makeReadOnly(t, db)

	if _, err := runCmd(t, consolidateCmd(), "--db", db); err == nil ||
		!strings.Contains(err.Error(), "readonly") {
		t.Errorf("consolidate on read-only db: err = %v, want readonly error", err)
	}
}

// A memories table missing a column the stats aggregation selects fails
// `stats` after engine construction succeeded (the column set keeps the
// startup queries working; only the aggregation trips over access_count).
func TestStatsCmd_MemoriesTableMissingColumn_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")
	rememberFact(t, db, "seed fact")

	d, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec(`DROP TABLE memories;
		CREATE TABLE memories(id TEXT, layer TEXT, type TEXT, content TEXT, summary TEXT,
			tags TEXT, metadata TEXT, source TEXT, workspace TEXT, created_at REAL,
			updated_at REAL, last_access_at REAL, activation REAL, content_hash TEXT, embedding BLOB)`)
	if err != nil {
		t.Fatalf("narrow memories table: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := runCmd(t, statsCmd(), "--db", db); err == nil ||
		!strings.Contains(err.Error(), "access_count") {
		t.Errorf("stats with narrowed memories table: err = %v, want 'no such column: access_count'", err)
	}
}

// A second engine holding the index lock makes `index` fail fast with the
// IndexInProgress error instead of interleaving writes.
func TestIndexCmd_IndexLockHeld_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")

	holder, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	release, err := holder.Store.TryAcquireIndexLock()
	if err != nil {
		t.Fatalf("holder acquire index lock: %v", err)
	}
	defer release()

	if _, err := runCmd(t, indexCmd(), "--db", db, t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "indexing is already running") {
		t.Errorf("index with held lock: err = %v, want IndexInProgressError", err)
	}
}

// `serve --http` with a db path that cannot be opened (a directory) reports
// the engine-construction error instead of hanging on ListenAndServe.
func TestServeCmd_HTTPUnopenableDB_Fails(t *testing.T) {
	isolateEnv(t)
	setGlobalConfigPath(t, "")

	if _, err := runCmd(t, serveCmd(), "--db", t.TempDir(), "--http", "127.0.0.1:0"); err == nil {
		t.Error("serve --http with a directory as --db should fail")
	}
}
