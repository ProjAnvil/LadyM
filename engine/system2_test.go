//go:build !enterprise

package engine

import (
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/config"
)

// Port of the second half of main:tests/integration/test_wal_concurrency.py.
//
// StartSystem2 runs cycles in a daemon goroutine backed by its own engine
// (its own SQLite connection, forced into WAL). Starts the loop, lets it tick
// at least once, asks it to stop, and asserts the foreground engine is still
// healthy afterwards.
func TestStartSystem2TicksAndStopsCleanly(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EnableWAL = true // main + worker engine share the db file
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	if _, err := eng.RecordEvent("bot", "x", "seed episode", "", nil, nil); err != nil {
		t.Fatal(err)
	}

	stop := eng.StartSystem2(0, cfg.Workspace)
	// Give the daemon goroutine time to tick at least once.
	time.Sleep(200 * time.Millisecond)
	close(stop)
	// Brief grace period — the worker exits after its current cycle.
	time.Sleep(100 * time.Millisecond)

	// The foreground engine must still read fine after the worker ran
	// alongside it on the same db file.
	if _, err := eng.Recall("seed", "", 5, nil, nil, 0); err != nil {
		t.Fatalf("recall after system2 worker stopped: %v", err)
	}
}
