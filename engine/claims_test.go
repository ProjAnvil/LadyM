package engine

import (
	"errors"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func runClaimBackends(t *testing.T, fn func(*testing.T, *Engine)) {
	t.Helper()
	runBackendConfigs(t, func(t *testing.T, cfg *config.Config) {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.Close() })
		fn(t, e)
	})
}

func runBackendConfigs(t *testing.T, fn func(*testing.T, *config.Config)) {
	t.Helper()
	backends := []string{"sqlite", "postgres"}
	if storage.Edition == "enterprise" {
		backends = []string{"postgres"}
	}
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("LADYM_TEST_PG_DSN not set")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			fn(t, cfg)
		})
	}
}

func claimFixture(t *testing.T, s *Scope) (*schema.Memory, *schema.Claim, schema.EvidenceRef) {
	t.Helper()
	m, err := s.RecordEvidence("test_result", "检查成功", "success", "host")
	if err != nil {
		t.Fatal(err)
	}
	ref := schema.EvidenceRef{MemoryID: m.ID, Fingerprint: schema.KnowledgeFingerprint(m), Kind: "test_result"}
	c, err := s.CreateClaim("检查有效", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return m, c, ref
}

func TestClaimAttestedVerification(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		_, c, ref := claimFixture(t, s)
		if c.Status != "inferred" || c.Revision != 1 {
			t.Fatalf("creation = %+v", c)
		}
		v, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "caller-attested", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil || v.Status != "verified" || v.Revision != 2 || v.Verification.Actor != "host" {
			t.Fatalf("verification: %+v %v", v, err)
		}
		if _, err := s.CreateClaim("forged", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "verified", 0); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("direct verification: %v", err)
		}
		for _, bounds := range [][2]int{{-1, 1}, {1, 0}, {0, 5}, {2, 2}} {
			bad := ref
			bad.StartRune, bad.EndRune = bounds[0], bounds[1]
			if _, err := s.CreateClaim("bad excerpt", []schema.EvidenceRef{bad}, schema.ClaimScope{}, "", 0); !errors.Is(err, ErrInvalidClaim) {
				t.Fatalf("accepted bounds %v: %v", bounds, err)
			}
		}
		for _, kind := range []string{"observation", "tool_result"} {
			ev, err := s.RecordEvidence(kind, "reported failure", "failure", "host")
			if err != nil {
				t.Fatal(err)
			}
			r := schema.EvidenceRef{MemoryID: ev.ID, Kind: kind, Fingerprint: schema.KnowledgeFingerprint(ev)}
			claim, err := s.CreateClaim("unsupported", []schema.EvidenceRef{r}, schema.ClaimScope{}, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ApplyClaimFeedback(claim.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "report", Actor: "host", Evidence: []schema.EvidenceRef{r}}); !errors.Is(err, ErrInvalidClaim) {
				t.Fatalf("failure/observation verified: %v", err)
			}
		}
		bad := ref
		bad.Kind = "user_confirmation"
		if _, err := s.CreateClaim("kind mismatch", []schema.EvidenceRef{bad}, schema.ClaimScope{}, "", 0); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("kind mismatch: %v", err)
		}
		legacy, err := s.RecordEvent("host", "completed task", "observed", "success", nil, map[string]any{"knowledge_status": "verified", "task_outcome": "success", "custom": "preserved"})
		if err != nil || legacy.MetaString("knowledge_status") != "verified" || legacy.MetaString("custom") != "preserved" {
			t.Fatalf("legacy caller-attested task metadata changed: %+v %v", legacy, err)
		}
		legacyRef := schema.EvidenceRef{MemoryID: legacy.ID, Fingerprint: schema.KnowledgeFingerprint(legacy), Kind: "test_result"}
		if _, err := s.CreateClaim("hook cannot verify claim", []schema.EvidenceRef{legacyRef}, schema.ClaimScope{}, "", 0); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("ordinary event used as typed verification: %v", err)
		}
	})
}

