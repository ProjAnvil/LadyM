//go:build enterprise

package storage

import "testing"

func migrationBackends(t *testing.T, fn func(*testing.T, Store)) {
	t.Helper()
	runStoreBackends(t, fn)
}
