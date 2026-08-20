package storage

// Regression test for the AfterConnect CREATE EXTENSION race: N processes
// (here: goroutines) cold-starting stores against the same fresh database must
// all succeed — before the advisory lock around CREATE EXTENSION, concurrent
// NewPostgresStore calls intermittently failed with pg_extension_name_index
// 23505 (SQLSTATE duplicate key) even with IF NOT EXISTS.
// Gated on LADYM_TEST_PG_DSN; needs a reachable PostgreSQL with pgvector.

import (
	"os"
	"sync"
	"testing"
)

func TestNewPostgresStoreConcurrentColdStart(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	fresh := freshPGDatabase(t, dsn)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	stores := make(chan *PostgresStore, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := NewPostgresStore(fresh, suiteDim)
			if err != nil {
				errs <- err
				return
			}
			stores <- s
		}()
	}
	wg.Wait()
	close(errs)
	close(stores)
	for s := range stores {
		s.Close()
	}
	for err := range errs {
		t.Errorf("concurrent NewPostgresStore on a fresh database: %v", err)
	}
}
