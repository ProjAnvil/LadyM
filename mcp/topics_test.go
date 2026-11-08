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

func TestTopicMCPContracts(t *testing.T) {
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
					t.Skip("PG unconfigured")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			s := &server{eng: e, cfg: cfg}
			for _, name := range []string{"build_topic", "get_topic", "hybrid_recall", "bootstrap", "set_task_state"} {
				found := false
				for _, tool := range s.tools() {
					if tool.Name == name {
						found = true
						if tool.InputSchema["properties"] == nil {
							t.Fatal(name)
						}
					}
				}
				if !found {
					t.Fatalf("missing %s", name)
				}
			}
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.Workspace, m.Content = "mcp-topics", "ExactMCP"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := s.call("build_topic", map[string]any{"topic": "topic", "input_ids": []string{m.ID}, "expected_revision": 0, "workspace": m.Workspace})
			if err != nil {
				t.Fatal(err)
			}
			var topic schema.Memory
			if err := json.Unmarshal([]byte(raw), &topic); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name  string
				args  map[string]any
				match string
			}{
				{"get_topic", map[string]any{"topic": "topic", "workspace": m.Workspace}, topic.ID},
				{"hybrid_recall", map[string]any{"query": "ExactMCP", "limit": 1, "workspace": m.Workspace}, m.ID},
				{"set_task_state", map[string]any{"id": "task", "goal": "explicit goal", "next_step": "next", "ttl_s": 60, "workspace": m.Workspace}, "explicit goal"},
				{"bootstrap", map[string]any{"topic_ids": []string{topic.ID}, "task_id": "task", "workspace": m.Workspace}, topic.ID},
			} {
				raw, err := s.call(tc.name, tc.args)
				if err != nil || !strings.Contains(raw, tc.match) {
					t.Fatalf("%s: %s %v", tc.name, raw, err)
				}
			}
			if _, err := s.call("bootstrap", map[string]any{"unit": "tokens"}); err == nil {
				t.Fatal("MCP tokens accepted")
			}
		})
	}
}
