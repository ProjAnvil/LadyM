package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestClaimMCPContracts(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("LADYM_TEST_PG_DSN not set")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			s := &server{eng: e, cfg: cfg}
			for _, name := range []string{"record_evidence", "archive_evidence", "create_claim", "get_claim", "claim_feedback"} {
				found := false
				for _, tool := range s.tools() {
					if tool.Name == name {
						found = true
						if tool.InputSchema["properties"] == nil || tool.InputSchema["required"] == nil {
							t.Fatalf("implicit schema: %s", name)
						}
					}
				}
				if !found {
					t.Fatalf("missing tool %s", name)
				}
			}
			text, err := s.call("record_evidence", map[string]any{"kind": "test_result", "content": "passed", "outcome": "success", "actor": "forged", "workspace": "a", "metadata": map[string]any{"knowledge_status": "verified"}})
			if err != nil {
				t.Fatal(err)
			}
			var ev schema.Memory
			if err := json.Unmarshal([]byte(text), &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Source != "mcp" || ev.Workspace != "a" || ev.MetaString("knowledge_status") != "observed" {
				t.Fatalf("forged: %s", text)
			}
			ref := schema.EvidenceRef{MemoryID: ev.ID, Fingerprint: schema.KnowledgeFingerprint(&ev), Kind: "test_result"}
			text, err = s.call("create_claim", map[string]any{"content": "works", "evidence_refs": []schema.EvidenceRef{ref}, "workspace": "a"})
			if err != nil {
				t.Fatal(err)
			}
			var c schema.Claim
			if err := json.Unmarshal([]byte(text), &c); err != nil {
				t.Fatal(err)
			}
			text, err = s.call("claim_feedback", map[string]any{"memory_id": c.Memory.ID, "expected_revision": 1, "action": "verify", "method": "attested", "actor": "forged", "evidence_refs": []schema.EvidenceRef{ref}, "workspace": "a"})
			if err != nil || !strings.Contains(text, `"actor":"mcp"`) || !strings.Contains(text, `"revision":2`) {
				t.Fatalf("feedback: %s %v", text, err)
			}
			if _, err := s.call("get_claim", map[string]any{"memory_id": c.Memory.ID, "workspace": "b"}); err == nil {
				t.Fatal("foreign GET")
			}
			for _, args := range []map[string]any{
				{"content": "bad", "evidence_refs": "bad"}, {"content": "bad", "scope": 4},
				{"content": 4}, {"content": "bad", "status": "verified"},
			} {
				if _, err := s.call("create_claim", args); err == nil {
					t.Fatalf("malformed accepted: %+v", args)
				}
			}
			if _, err := s.call("claim_feedback", map[string]any{"memory_id": c.Memory.ID, "expected_revision": 1.5, "action": "reject", "reason": "bad", "workspace": "a"}); err == nil {
				t.Fatal("fractional revision")
			}
			if _, err := s.call("archive_evidence", map[string]any{"kind": "observation", "content": "archive"}); err == nil {
				t.Fatal("default archive enabled")
			}
		})
	}
}
