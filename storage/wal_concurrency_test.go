package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

// Port of main:tests/integration/test_wal_concurrency.py.
//
// WAL mode lets a reader connection keep querying while a separate writer
// connection commits, with no SQLITE_BUSY / locking errors. Each store opens
// its OWN connection (like the Python test's per-thread Engine) — that is the
// WAL concurrency model: writer-connection vs reader-connection.

func openWALStore(t *testing.T, dbPath string) *SQLiteStore {
	t.Helper()
	s, err := NewStore(dbPath, 8, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestWALJournalModeEnabled(t *testing.T) {
	s := openWALStore(t, filepath.Join(t.TempDir(), "w.db"))
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q (enable_wal=true must be set before the store opens)", mode, "wal")
	}
}

func TestWALConcurrentReadWhileWriting(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "w.db")
	emb := NewHashingEmbedding(8)
	writer := openWALStore(t, dbPath)

	// Seed a memory so the reader has something to find, then open a second
	// connection for the reader (WAL must already be active — see Python's
	// Task 4.2 correction).
	seed := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	seed.Content = "a fact to consolidate"
	seed.Workspace = "w"
	vec, err := emb.Embed(seed.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.PutMemory(seed, vec); err != nil {
		t.Fatal(err)
	}
	reader := openWALStore(t, dbPath)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() { // writer connection: keep committing new memories
		defer wg.Done()
		for i := 0; i < 30; i++ {
			m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
			m.Content = fmt.Sprintf("concurrent episode %d", i)
			m.Workspace = "w"
			vec, err := emb.Embed(m.Content)
			if err != nil {
				errCh <- err
				return
			}
			if err := writer.PutMemory(m, vec); err != nil {
				errCh <- err
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() { // reader connection: keep recalling while the writer works
		defer wg.Done()
		for i := 0; i < 20; i++ {
			if _, err := reader.IterMemories("w", "", ""); err != nil {
				errCh <- err
				return
			}
			qvec, err := emb.Embed("fact")
			if err != nil {
				errCh <- err
				return
			}
			reader.vectorIndex.Search(qvec, 8)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent WAL access failed (want no sqlite locking errors): %v", err)
	}
}