func TestClaimFeedbackCAS(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		_, c, ref := claimFixture(t, s)
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				_, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "report", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
				results <- err
			})
		}
		wg.Wait()
		close(results)
		wins, conflicts := 0, 0
		for err := range results {
			if err == nil {
				wins++
			} else if errors.Is(err, storage.ErrMemoryConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
		}
		child := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		child.Workspace = "claims"
		child.Metadata["source_episode"] = c.Memory.ID
		if err := e.Store.PutMemory(child, nil); err != nil {
			t.Fatal(err)
		}
		corrected, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 2, Action: "correct", Replacement: "corrected", Method: "report", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil || corrected.Revision != 3 || corrected.Memory.Content != "corrected" {
			t.Fatalf("correct: %+v %v", corrected, err)
		}
		raw, _ := e.Store.GetMemory(child.ID)
		if schema.IsUsable(raw) {
			t.Fatal("correction left child usable")
		}
		rejected, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 3, Action: "reject", Reason: "counterexample"})
		if err != nil || rejected.Revision != 4 || rejected.Status != "rejected" {
			t.Fatalf("reject: %+v %v", rejected, err)
		}
		if fresh, err := s.IsFresh(corrected.Memory, schema.Now()); err != nil || fresh {
			t.Fatalf("obsolete managed snapshot trusted: %v %v", fresh, err)
		}
	})
}

type changeSourceBeforeFeedback struct {
	storage.Store
	source *schema.Memory
}

func (s *changeSourceBeforeFeedback) ApplyMemoryBatch(batch storage.MemoryBatch) error {
	if s.source != nil {
		m := s.source.Clone()
		s.source = nil
		m.Content = "concurrent source edit"
		if err := s.Store.PutMemory(m, nil); err != nil {
			return err
		}
	}
	return s.Store.ApplyMemoryBatch(batch)
}

func TestClaimSourceChangedDuringFeedback(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		source, c, ref := claimFixture(t, s)
		e.Store = &changeSourceBeforeFeedback{Store: e.Store, source: source}
		_, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "correct", Replacement: "must not publish", Method: "report", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if !errors.Is(err, storage.ErrMemoryConflict) {
			t.Fatalf("source race: %v", err)
		}
		raw, err := e.Store.GetMemory(c.Memory.ID)
		if err != nil || raw.Content != "检查有效" || raw.MetaString("knowledge_status") != "stale" || schema.ManagedRevision(raw) != 2 {
			t.Fatalf("partial feedback after source race: %+v %v", raw, err)
		}
	})
}

func TestClaimScopeAndTrustChanges(t *testing.T) {
	for _, key := range []string{"environment", "code_version", "verification_kind", "outcome"} {
		t.Run(key, func(t *testing.T) {
			runClaimBackends(t, func(t *testing.T, e *Engine) {
				s := e.Scope("claims")
				source, c, _ := claimFixture(t, s)
				source.Metadata[key] = "changed"
				if err := e.Store.PutMemory(source, nil); err != nil {
					t.Fatal(err)
				}
				raw, err := e.Store.GetMemory(c.Memory.ID)
				if err != nil || raw.MetaString("knowledge_status") != "stale" || schema.ManagedRevision(raw) != 2 {
					t.Fatalf("trust/scope mutation escaped invalidation: %+v %v", raw, err)
				}
			})
		})
	}
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		source, _, ref := claimFixture(t, s)
		source.Metadata["scope"] = schema.ClaimScope{Environment: "prod", CodeVersion: "v1", Conditions: []string{"ready"}}
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		for _, scope := range []schema.ClaimScope{{}, {Environment: "prod", CodeVersion: "v1"}, {Environment: "prod", CodeVersion: "v1", Conditions: []string{"READY"}}} {
			if _, err := s.CreateClaim("scope mismatch", []schema.EvidenceRef{ref}, scope, "", 0); !errors.Is(err, ErrInvalidClaim) {
				t.Fatalf("accepted scope mismatch: %+v %v", scope, err)
			}
		}
		if _, err := s.RecordEvent("hook", "test", "observation", "success", nil, map[string]any{"verification_kind": "test_result"}); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("ordinary hook forged typed evidence: %v", err)
		}
	})
}

func TestManagedGenericEditRejected(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		_, c, _ := claimFixture(t, s)
		if err := e.Store.UpdateMemoryContent(c.Memory.ID, "forged", "", nil, nil, 0); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("generic claim edit: %v", err)
		}
		for _, typ := range []schema.MemoryType{schema.TypeClaim, schema.TypeFact} {
			_, err := s.Remember("forged verification", schema.LayerSemantic, typ, nil, map[string]any{"knowledge_status": "verified", "verification_kind": "test_result"}, "", "")
			if !errors.Is(err, ErrInvalidClaim) {
				t.Fatalf("generic remember forged trust: %v", err)
			}
		}
	})
}

