package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type observation struct {
	Variant        string  `json:"variant"`
	SourceCoverage float64 `json:"source_coverage"`
	StaleUsage     float64 `json:"stale_usage"`
	ReadElapsed    float64 `json:"read_elapsed"`
	ContextUnits   int     `json:"context_units"`
	Unit           string  `json:"unit"`
}

func backends(t *testing.T, fn func(*testing.T, *config.Config)) {
	t.Helper()
	names := []string{"sqlite", "postgres"}
	if storage.Edition == "enterprise" {
		names = []string{"postgres"}
	}
	if selected := os.Getenv("LADYM_LEARNING_BACKEND"); selected != "" {
		if selected != "sqlite" && selected != "postgres" {
			t.Fatal("invalid learning backend")
		}
		if selected == "sqlite" && storage.Edition == "enterprise" {
			t.Fatal("enterprise excludes SQLite")
		}
		names = []string{selected}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = name
			if name == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("PostgreSQL requires explicit LADYM_TEST_PG_DSN; not parity evidence")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			fn(t, cfg)
		})
	}
}

func openEngine(t *testing.T, cfg *config.Config) *engine.Engine {
	t.Helper()
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func measureVariants(t *testing.T, e *engine.Engine) []observation {
	t.Helper()
	s := e.Scope("offline")
	a := exactSource(t, e, s.Workspace(), "auth.go", "func Authenticate() { verifyJWT() }")
	b := exactSource(t, e, s.Workspace(), "tokens.go", "func verifyJWT() { rejectExpiredToken() }")
	for _, m := range []*schema.Memory{a, b} {
		vector, err := e.Provider.Embed(m.Content)
		must(t, err)
		must(t, e.Store.PutMemory(m, vector))
	}
	topic, err := s.BuildTopic("authentication", []string{a.ID, b.ID}, 0)
	must(t, err)
	stale := exactSource(t, e, s.Workspace(), "old.go", "OBSOLETE authentication: allow expired tokens")
	stale.Metadata["knowledge_status"] = "rejected"
	vector, err := e.Provider.Embed(stale.Content)
	must(t, err)
	must(t, e.Store.PutMemory(stale, vector))
	roots := []string{a.ID, b.ID}
	var rows []observation
	for _, name := range []string{"stateless", "direct_source", "r1", "r2_topic"} {
		start := time.Now()
		content := ""
		var selected []*schema.Memory
		switch name {
		case "stateless":
			content = "Task: locate authentication and token validation."
		case "direct_source":
			all, err := e.List(s.Workspace(), nil, 100, 0)
			must(t, err)
			for _, m := range all {
				if slices.Contains(roots, m.ID) {
					selected = append(selected, m)
					content += m.Content + "\n"
				}
			}
		case "r1":
			response, err := e.Recall("Authenticate verifyJWT", s.Workspace(), 8, nil, nil, 0)
			must(t, err)
			for _, r := range response.Results {
				selected = append(selected, r.Memory)
				content += r.Memory.Content + "\n"
			}
		case "r2_topic":
			boot, err := s.Bootstrap(engine.BootstrapRequest{TopicIDs: []string{topic.ID}}, nil)
			must(t, err)
			current, err := s.GetTopic("authentication")
			must(t, err)
			selected = append(selected, current)
			content = boot.Content + "\n" + current.Content
		}
		elapsed := time.Since(start).Seconds()
		covered := map[string]bool{}
		staleCount := 0
		for _, m := range selected {
			fresh, err := s.IsFresh(m, schema.Now())
			must(t, err)
			if !fresh {
				staleCount++
			}
			traceSources(t, e.Store, m.ID, roots, covered, map[string]bool{})
		}
		staleUsage := 0.0
		if len(selected) > 0 {
			staleUsage = float64(staleCount) / float64(len(selected))
		}
		if strings.Contains(content, "OBSOLETE") {
			t.Fatal("rejected source text exposed by offline reader")
		}
		rows = append(rows, observation{name, float64(len(covered)) / 2, staleUsage, elapsed, len(content), "bytes"})
	}
	return rows
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exactSource(t *testing.T, e *engine.Engine, ws, path, text string) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	m.Workspace, m.Content, m.Source = ws, text, path
	m.Metadata["file_path"] = path
	must(t, e.Store.PutMemory(m, nil))
	return m
}

func traceSources(t *testing.T, store storage.Store, id string, roots []string, covered, visited map[string]bool) {
	t.Helper()
	if visited[id] {
		return
	}
	visited[id] = true
	if slices.Contains(roots, id) {
		covered[id] = true
	}
	edges, err := store.Neighbors(id, "")
	must(t, err)
	for _, edge := range edges {
		if edge.SrcID == id && edge.ValidTo == nil && (edge.Relation == "produced_from" || edge.Relation == "abstracts") {
			traceSources(t, store, edge.DstID, roots, covered, visited)
		}
	}
}

func TestTrustedMemoryVariants(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		rows := measureVariants(t, openEngine(t, cfg))
		if len(rows) != 4 {
			t.Fatalf("four real Engine observations required, got %d", len(rows))
		}
		for i, name := range []string{"stateless", "direct_source", "r1", "r2_topic"} {
			row := rows[i]
			if row.Variant != name || row.Unit != "bytes" || row.ReadElapsed < 0 || row.ContextUnits < 0 || row.SourceCoverage < 0 || row.SourceCoverage > 1 || row.StaleUsage != 0 {
				t.Fatalf("invalid offline observation: %+v", row)
			}
		}
		if rows[0].SourceCoverage != 0 || rows[1].SourceCoverage != 1 || rows[2].SourceCoverage != 1 || rows[3].SourceCoverage != 1 || rows[2].ContextUnits == 0 {
			t.Fatalf("hand-checked source coverage: %+v", rows)
		}
		if out := os.Getenv("LADYM_LEARNING_REPORT"); out != "" {
			if filepath.IsAbs(out) || filepath.Clean(out) != out || strings.HasPrefix(out, "..") {
				t.Fatal("report path must be project-relative and clean")
			}
			data, err := json.Marshal(rows)
			must(t, err)
			must(t, os.WriteFile(filepath.Join("..", out), data, 0o600))
		}
	})
}

