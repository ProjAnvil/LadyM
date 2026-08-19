package code

import "fmt"

// IndexInProgressError is returned when another process already holds the
// index lock for a database (mirrors Python's IndexInProgressError). CLI/MCP
// surface the message verbatim instead of dumping a traceback. The lock
// itself (flock on <db>.index.lock) lives in the storage package behind
// Store.TryAcquireIndexLock; this package only translates
// storage.ErrIndexLockHeld into this user-facing type.
type IndexInProgressError struct {
	DBPath string
}

func (e *IndexInProgressError) Error() string {
	return fmt.Sprintf("code indexing is already running for %s; wait for it to finish before starting another", e.DBPath)
}