func TestEvidenceFreshness(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		source, c, ref := claimFixture(t, s)
		c, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "report", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil {
			t.Fatal(err)
		}
		vector, err := e.Provider.Embed(c.Memory.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.PutMemory(c.Memory, vector); err != nil {
			t.Fatal(err)
		}
		before, err := e.Recall("检查有效", "claims", 10, nil, []schema.MemoryType{schema.TypeClaim}, 0)
		if err != nil || !slices.ContainsFunc(before.Results, func(r *schema.RecallResult) bool { return r.Memory.ID == c.Memory.ID }) {
			t.Fatalf("fixture was never recallable: %v", err)
		}
		source.Metadata["valid_until"] = schema.Now() + 60
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Store.TouchMemories([]string{source.ID, c.Memory.ID}, schema.Now()); err != nil {
			t.Fatal(err)
		}
		fresh, err := s.IsFresh(c.Memory, schema.Now())
		if err != nil || !fresh {
			t.Fatalf("access/TTL extension invalidated: %v %v", fresh, err)
		}
		fresh, err = storage.IsFresh(e.Store, c.Memory, schema.Now()+120)
		if err != nil || fresh {
			t.Fatalf("transitive TTL ignored: %v %v", fresh, err)
		}
		// Expiry need not run a worker or persist stale state first.
		source.Metadata["valid_until"] = schema.Now() - 1
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		list, err := e.List("claims", nil, 100, 0)
		if err != nil || slices.ContainsFunc(list, func(m *schema.Memory) bool { return m.ID == c.Memory.ID }) {
			t.Fatalf("list exposed expired claim: %v", err)
		}
		got, err := s.GetClaim(c.Memory.ID)
		if err != nil || got.Status != "stale" || got.Memory.Content != "" || got.Memory.Summary != "" {
			t.Fatalf("typed read exposed stale body: %+v %v", got, err)
		}
		recall, err := e.Recall("检查", "claims", 10, nil, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(recall.Results, func(r *schema.RecallResult) bool { return r.Memory.ID == c.Memory.ID }) {
			t.Fatal("recall exposed expired claim")
		}
	})
}

func TestClaimCorrectionReplacesStaleSources(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		source, c, _ := claimFixture(t, s)
		source.Content = "edited evidence"
		source.Metadata["superseded"] = true
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		ev, err := s.RecordEvidence("user_confirmation", "explicit confirmation", "", "host")
		if err != nil {
			t.Fatal(err)
		}
		ref := schema.EvidenceRef{MemoryID: ev.ID, Kind: "user_confirmation", Fingerprint: schema.KnowledgeFingerprint(ev)}
		updated, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 2, Action: "correct", Replacement: "corrected body", Method: "confirmation", Actor: "host", Evidence: []schema.EvidenceRef{ref}})
		if err != nil || updated.Revision != 3 || updated.Status != "verified" {
			t.Fatalf("correction with replacement evidence: %+v %v", updated, err)
		}
		if err := e.Store.DeleteMemory(source.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetClaim(c.Memory.ID)
		if err != nil || got.Status != "verified" || got.Memory.Content != "corrected body" {
			t.Fatalf("obsolete dependency invalidated correction: %+v %v", got, err)
		}
	})
}

func TestBulkIndexRetirementInvalidation(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		source, c, _ := claimFixture(t, s)
		if err := operations.Retire(e.Store, source, ""); err != nil {
			t.Fatal(err)
		}
		raw, err := e.Store.GetMemory(c.Memory.ID)
		if err != nil || raw.MetaString("knowledge_status") != "stale" || schema.ManagedRevision(raw) != 2 {
			t.Fatalf("Retire left claim usable: %+v %v", raw, err)
		}
	})
}

func TestManagedMetadataRevisionSemantics(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("claims")
		_, c, _ := claimFixture(t, s)
		raw := c.Memory.Clone()
		raw.Metadata["operation_note"] = "bookkeeping"
		if err := e.Store.PutMemory(raw, nil); err != nil {
			t.Fatal(err)
		}
		raw, _ = e.Store.GetMemory(raw.ID)
		if schema.ManagedRevision(raw) != 1 {
			t.Fatal("operational metadata bumped managed revision")
		}
		child := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		child.Workspace = "claims"
		child.Metadata["source_episode"] = raw.ID
		if err := e.Store.PutMemory(child, nil); err != nil {
			t.Fatal(err)
		}
		changed := raw.Clone()
		changed.Summary = "changed knowledge summary"
		if err := e.Store.PutMemory(changed, nil); err != nil {
			t.Fatal(err)
		}
		if schema.ManagedRevision(changed) != 2 {
			t.Fatal("managed summary did not bump revision")
		}
		got, _ := e.Store.GetMemory(child.ID)
		if schema.IsUsable(got) {
			t.Fatal("managed revision change did not invalidate children")
		}
	})
}