func verifiedClaim(t *testing.T, s *engine.Scope, text string) (*schema.Memory, *schema.Claim, schema.EvidenceRef) {
	t.Helper()
	ev, err := s.RecordEvidence("test_result", "检查成功：tokens reject expired JWT", "success", "host")
	must(t, err)
	ref := schema.EvidenceRef{MemoryID: ev.ID, Fingerprint: schema.KnowledgeFingerprint(ev), Kind: "test_result", StartRune: 0, EndRune: 4}
	c, err := s.CreateClaim(text, []schema.EvidenceRef{ref}, schema.ClaimScope{Environment: "test", CodeVersion: "v1", Conditions: []string{"jwt"}}, "", 0)
	must(t, err)
	c, err = s.ApplyClaimFeedback(c.Memory.ID, engine.ClaimFeedback{ExpectedRevision: c.Revision, Action: "verify", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
	must(t, err)
	return ev, c, ref
}

func TestTrustedMemoryCorrectionRepeatedTask(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		e := openEngine(t, cfg)
		s := e.Scope("repeat")
		_, c, ref := verifiedClaim(t, s, "OLD_CLAIM tokens expire after 24h")
		code := exactSource(t, e, s.Workspace(), "tokens.go", "func verifyJWT() { rejectExpiredToken() }")
		topic, err := s.BuildTopic("authentication", []string{c.Memory.ID, code.ID}, 0)
		must(t, err)
		task, err := s.SetTaskState("review", "review authentication", "inspect tokens.go", 86400)
		must(t, err)
		req := engine.BootstrapRequest{Environment: "test", CodeVersion: "v1", Conditions: []string{"jwt"}, TopicIDs: []string{topic.ID}, TaskID: "review"}
		first, err := s.Bootstrap(req, nil)
		must(t, err)
		if !slices.Contains(first.SourceIDs, c.Memory.ID) || !slices.Contains(first.SourceIDs, task.ID) {
			t.Fatalf("claim/task missing from bootstrap: %+v", first)
		}
		corrected, err := s.ApplyClaimFeedback(c.Memory.ID, engine.ClaimFeedback{ExpectedRevision: c.Revision, Action: "correct", Replacement: "CORRECTED_CLAIM tokens expire after 1h", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		must(t, err)
		if corrected.Memory.ID != c.Memory.ID || corrected.Revision != 3 {
			t.Fatal("correction did not preserve identity and increment revision")
		}
		current, err := s.GetTopic("authentication")
		must(t, err)
		if current.Content != "" {
			t.Fatal("old topic body exposed after correction")
		}
		second, err := s.Bootstrap(req, nil)
		must(t, err)
		if strings.Contains(second.Content, "OLD_CLAIM") || !strings.Contains(second.Content, "CORRECTED_CLAIM") {
			t.Fatalf("repeated task trusted obsolete claim: %q", second.Content)
		}
		req.Conditions = []string{"JWT"}
		filtered, err := s.Bootstrap(req, nil)
		must(t, err)
		if slices.Contains(filtered.SourceIDs, c.Memory.ID) {
			t.Fatal("case-insensitive caller condition bypass")
		}
		exact, err := s.HybridRecall("verifyJWT", 8)
		must(t, err)
		found := false
		for _, r := range exact.Results {
			found = found || r.Memory.ID == code.ID
		}
		if !found {
			t.Fatal("exact cross-file source without embedding missing")
		}
		report, err := e.RunBudgetedCycle(t.Context(), s.Workspace())
		must(t, err)
		if report.CompletedJobs < 1 || report.Calls != 0 {
			t.Fatalf("deterministic refresh: %+v", report)
		}
		current, err = s.GetTopic("authentication")
		must(t, err)
		if !strings.Contains(current.Content, "CORRECTED_CLAIM") || !strings.Contains(current.Content, code.ID) {
			t.Fatal("refresh lost corrected claim or cross-file source")
		}
	})
}

func TestTrustedMemoryRecursiveDelete(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		e := openEngine(t, cfg)
		s := e.Scope("delete")
		ev, c, _ := verifiedClaim(t, s, "DELETE_SECRET trusted result")
		topic, err := s.BuildTopic("result", []string{c.Memory.ID}, 0)
		must(t, err)
		task, err := s.SetTaskState("review", "DELETE_SECRET review", "next", 86400)
		must(t, err)
		task.Metadata["source_episode"] = topic.ID
		must(t, e.Store.PutMemory(task, nil))
		foreign := exactSource(t, e, "other", "other.go", "foreign preserved")
		must(t, e.Forget(ev.ID))
		raw, err := e.Store.GetMemory(ev.ID)
		must(t, err)
		if raw != nil {
			t.Fatal("explicit deletion preserved source")
		}
		for _, id := range []string{c.Memory.ID, topic.ID, task.ID} {
			raw, err := e.Store.GetMemory(id)
			must(t, err)
			fresh, err := s.IsFresh(raw, schema.Now())
			must(t, err)
			if fresh {
				t.Fatalf("recursive descendant remained fresh: %s", id)
			}
		}
		boot, err := s.Bootstrap(engine.BootstrapRequest{Environment: "test", CodeVersion: "v1", Conditions: []string{"jwt"}, TopicIDs: []string{topic.ID}, TaskID: "review"}, nil)
		must(t, err)
		if strings.Contains(boot.Content, "DELETE_SECRET") {
			t.Fatal("deleted source text escaped into bootstrap")
		}
		list, err := e.List(s.Workspace(), nil, 100, 0)
		must(t, err)
		for _, m := range list {
			if strings.Contains(m.Content, "DELETE_SECRET") {
				t.Fatal("public list leaked stale descendant")
			}
		}
		raw, err = e.Store.GetMemory(foreign.ID)
		must(t, err)
		if raw == nil || raw.Content != "foreign preserved" {
			t.Fatal("cross-workspace deletion")
		}
	})
}

