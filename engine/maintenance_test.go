package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type maintenanceLLM struct {
	providers.FakeLLMProvider
	fn    func(context.Context, []providers.Message, providers.JSONSchema, providers.CallOptions) (providers.StructuredResult, error)
	calls atomic.Int32
}

func (p *maintenanceLLM) CompleteStructuredContext(ctx context.Context, messages []providers.Message, sc providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
	p.calls.Add(1)
	if p.fn != nil {
		return p.fn(ctx, messages, sc, options)
	}
	var inputs map[string]*schema.Memory
	if err := json.Unmarshal([]byte(messages[1].Content), &inputs); err != nil {
		return providers.StructuredResult{}, err
	}
	var id string
	for key, m := range inputs {
		if m.Type == schema.TypeEvent {
			id = key
		}
	}
	return providers.StructuredResult{Data: map[string]any{"target_id": "", "action": "ADD", "evidence_ids": []string{id}, "new_text": "inferred output"}, RawOutputBytes: 123}, nil
}

func maintenanceEpisode(t *testing.T, e *Engine, text string) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m.Workspace, m.Content, m.ContentHash = "jobs", text, schema.ContentHash(text)
	if err := e.Store.PutMemory(m, nil); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMaintenanceDedupAndLease(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("jobs")
		m := maintenanceEpisode(t, e, "completed work")
		source := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		source.Workspace, source.Content = "jobs", "source"
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		a, err := s.ScheduleMaintenance("new_episode", m.ID, []string{source.ID, m.ID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.ScheduleMaintenance("new_episode", m.ID, []string{m.ID, source.ID}, map[string]any{})
		if err != nil || a.ID != b.ID {
			t.Fatalf("canonical dedup %v %v", b, err)
		}
		c, err := s.ScheduleMaintenance("new_episode", m.ID, []string{m.ID, source.ID}, map[string]any{"mode": "literal"})
		if err != nil || c.ID == a.ID {
			t.Fatalf("args identity %v %v", c, err)
		}
		cfg, _ := maintenanceConfig(e.Config.System2)
		var wins atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, j, err := claimMaintenance(e.Store, a, cfg)
				if err != nil {
					t.Error(err)
				}
				if j != nil {
					wins.Add(1)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("lease winners %d", wins.Load())
		}
		running, _ := e.Store.GetMemory(a.ID)
		job, _ := schema.MaintenanceJobFromMemory(running)
		oldOwner := job.Owner
		expired := running.Clone()
		job.LeaseUntil = schema.Now() - 1
		job.SetMemory(expired)
		if err := e.Store.ApplyMemoryBatch(storage.MemoryBatch{Guards: []storage.MemoryGuard{{Expected: running}}, Writes: []storage.MemoryWrite{{Memory: expired}}}); err != nil {
			t.Fatal(err)
		}
		_, takeover, err := claimMaintenance(e.Store, expired, cfg)
		if err != nil || takeover == nil || takeover.Attempt != 2 || takeover.Owner == oldOwner {
			t.Fatalf("takeover %+v %v", takeover, err)
		}
		if _, err := s.HeartbeatMaintenance(t.Context(), a.ID, oldOwner, job.Revision); !errors.Is(err, storage.ErrMemoryConflict) {
			t.Fatalf("late owner heartbeat %v", err)
		}
		_, claim, ref := claimFixture(t, s)
		before, err := s.ScheduleMaintenance("new_episode", m.ID, []string{claim.Memory.ID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := s.ApplyClaimFeedback(claim.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.ScheduleMaintenance("new_episode", m.ID, []string{claim.Memory.ID}, nil)
		if err != nil || before.ID == after.ID {
			t.Fatalf("revision/fingerprint identity did not change: %v", err)
		}
		persisted, err := schema.MaintenanceJobFromMemory(after)
		if err != nil || persisted.InputRevisions[claim.Memory.ID] != updated.Revision {
			t.Fatalf("input revision not persisted: %+v %v", persisted, err)
		}
	})
}

func TestMaintenanceUnchangedInputs(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		p := &maintenanceLLM{}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		maintenanceEpisode(t, e, "completed work")
		first, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || first.CompletedJobs != 1 || first.Calls != 1 {
			t.Fatalf("first %+v %v", first, err)
		}
		second, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || second.Calls != 0 || p.calls.Load() != 1 {
			t.Fatalf("repeat %+v %v calls=%d", second, err, p.calls.Load())
		}
		status, err := e.Scope("jobs").MaintenanceStatus()
		if err != nil || status.Unit != "bytes" || status.Calls != 0 {
			t.Fatalf("persistent report %+v %v", status, err)
		}
		jobs, err := e.Scope("jobs").ListMaintenanceJobs(100)
		if err != nil || len(jobs) != 1 || jobs[0].State != "done" || jobs[0].Checkpoint == nil {
			t.Fatalf("checkpoint %+v %v", jobs, err)
		}
		repeated, err := e.Scope("jobs").ScheduleMaintenance(jobs[0].Operation, jobs[0].TargetID, jobs[0].InputIDs, jobs[0].Args)
		if err != nil || repeated.ID != jobs[0].ID {
			t.Fatalf("accounting stamp broke explicit dedup %v %v", repeated, err)
		}
		_, err = operations.Decay(e.Store, "jobs", nil, 1, 0.1, schema.Now()+10000000, false)
		if err != nil {
			t.Fatal(err)
		}
		if m, _ := e.Store.GetMemory(jobs[0].ID); m == nil {
			t.Fatal("done dedup decayed")
		}
		response, err := operations.HybridRecall(e.Store, e.Provider, "job", "jobs", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, result := range response.Results {
			if result.Memory.Type == schema.TypeMaintenanceJob {
				t.Fatal("job recalled")
			}
		}
	})
}

func TestMaintenanceBudgets(t *testing.T) {
	for _, test := range []struct {
		name                     string
		calls, candidates, bytes int
		want                     string
	}{
		{"calls", 2, 50, 100000, "calls"},
		{"candidates", 8, 2, 100000, "candidates"},
		{"reserved", 8, 50, 1024, "reserved_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runClaimBackends(t, func(t *testing.T, e *Engine) {
				p := &maintenanceLLM{}
				e.routing = &adapter.ModelRouting{Consolidate: p}
				e.Config.System2.MaxCalls, e.Config.System2.MaxCandidates, e.Config.System2.MaxReservedBytes, e.Config.System2.Concurrency = test.calls, test.candidates, test.bytes, 4
				for range 12 {
					maintenanceEpisode(t, e, "event")
				}
				report, err := e.RunBudgetedCycle(t.Context(), "jobs")
				if err != nil || report.StopReason != test.want || report.Calls > test.calls || report.ReservedBytes > test.bytes || report.CandidateCount > test.candidates || report.Unit != "bytes" || report.PendingJobs == 0 {
					t.Fatalf("budget %+v %v", report, err)
				}
			})
		})
	}
}

