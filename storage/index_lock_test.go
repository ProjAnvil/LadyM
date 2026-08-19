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
