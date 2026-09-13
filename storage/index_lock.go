package storage

import "errors"

// ErrIndexLockHeld is returned by TryAcquireIndexLock when another process
// already holds the code-index lock for this store's database. The
// user-facing IndexInProgressError type lives in the code package (code
// imports storage, so the type cannot live here); callers translate
// ErrIndexLockHeld into it.
var ErrIndexLockHeld = errors.New("code indexing is already running")

// ErrWorkerLockHeld is returned by TryAcquireWorkerLock when another process
// already holds the System2 worker lock for this store's database. Callers
// treat it as "standby": skip the cycle, do not report a failure.
var ErrWorkerLockHeld = errors.New("a system2 worker cycle is already running")