func TestTrustedMemoryTransitiveExpiry(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		e := openEngine(t, cfg)
		s := e.Scope("expiry")
		ev, err := s.RecordEvidence("user_confirmation", "expiry source", "success", "host")
		must(t, err)
		ev.Metadata["valid_until"] = schema.Now() + 1
		must(t, e.Store.PutMemory(ev, nil))
		ref := schema.EvidenceRef{MemoryID: ev.ID, Fingerprint: schema.KnowledgeFingerprint(ev), Kind: "user_confirmation"}
		c, err := s.CreateClaim("EXPIRED_CLAIM", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "", 0)
		must(t, err)
		c, err = s.ApplyClaimFeedback(c.Memory.ID, engine.ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		must(t, err)
		topic, err := s.BuildTopic("expiry", []string{c.Memory.ID}, 0)
		must(t, err)
		time.Sleep(1050 * time.Millisecond)
		current, err := s.GetTopic("expiry")
		must(t, err)
		if current.Content != "" {
			t.Fatal("transitive TTL trusted before any maintenance cycle")
		}
		boot, err := s.Bootstrap(engine.BootstrapRequest{TopicIDs: []string{topic.ID}}, nil)
		must(t, err)
		if strings.Contains(boot.Content, "EXPIRED_CLAIM") || slices.Contains(boot.SourceIDs, topic.ID) {
			t.Fatal("expired provenance entered bootstrap")
		}
	})
}

