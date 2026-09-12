//go:build !enterprise

package engine

// StartSystem2 worker-lock branches: a held lock means standby (skip the
// cycle without counting a failure); a genuine lock error counts towards
// MaxConsecutiveErrors and stops the worker.

import (
	"os"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/observability"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(deadline time.Duration, cond func() bool) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

func TestStartSystem2_WorkerLockHeld_SkipsCycle(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EnableWAL = true // main + worker engine share the db file
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	// Hold the worker lock from the foreground engine: the worker replica must
	// observe ErrWorkerLockHeld and skip its cycles (standby), never failing.
	release, err := eng.Store.TryAcquireWorkerLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	base := observability.Default().System2Cycles()
	stop := eng.StartSystem2(1, cfg.Workspace)
	defer close(stop)

	if !waitFor(5*time.Second, func() bool {
		return observability.Default().System2Cycles()["skipped"] > base["skipped"]
	}) {
		t.Fatal("worker never skipped a cycle while the lock was held")
	}
	if got := observability.Default().System2Cycles()["failed"]; got != base["failed"] {
		t.Errorf("failed cycles = %d, want unchanged %d (lock-held is standby, not failure)", got, base["failed"])
	}
}

func TestStartSystem2_WorkerLockError_StopsAfterMaxConsecutiveErrors(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EnableWAL = true
	cfg.System2.MaxConsecutiveErrors = 2
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	// A directory at the worker-lock path makes flock's OpenFile fail with a
	// genuine error (not ErrWorkerLockHeld) on every cycle.
	if err := os.Mkdir(cfg.DBPath+".worker.lock", 0o755); err != nil {
		t.Fatal(err)
	}

	base := observability.Default().System2Cycles()["failed"]
	stop := eng.StartSystem2(1, cfg.Workspace)
	defer close(stop)

	if !waitFor(5*time.Second, func() bool {
		return observability.Default().System2Cycles()["failed"] >= base+2
	}) {
		t.Fatal("worker never hit the lock-error branch")
	}
	// The worker must give up after MaxConsecutiveErrors: the failed count
	// stops growing even though the interval keeps elapsing.
	time.Sleep(1500 * time.Millisecond)
	if got := observability.Default().System2Cycles()["failed"]; got != base+2 {
		t.Errorf("failed cycles = %d, want exactly %d (worker should stop after max consecutive errors)", got, base+2)
	}
}
