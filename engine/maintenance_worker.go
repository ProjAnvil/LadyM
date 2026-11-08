package engine

import (
	"cmp"
	"context"
	"time"
)

func (e *Engine) startBudgetedWorker(intervalS int, workspace string) chan struct{} {
	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		cancel()
		return stop
	}
	e.workerCancels = append(e.workerCancels, cancel)
	e.workerWG.Go(func() {
		defer cancel()
		// A stop watcher owns no I/O and is joined before worker shutdown.
		watcherDone := make(chan struct{})
		go func() {
			defer close(watcherDone)
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		defer func() { cancel(); <-watcherDone }()
		interval := time.Duration(cmp.Or(intervalS, e.Config.System2.IntervalS)) * time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			_, _ = e.RunBudgetedCycle(ctx, workspace)
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	})
	e.mu.Unlock()
	return stop
}
