//go:build !windows

package code

import (
	"os"

	"golang.org/x/sys/unix"
)

// acquireIndexLock takes a non-blocking exclusive flock on <db>.index.lock,
// mirroring the Python indexer (os.open(O_CREAT|O_RDWR, 0644) +
// fcntl.flock(LOCK_EX|LOCK_NB)). The lock is per open file description and is
// released automatically on process exit (including crashes), so it cannot
// deadlock across processes. Contention fails fast with IndexInProgressError
// — callers do not queue.
func acquireIndexLock(dbPath string) (func(), error) {
	f, err := os.OpenFile(indexLockPath(dbPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, &IndexInProgressError{DBPath: dbPath}
	}
	// Closing the fd drops the flock; no explicit LOCK_UN needed.
	return func() { _ = f.Close() }, nil
}