func TestMaintenanceOutputAndRetries(t *testing.T) {
	for _, kind := range []string{"oversized", "provider_error", "unsupported"} {
		t.Run(kind, func(t *testing.T) {
			runClaimBackends(t, func(t *testing.T, e *Engine) {
				p := &maintenanceLLM{fn: func(ctx context.Context, m []providers.Message, s providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
					if kind == "provider_error" {
						return providers.StructuredResult{}, errors.New("failure")
					}
					return providers.StructuredResult{Data: map[string]any{"action": "ADD"}, RawOutputBytes: 1025}, nil
				}}
				e.routing = &adapter.ModelRouting{Consolidate: p}
				if kind == "unsupported" {
					e.routing.Consolidate = &providers.FakeLLMProvider{}
				}
				m := maintenanceEpisode(t, e, "event")
				for range 4 {
					report, err := e.RunBudgetedCycle(t.Context(), "jobs")
					if report == nil {
						t.Fatal(err)
					}
				}
				jobs, err := e.Scope("jobs").ListMaintenanceJobs(100)
				if err != nil || len(jobs) != 1 || jobs[0].State != "failed" || jobs[0].Attempt != 3 || p.calls.Load() > 3 {
					t.Fatalf("retry %+v %v", jobs, err)
				}
				if output, _ := e.Store.GetMemory("result-" + jobs[0].ID); output != nil {
					t.Fatal("failed output published")
				}
				input, _ := e.Store.GetMemory(m.ID)
				if input.Meta("maintenance_input_stamp") != nil {
					t.Fatal("failed checkpoint published")
				}
			})
		})
	}
}

