//go:build enterprise

package storage

import (
	"os"
	"testing"
)

func migrationBackends(t *testing.T, fn func(*testing.T, Store)) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fn(t, s)
}
