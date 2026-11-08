package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMaintenanceTopicRefresh(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		scope := e.Scope("jobs")
		var sources []*schema.Memory
		var topics []*schema.Memory
		for _, text := range []string{"alpha", "beta"} {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.Workspace, m.Content = "jobs", text
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			topic, err := scope.BuildTopic(text, []string{m.ID}, 0)
			if err != nil {
				t.Fatal(err)
			}
			sources, topics = append(sources, m), append(topics, topic)
		}
		if err := e.Store.UpdateMemoryContent(sources[0].ID, "changed alpha", "", nil, nil, schema.Now()); err != nil {
			t.Fatal(err)
		}
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || report.CompletedJobs != 1 || report.Calls != 0 {
			t.Fatalf("refresh %+v %v", report, err)
		}
		a, _ := scope.GetTopic("alpha")
		b, _ := scope.GetTopic("beta")
		if !strings.Contains(a.Content, "changed alpha") || schema.ManagedRevision(a) <= schema.ManagedRevision(topics[0]) || schema.ManagedRevision(b) != schema.ManagedRevision(topics[1]) {
			t.Fatal("refresh ignored explicit input or touched unrelated wiki")
		}
		if err := e.Store.DeleteMemory(sources[0].ID); err != nil {
			t.Fatal(err)
		}
		a, _ = scope.GetTopic("alpha")
		if a.Content != "" {
			t.Fatal("explicit deletion not synchronous")
		}
		if _, err := e.RunBudgetedCycle(t.Context(), "jobs"); err != nil {
			t.Fatal(err)
		}
		a, _ = scope.GetTopic("alpha")
		if a.Content != "" {
			t.Fatal("refresh resurrected deleted evidence")
		}
	})
}

func TestMaintenanceExpiredClaim(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		scope := e.Scope("jobs")
		evidence, err := scope.RecordEvidence("test_result", "passed", "success", "host")
		if err != nil {
			t.Fatal(err)
		}
		ref := schema.EvidenceRef{MemoryID: evidence.ID, Fingerprint: schema.KnowledgeFingerprint(evidence), Kind: "test_result"}
		claim, err := scope.CreateClaim("caller-attested result", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "", schema.Now()+0.5)
		if err != nil {
			t.Fatal(err)
		}
		claim, err = scope.ApplyClaimFeedback(claim.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(550 * time.Millisecond)
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || report.CompletedJobs != 1 || report.Calls != 0 {
			t.Fatalf("expiry %+v %v", report, err)
		}
		updated, err := scope.GetClaim(claim.Memory.ID)
		if err != nil || updated.Status != "stale" {
			t.Fatalf("expired claim %+v %v", updated, err)
		}
	})
}

func TestMaintenanceMigrationGeneration(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		m := maintenanceEpisode(t, e, "event")
		provider := &maintenanceLLM{fn: func(ctx context.Context, messages []providers.Message, sc providers.JSONSchema, options providers.CallOptions) (providers.StructuredResult, error) {
			_, err := e.Store.BeginEmbeddingMigrationGeneration(schema.EmbeddingIdentity{Provider: "test", Model: "next", Dim: e.Provider.Dim(), Tokenizer: "v1"})
			if err != nil {
				return providers.StructuredResult{}, err
			}
			return providers.StructuredResult{Data: map[string]any{"action": "ADD", "target_id": "", "evidence_ids": []string{m.ID}, "new_text": "late"}, RawOutputBytes: 100}, nil
		}}
		e.routing = &adapter.ModelRouting{Consolidate: provider}
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if !errors.Is(err, storage.ErrEmbeddingMigration) || report.CompletedJobs != 0 {
			t.Fatalf("generation fence %+v %v", report, err)
		}
		jobs, _ := e.Scope("jobs").ListMaintenanceJobs(100)
		if output, _ := e.Store.GetMemory("result-" + jobs[0].ID); output != nil {
			t.Fatal("old generation published")
		}
	})
}

func TestMaintenanceDefaultLimits(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		p := &maintenanceLLM{}
		e.routing = &adapter.ModelRouting{Consolidate: p}
		e.Config.System2.MaxReservedBytes = 100000
		e.Config.System2.Concurrency = 4
		for range 55 {
			maintenanceEpisode(t, e, "event")
		}
		report, err := e.RunBudgetedCycle(t.Context(), "jobs")
		if err != nil || report.CandidateCount != 50 || report.Calls != 8 || p.calls.Load() != 8 || report.PendingJobs == 0 {
			t.Fatalf("default limits %+v %v", report, err)
		}
	})
}