func TestMaintenanceCancellation(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		cancelled := make(chan struct{})
		p := &maintenanceLLM{fn: func(ctx context.Context, m []providers.Message, s providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
			<-ctx.Done()
			close(cancelled)
			return providers.StructuredResult{}, ctx.Err()
		}}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		e.Config.System2.MaxWallS = 0.3
		m := maintenanceEpisode(t, e, "event")
		start := time.Now()
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second || report.StopReason != "wall_or_cancel" {
			t.Fatalf("wall %+v %v", report, err)
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("provider did not observe cancellation")
		}
		current, _ := e.Store.GetMemory(m.ID)
		if current.Meta("maintenance_input_stamp") != nil {
			t.Fatal("cancelled publish")
		}
	})
}

func TestMaintenanceSourceChangedDuringCall(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		m := maintenanceEpisode(t, e, "before")
		p := &maintenanceLLM{}
		p.fn = func(ctx context.Context, messages []providers.Message, sc providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
			if err := e.Store.UpdateMemoryContent(m.ID, "after", "", nil, nil, schema.Now()); err != nil {
				return providers.StructuredResult{}, err
			}
			return providers.StructuredResult{Data: map[string]any{"target_id": "", "action": "ADD", "evidence_ids": []string{m.ID}, "new_text": "stale"}, RawOutputBytes: 100}, nil
		}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if !errors.Is(err, storage.ErrMemoryConflict) || report.CompletedJobs != 0 {
			t.Fatalf("changed %+v %v", report, err)
		}
		jobs, _ := e.Scope("jobs").ListMaintenanceJobs(100)
		if output, _ := e.Store.GetMemory("result-" + jobs[0].ID); output != nil {
			t.Fatal("changed source output published")
		}
		if jobs[0].State != "superseded" {
			t.Fatalf("state %s", jobs[0].State)
		}
	})
}

func TestRichExtractionTarget(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		episode := maintenanceEpisode(t, e, "event")
		other := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		other.Workspace, other.Content = "jobs", "original"
		inputs := map[string]*schema.Memory{episode.ID: episode, other.ID: other}
		job := &schema.MaintenanceJob{TargetID: episode.ID, Workspace: "jobs"}
		for _, tc := range []struct {
			name, target, action, status string
			good                         bool
		}{
			{"explicit", other.ID, "UPDATE", "", true}, {"missing", "", "UPDATE", "", false},
			{"foreign", "unknown", "UPDATE", "", false}, {"verified", other.ID, "UPDATE", "verified", false},
			{"add", "", "ADD", "", true}, {"wrong_add", other.ID, "ADD", "", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				other.Metadata["knowledge_status"] = tc.status
				err := validateRichExtraction(RichExtraction{TargetID: tc.target, Action: tc.action, EvidenceIDs: []string{episode.ID}, NewText: "inferred"}, job, inputs)
				if (err == nil) != tc.good {
					t.Fatalf("guard %v", err)
				}
			})
		}
	})
}

func TestMaintenanceWorkerShutdownRouting(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		started := make(chan struct{})
		p := &maintenanceLLM{fn: func(ctx context.Context, m []providers.Message, s providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
			close(started)
			<-ctx.Done()
			return providers.StructuredResult{}, ctx.Err()
		}}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		e.Config.System2.BudgetedEnabled = true
		maintenanceEpisode(t, e, "host routing")
		stop := e.StartSystem2(1, "jobs")
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("host route not called")
		}
		close(stop)
		e.workerWG.Wait()
		if p.calls.Load() != 1 {
			t.Fatal("routing lost or worker not joined")
		}
	})
}

func TestMaintenanceFullReservation(t *testing.T) {
	b := &providers.Budget{MaxCalls: 8, MaxReservedBytes: 8192}
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { b.Reserve(300, 1024) })
	}
	wg.Wait()
	if b.Calls != 6 || b.ReservedBytes != 6*1324 {
		t.Fatalf("atomic full reservation %+v", b)
	}
	n, err := providers.SerializedInputBytes([]providers.Message{{Role: "user", Content: strings.Repeat("界", 5)}}, richExtractionSchema)
	if err != nil || n < 15 {
		t.Fatal("byte input accounting")
	}
}
