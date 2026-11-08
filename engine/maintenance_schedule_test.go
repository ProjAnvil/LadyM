package engine

import (
	"errors"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type schedulingConflictStore struct {
	storage.Store
	once   sync.Once
	change func()
}

func (s *schedulingConflictStore) ApplyMemoryBatch(batch storage.MemoryBatch) error {
	s.once.Do(s.change)
	return s.Store.ApplyMemoryBatch(batch)
}

func TestMaintenanceScheduleConflict(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		m := maintenanceEpisode(t, e, "before")
		base := e.Store
		e.Store = &schedulingConflictStore{Store: base, change: func() {
			if err := base.UpdateMemoryContent(m.ID, "after", "", nil, nil, schema.Now()); err != nil {
				t.Fatal(err)
			}
		}}
		job, err := e.Scope("jobs").ScheduleMaintenance("new_episode", m.ID, nil, nil)
		if !errors.Is(err, storage.ErrMemoryConflict) || job != nil {
			t.Fatalf("schedule returned empty success: %v %v", job, err)
		}
	})
}

func TestMaintenanceMaximumTopicInputs(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		var ids []string
		for range 64 {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.Workspace, m.Content = "jobs", "source"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, m.ID)
		}
		topic, err := e.Scope("jobs").BuildTopic("maximum", ids, 0)
		if err != nil {
			t.Fatal(err)
		}
		job, err := e.Scope("jobs").ScheduleMaintenance("refresh_topic", topic.ID, ids, nil)
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := schema.MaintenanceJobFromMemory(job)
		if err != nil || len(persisted.InputIDs) != 65 {
			t.Fatalf("target snapshot displaced valid topic input: %+v %v", persisted, err)
		}
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || report.CompletedJobs != 1 || report.Calls != 0 {
			t.Fatalf("maximum input refresh: %+v %v", report, err)
		}
	})
}
