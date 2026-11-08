package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestClaimCLIContracts(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			dir := t.TempDir()
			cfg := config.ForTesting(dir)
			cfg.Workspace, cfg.StoreBackend = "cli-claims", backend
			t.Setenv("LADYM_STORE_BACKEND", backend)
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("LADYM_TEST_PG_DSN not set")
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
			h := httptest.NewServer(api.NewHandler(e, cfg))
			t.Cleanup(h.Close)
			for _, mode := range []string{"local", "remote"} {
				t.Run(mode, func(t *testing.T) {
					run := func(args ...string) ([]byte, error) {
						cmd := newRootCmd()
						var out bytes.Buffer
						cmd.SetOut(&out)
						if mode == "remote" {
							args = append(args, "--server", h.URL)
						}
						args = append(args, "--workspace", "cli-claims")
						cmd.SetArgs(args)
						err := cmd.Execute()
						return out.Bytes(), err
					}
					raw, err := run("evidence", "record", "passed", "--kind", "test_result", "--outcome", "success")
					if err != nil {
						t.Fatal(err)
					}
					var ev schema.Memory
					if err := json.Unmarshal(raw, &ev); err != nil {
						t.Fatal(err)
					}
					wantActor := "cli"
					if mode == "remote" {
						wantActor = "local"
					}
					if ev.Source != wantActor {
						t.Fatalf("actor=%s", ev.Source)
					}
					refs, _ := json.Marshal([]schema.EvidenceRef{{MemoryID: ev.ID, Fingerprint: schema.KnowledgeFingerprint(&ev), Kind: "test_result"}})
					raw, err = run("claim", "create", "works", "--evidence", string(refs), "--scope", `{"environment":"test","code_version":"v1","conditions":["ready"]}`)
					if err != nil {
						t.Fatal(err)
					}
					var c schema.Claim
					if err := json.Unmarshal(raw, &c); err != nil {
						t.Fatal(err)
					}
					raw, err = run("claim", "feedback", c.Memory.ID, "--action", "verify", "--expected-revision", "1", "--method", "attested", "--evidence", string(refs))
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(raw, &c); err != nil || c.Revision != 2 || c.Status != "verified" {
						t.Fatalf("feedback=%s %v", raw, err)
					}
					if _, err := run("claim", "get", c.Memory.ID); err != nil {
						t.Fatal(err)
					}
					for _, args := range [][]string{
						{"claim", "create", "bad", "--evidence", "not-json"}, {"claim", "create", "bad", "--evidence", "null"},
						{"claim", "feedback", c.Memory.ID, "--action", "reject", "--reason", "bad"},
						{"claim", "feedback", c.Memory.ID, "--action", "reject", "--expected-revision", "1", "--reason", "bad"},
						{"claim", "get", "missing"}, {"claim", "get", c.Memory.ID, "extra"},
						{"evidence", "archive", "text", "--kind", "observation"},
						{"claim", "get", c.Memory.ID, "--db", "ignored", "--server", h.URL},
					} {
						if _, err := run(args...); err == nil {
							t.Fatalf("accepted malformed request %v", args)
						}
					}
				})
			}
		})
	}
}
