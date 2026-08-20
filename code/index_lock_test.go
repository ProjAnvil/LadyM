//go:build !enterprise

package code

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/storage"
)

// flock locks are per open file description, so a second open of the same lock
// file inside one process conflicts exactly like a second process would.
func TestIndexLockConflict(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "db.sqlite")
	store, err := storage.NewStore(dbPath, 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.py"), []byte("def f():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.ForTesting(tmp)
	cfg.DBPath = dbPath // the error message must name the locked db
	emb := storage.NewHashingEmbedding(256)

	// Hold the lock (as a competing process would).
	release, err := store.TryAcquireIndexLock()
	if err != nil {
		t.Fatal(err)
	}

	_, err = IndexCodebase(root, store, emb, cfg, "test", false, nil)
	var inProg *IndexInProgressError
	if !errors.As(err, &inProg) {
		t.Fatalf("err = %v, want IndexInProgressError", err)
	}
	if !strings.Contains(err.Error(), dbPath) {
		t.Errorf("error should name the db path, got %q", err.Error())
	}

	// Lock file must be named <db>.index.lock — identical to the Python port so
	// Go and Python processes exclude each other.
	if _, statErr := os.Stat(dbPath + ".index.lock"); statErr != nil {
		t.Errorf("lock file %q missing: %v", dbPath+".index.lock", statErr)
	}

	// After release, indexing succeeds.
	release()
	report, err := IndexCodebase(root, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatalf("index after release: %v", err)
	}
	if report.FilesIndexed != 1 {
		t.Errorf("files_indexed = %d, want 1", report.FilesIndexed)
	}
}

// Sequential runs acquire and release the lock internally — a second index
// right after the first must succeed.
func TestIndexLockReleasedBetweenRuns(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewStore(filepath.Join(tmp, "db.sqlite"), 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	root := t.TempDir()
	cfg := config.ForTesting(tmp)
	emb := storage.NewHashingEmbedding(256)

	for i := 0; i < 2; i++ {
		if _, err := IndexCodebase(root, store, emb, cfg, "test", false, nil); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}