func TestTrustedMemoryJobCrashBudget(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		e := openEngine(t, cfg)
		s := e.Scope("restart")
		source := exactSource(t, e, s.Workspace(), "a.go", "old code")
		topic, err := s.BuildTopic("code", []string{source.ID}, 0)
		must(t, err)
		must(t, e.Store.UpdateMemoryContent(source.ID, "updated code", "", nil, nil, schema.Now()))
		stale, err := s.GetTopic("code")
		must(t, err)
		job, err := s.ScheduleMaintenance("refresh_topic", stale.ID, []string{source.ID}, nil)
		must(t, err)
		data, err := schema.MaintenanceJobFromMemory(job)
		must(t, err)
		data.State, data.Owner, data.Attempt, data.LeaseUntil = "running", "crashed-host", 1, schema.Now()-1
		data.Revision++
		abandoned := job.Clone()
		data.SetMemory(abandoned)
		must(t, e.Store.ApplyMemoryBatch(storage.MemoryBatch{Guards: []storage.MemoryGuard{{Expected: job}}, Writes: []storage.MemoryWrite{{Memory: abandoned}}}))
		e.Close()
		reopened := openEngine(t, cfg)
		report, err := reopened.RunBudgetedCycle(t.Context(), s.Workspace())
		must(t, err)
		jobs, err := reopened.Scope(s.Workspace()).ListMaintenanceJobs(100)
		must(t, err)
		if report.CompletedJobs != 1 || report.Calls != 0 || len(jobs) != 1 || jobs[0].State != "done" || jobs[0].Attempt != 2 || jobs[0].Checkpoint == nil {
			t.Fatalf("restart did not commit atomic takeover: %+v %+v", report, jobs)
		}
		current, err := reopened.Scope(s.Workspace()).GetTopic("code")
		must(t, err)
		if !strings.Contains(current.Content, "updated code") || schema.ManagedRevision(current) <= schema.ManagedRevision(topic) {
			t.Fatal("restart did not refresh topic")
		}
		repeat, err := reopened.RunBudgetedCycle(t.Context(), s.Workspace())
		must(t, err)
		if repeat.CompletedJobs != 0 || repeat.Calls != 0 {
			t.Fatalf("unchanged work repeated: %+v", repeat)
		}
		cfg.System2.MaxCandidates = 1
		for _, name := range []string{"second", "third"} {
			_, err := reopened.Scope(s.Workspace()).BuildTopic(name, []string{source.ID}, 0)
			must(t, err)
		}
		must(t, reopened.Store.UpdateMemoryContent(source.ID, "latest code", "", nil, nil, schema.Now()))
		limited, err := reopened.RunBudgetedCycle(t.Context(), s.Workspace())
		must(t, err)
		if limited.CandidateCount != 1 || limited.PendingJobs == 0 || limited.StopReason != "candidates" || limited.Unit != "bytes" {
			t.Fatalf("candidate budget not enforced: %+v", limited)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := reopened.RunBudgetedCycle(cancelled, s.Workspace()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled cycle: %v", err)
		}
	})
}

type localEmbedding struct {
	*storage.HashingEmbedding
	identity schema.EmbeddingIdentity
}

func (p *localEmbedding) Identity() schema.EmbeddingIdentity { return p.identity }

func TestTrustedMemoryMigrationRollback(t *testing.T) {
	backends(t, func(t *testing.T, cfg *config.Config) {
		old := &localEmbedding{storage.NewHashingEmbedding(8), schema.EmbeddingIdentity{Provider: "local-fixture", Model: "old", Dim: 8, Tokenizer: "v1"}}
		e, err := engine.NewWithModels(cfg, &adapter.ModelRouting{Embedding: old})
		must(t, err)
		t.Cleanup(func() { e.Close() })
		source := exactSource(t, e, "migration", "a.go", "KEEP_SOURCE")
		vector, err := old.Embed(source.Content)
		must(t, err)
		must(t, e.Store.PutMemory(source, vector))
		deleted := exactSource(t, e, "migration", "gone.go", "DELETE_TEXT")
		must(t, e.Store.PutMemory(deleted, vector))
		next := &localEmbedding{storage.NewHashingEmbedding(8), schema.EmbeddingIdentity{Provider: "local-fixture", Model: "next", Dim: 8, Tokenizer: "v1"}}
		must(t, engine.MigrateEmbeddings(t.Context(), e.Store, next.identity, next, 1, true))
		status, err := e.Store.EmbeddingMigrationStatus()
		must(t, err)
		if status.ActiveFingerprint != next.identity.Fingerprint() || !status.RollbackAvailable {
			t.Fatalf("same-dimension model migration: %+v", status)
		}
		must(t, e.Forget(deleted.ID))
		must(t, e.Store.RollbackEmbeddingMigration())
		e.Close()
		reopened, err := engine.NewWithModels(cfg, &adapter.ModelRouting{Embedding: old})
		must(t, err)
		t.Cleanup(func() { reopened.Close() })
		got, err := reopened.Store.GetMemory(source.ID)
		must(t, err)
		gone, err := reopened.Store.GetMemory(deleted.ID)
		must(t, err)
		restored, err := reopened.Store.GetEmbedding(source.ID)
		must(t, err)
		if got == nil || got.Content != "KEEP_SOURCE" || gone != nil || !slices.Equal(restored, vector) {
			t.Fatal("rollback changed source text, lost vectors or resurrected deletion")
		}
	})
}
