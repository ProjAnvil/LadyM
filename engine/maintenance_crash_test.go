package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMaintenanceCrashAttempts(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		m := maintenanceEpisode(t, e, "event")
		jobMemory, err := e.Scope("jobs").ScheduleMaintenance("new_episode", m.ID, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		cfg, _ := maintenanceConfig(e.Config.System2)
		for attempt := 1; attempt <= 3; attempt++ {
			claimed, job, err := claimMaintenance(e.Store, jobMemory, cfg)
			if err != nil || job == nil || job.Attempt != attempt {
				t.Fatalf("attempt %d %+v %v", attempt, job, err)
			}
			expired := claimed.Clone()
			job.LeaseUntil = schema.Now() - 1
			job.SetMemory(expired)
			if err := e.Store.ApplyMemoryBatch(storage.MemoryBatch{Guards: []storage.MemoryGuard{{Expected: claimed}}, Writes: []storage.MemoryWrite{{Memory: expired}}}); err != nil {
				t.Fatal(err)
			}
			jobMemory = expired
		}
		if _, err := e.RunBudgetedCycle(t.Context(), "jobs"); err != nil {
			t.Fatal(err)
		}
		current, _ := e.Store.GetMemory(jobMemory.ID)
		job, err := schema.MaintenanceJobFromMemory(current)
		if err != nil || job.State != "failed" || job.Attempt != 3 || job.ErrorCode != "attempts_exhausted" {
			t.Fatalf("exhausted %+v %v", job, err)
		}
		if _, err := e.RunBudgetedCycle(t.Context(), "jobs"); err != nil {
			t.Fatal(err)
		}
	})
}
