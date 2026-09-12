package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/observability"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/storage"
)

// runWorkerLoop runs System2 cycles. In --once mode errors propagate (non-zero
// exit); in loop mode failures are logged and the loop continues. Each cycle
// first takes the cross-process worker lock so redundant replicas never run a
// cycle twice; a held lock means this replica is standby and skips the cycle
// (not a failure). Every outcome increments ladym_system2_cycles_total.
func runWorkerLoop(eng *engine.Engine, once bool, interval int, workspace string) error {
	for {
		release, err := eng.Store.TryAcquireWorkerLock()
		switch {
		case errors.Is(err, storage.ErrWorkerLockHeld):
			observability.Default().IncSystem2Cycle("skipped")
			fmt.Fprintln(os.Stderr, "system2 cycle skipped: another worker holds the lock")
			if once {
				return nil
			}
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		case err != nil:
			observability.Default().IncSystem2Cycle("failed")
			if once {
				return err
			}
			fmt.Fprintf(os.Stderr, "system2 worker lock failed; continuing: %v\n", err)
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}
		_, err = operations.RunSystem2Cycle(eng, workspace)
		release()
		if err != nil {
			observability.Default().IncSystem2Cycle("failed")
		} else {
			observability.Default().IncSystem2Cycle("ran")
		}
		if once {
			return err
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "system2 CLI worker cycle failed; continuing: %v\n", err)
		}
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

// workerMetricsHandler serves just /metrics (Prometheus exposition of the
// global registry) and /healthz (store ping) for `ladym worker
// --metrics-addr`.
func workerMetricsHandler(eng *engine.Engine) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		observability.Default().Render(w)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := eng.Store.Ping(); err != nil {
			http.Error(w, "store ping failed", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	return mux
}

// startWorkerMetrics serves workerMetricsHandler on addr (e.g. ":9090") in the
// background; the returned closer shuts it down (called when the worker
// exits). An empty addr is a no-op.
func startWorkerMetrics(eng *engine.Engine, addr string) (func(), error) {
	if addr == "" {
		return func() {}, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: workerMetricsHandler(eng)}
	go func() { _ = srv.Serve(ln) }()
	fmt.Fprintf(os.Stderr, "worker metrics listening on %s\n", ln.Addr())
	return func() { _ = srv.Close() }, nil
}
