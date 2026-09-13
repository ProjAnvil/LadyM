//go:build !windows && !enterprise

package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// acquireIndexLock takes a non-blocking exclusive flock on <db>.index.lock,
// mirroring the Python indexer (os.open(O_CREAT|O_RDWR, 0644) +
// fcntl.flock(LOCK_EX|LOCK_NB)).
func acquireIndexLock(dbPath string) (func(), error) {
	return flockPath(indexLockPath(dbPath), ErrIndexLockHeld)
}

// acquireWorkerLock takes the same kind of flock on <db>.worker.lock — the
// System2 worker-cycle counterpart of acquireIndexLock.
func acquireWorkerLock(dbPath string) (func(), error) {
	return flockPath(workerLockPath(dbPath), ErrWorkerLockHeld)
}

// flockPath takes a non-blocking exclusive flock on p. The lock is per open
// file description and is released automatically on process exit (including
// crashes), so it cannot deadlock across processes. Contention fails fast
// with the given held sentinel — callers do not queue.
func flockPath(p string, held error) (func(), error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, held
	}
	// Closing the fd drops the flock; no explicit LOCK_UN needed.
	return func() { _ = f.Close() }, nil
}
