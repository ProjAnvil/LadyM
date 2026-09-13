//go:build !enterprise

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// flock locks are per open file description, so a second acquire inside one
// process conflicts exactly like a second process would.
func TestTryAcquireIndexLockConflict(t *testing.T) {
	s := openTestStore(t)

	release, err := s.TryAcquireIndexLock()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.TryAcquireIndexLock(); !errors.Is(err, ErrIndexLockHeld) {
		t.Errorf("second acquire err = %v, want ErrIndexLockHeld", err)
	}

	// Lock file must be named <db>.index.lock — identical to the Python port so
	// Go and Python processes exclude each other.
	if _, statErr := os.Stat(s.DBPath + ".index.lock"); statErr != nil {
		t.Errorf("lock file %q missing: %v", s.DBPath+".index.lock", statErr)
	}

	// After release, the lock can be taken again.
	release()
	release2, err := s.TryAcquireIndexLock()
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}

func TestTryAcquireIndexLockBadPath(t *testing.T) {
	s := &SQLiteStore{DBPath: filepath.Join(t.TempDir(), "no", "such", "dir", "db.sqlite")}
	release, err := s.TryAcquireIndexLock()
	if err == nil {
		release()
		t.Fatal("expected error opening lock file in missing directory")
	}
	if errors.Is(err, ErrIndexLockHeld) {
		t.Errorf("err = %v, want a plain open error, not ErrIndexLockHeld", err)
	}
}

// Two stores on one database exclude each other exactly like two worker
// replicas would: the second acquire fails fast with ErrWorkerLockHeld while
// the first holds the lock, and succeeds after release.
func TestTryAcquireWorkerLockConflict(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s1, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s1.Close() })
	s2, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })

	release, err := s1.TryAcquireWorkerLock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.TryAcquireWorkerLock(); !errors.Is(err, ErrWorkerLockHeld) {
		t.Errorf("second store acquire err = %v, want ErrWorkerLockHeld", err)
	}
	if _, statErr := os.Stat(dbPath + ".worker.lock"); statErr != nil {
		t.Errorf("lock file %q missing: %v", dbPath+".worker.lock", statErr)
	}

	// The worker lock is independent of the index lock.
	indexRelease, err := s2.TryAcquireIndexLock()
	if err != nil {
		t.Errorf("index lock while worker lock held: %v", err)
	} else {
		indexRelease()
	}

	release()
	release2, err := s2.TryAcquireWorkerLock()
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	release2()
}
