//go:build !enterprise

// Tests for the System2 worker loop's lock handling: a held worker lock makes
// a standby replica skip the cycle (not fail), and a lock acquisition error
// propagates in --once mode. Also the workerCmd wiring around
// startWorkerMetrics failures.

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A second engine holding the worker lock makes runWorkerLoop skip the cycle
// and, in once mode, return nil (standby is not a failure).
func TestRunWorkerLoop_LockHeld_SkipsCycleOnce(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")

	holder, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	release, err := holder.Store.TryAcquireWorkerLock()
	if err != nil {
		t.Fatalf("holder acquire worker lock: %v", err)
	}
	defer release()

	standby, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer standby.Close()

	if err := runWorkerLoop(standby, true, 0, ""); err != nil {
		t.Errorf("runWorkerLoop with held lock: err = %v, want nil (skip is not a failure)", err)
	}
}

// When the worker lock cannot even be acquired (here: the lock file's parent
// directory was removed underneath the engine), once mode propagates the
// error for a non-zero exit.
func TestRunWorkerLoop_LockError_FailsOnce(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "ladym.db")
	setGlobalConfigPath(t, "")
	isolateEnv(t)

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// The sqlite handle survives unlinking, but the worker lock file cannot be
	// created in a gone directory — flockPath fails with the OS error.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if err := runWorkerLoop(eng, true, 0, ""); err == nil {
		t.Error("runWorkerLoop with un-acquirable lock: err = nil, want the lock error")
	}
}

// `ladym worker --metrics-addr` with an un-listenable address fails before
// entering the worker loop.
func TestWorkerCmd_MetricsAddrInvalid_Fails(t *testing.T) {
	db := isolateEnv(t)
	setGlobalConfigPath(t, "")

	_, err := runCmd(t, workerCmd(), "--db", db, "--once", "--metrics-addr", "no-port-here")
	if err == nil {
		t.Fatal("worker with invalid --metrics-addr should fail")
	}
	if !strings.Contains(err.Error(), "listen") && !strings.Contains(err.Error(), "address") && !strings.Contains(err.Error(), "port") {
		t.Errorf("error = %q, want a listen/address error", err.Error())
	}
}
