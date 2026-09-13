//go:build windows && !enterprise

package storage

import (
	"os"
	"sync"
)

// Windows has no flock(2); fall back to a process-local set of held locks.
// NOTE: cross-process exclusion (Go vs Python, or two ladym processes) is NOT
// provided on Windows — LadyM targets macOS/Linux. Contention within one
// process still fails fast with ErrIndexLockHeld.
var (
	indexLocksMu sync.Mutex
	indexLocks   = map[string]bool{}
)

func acquireIndexLock(dbPath string) (func(), error) {
	return acquirePathLock(indexLockPath(dbPath), ErrIndexLockHeld)
}

func acquireWorkerLock(dbPath string) (func(), error) {
	return acquirePathLock(workerLockPath(dbPath), ErrWorkerLockHeld)
}

// acquirePathLock records p in the process-local held set (paths differ per
// lock kind, so one set serves both) and still creates the lock file for name
// parity with the Unix/Python path.
func acquirePathLock(p string, held error) (func(), error) {
	indexLocksMu.Lock()
	if indexLocks[p] {
		indexLocksMu.Unlock()
		return nil, held
	}
	indexLocks[p] = true
	indexLocksMu.Unlock()

	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		indexLocksMu.Lock()
		delete(indexLocks, p)
		indexLocksMu.Unlock()
		return nil, err
	}
	return func() {
		_ = f.Close()
		indexLocksMu.Lock()
		delete(indexLocks, p)
		indexLocksMu.Unlock()
	}, nil
}
