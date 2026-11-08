package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestTopicCLIContracts(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			t.Setenv("LADYM_STORE_BACKEND", backend)
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("PG unconfigured")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
				t.Setenv("LADYM_STORE_DSN", cfg.StoreDSN)
			}
			t.Setenv("LADYM_DB", cfg.DBPath)
			t.Setenv("LADYM_EMBEDDING_DIM", "64")
			t.Setenv("LADYM_EMBEDDING_PROVIDER", "hashing")
			t.Setenv("LADYM_LLM_PROVIDER", "none")
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.Workspace, m.Content = "cli-topics", "ExactCLI"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(api.NewHandler(e, cfg))
			t.Cleanup(server.Close)
			for _, mode := range []string{"local", "remote"} {
				t.Run(mode, func(t *testing.T) {
					run := func(args ...string) ([]byte, error) {
						cmd := newRootCmd()
						var out bytes.Buffer
						cmd.SetOut(&out)
						args = append(args, "--workspace", "cli-topics")
						if mode == "remote" {
							args = append(args, "--server", server.URL)
						}
						cmd.SetArgs(args)
						err := cmd.Execute()
						return out.Bytes(), err
					}
					raw, err := run("topic", "build", mode, "--input-ids", m.ID, "--expected-revision", "0")
					if err != nil {
						t.Fatal(err)
					}
					var topic schema.Memory
					if err := json.Unmarshal(raw, &topic); err != nil {
						t.Fatal(err)
					}
					for _, args := range [][]string{{"topic", "get", mode}, {"hybrid-recall", "ExactCLI", "--limit", "1"}, {"task-state", "set", mode, "--goal", "explicit goal", "--next-step", "next", "--ttl-s", "60"}, {"bootstrap", "--topic-ids", topic.ID, "--task-id", mode}} {
						raw, err := run(args...)
						if err != nil {
							t.Fatalf("%v: %v", args, err)
						}
						if args[0] == "bootstrap" && (!strings.Contains(string(raw), topic.ID) || !strings.Contains(string(raw), "explicit goal")) {
							t.Fatalf("bootstrap %s", raw)
						}
					}
					if _, err := run("bootstrap", "--unit", "tokens"); err == nil {
						t.Fatal("CLI tokens accepted")
					}
				})
			}
		})
	}
}
