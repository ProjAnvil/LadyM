package storage

import "errors"

// ErrIndexLockHeld is returned by TryAcquireIndexLock when another process
// already holds the code-index lock for this store's database. The
// user-facing IndexInProgressError type lives in the code package (code
// imports storage, so the type cannot live here); callers translate
// ErrIndexLockHeld into it.
var ErrIndexLockHeld = errors.New("code indexing is already running")
