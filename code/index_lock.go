package code

import "fmt"

// IndexInProgressError is returned when another process already holds the
// index lock for a database (mirrors Python's IndexInProgressError). CLI/MCP
// surface the message verbatim instead of dumping a traceback.
type IndexInProgressError struct {
	DBPath string
}

func (e *IndexInProgressError) Error() string {
	return fmt.Sprintf("code indexing is already running for %s; wait for it to finish before starting another", e.DBPath)
}

// indexLockPath names the lock file <db>.index.lock — identical to the Python
// implementation so Go and Python processes exclude each other.
func indexLockPath(dbPath string) string { return dbPath + ".index.lock" }
