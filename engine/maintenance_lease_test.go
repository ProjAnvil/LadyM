package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMaintenanceLeaseNoTakeover(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		m := maintenanceEpisode(t, e, "event")
		p := &maintenanceLLM{fn: func(ctx context.Context, messages []providers.Message, sc providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
			time.Sleep(150 * time.Millisecond)
			return providers.StructuredResult{Data: map[string]any{"target_id": "", "action": "ADD", "evidence_ids": []string{m.ID}, "new_text": "inferred"}, RawOutputBytes: 100}, nil
		}}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		e.Config.System2.LeaseS = 0.1
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if !errors.Is(err, storage.ErrMaintenanceLease) || report.CompletedJobs != 0 {
			t.Fatalf("late completion %+v %v", report, err)
		}
		jobs, _ := e.Scope("jobs").ListMaintenanceJobs(100)
		if jobs[0].State != "running" || jobs[0].Attempt != 1 {
			t.Fatalf("expired job not recoverable %+v", jobs[0])
		}
		e.Config.System2.LeaseS = 60
		report, err = e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || report.CompletedJobs != 1 || p.calls.Load() != 2 {
			t.Fatalf("takeover %+v %v", report, err)
		}
	})
}
