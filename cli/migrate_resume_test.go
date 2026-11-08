package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMigrationCLIResumeHonorsPersistedMode(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			for _, automatic := range []bool{false, true} {
				t.Run(fmt.Sprintf("automatic=%t", automatic), func(t *testing.T) {
					cfg := config.ForTesting(t.TempDir())
					cfg.StoreBackend = backend
					t.Setenv("LADYM_STORE_BACKEND", backend)
					if backend == "postgres" {
						dsn := os.Getenv("LADYM_TEST_PG_DSN")
						if dsn == "" {
							t.Skip("LADYM_TEST_PG_DSN not set")
						}
						cfg.StoreDSN = testutil.PGDatabase(t, dsn)
						t.Setenv("LADYM_STORE_DSN", cfg.StoreDSN)
					}
					var mu sync.Mutex
					var calls []string
					fail := true
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var request struct {
							Input string `json:"input"`
						}
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							http.Error(w, "invalid fixture input", http.StatusBadRequest)
							return
						}
						mu.Lock()
						defer mu.Unlock()
						calls = append(calls, request.Input)
						if fail && request.Input == "c" {
							http.Error(w, "injected embedding failure", http.StatusInternalServerError)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if err := json.NewEncoder(w).Encode(map[string]any{"data": []float32{0, 1, 0, 0, 0, 0, 0, 0}}); err != nil {
							t.Error(err)
						}
					}))
					t.Cleanup(server.Close)
					cfg.EmbeddingDim, cfg.EmbeddingProvider, cfg.EmbeddingModel = 8, "http", "resume-v1"
					cfg.EmbeddingBaseURL = server.URL
					t.Setenv("LADYM_EMBEDDING", "http")
					t.Setenv("LADYM_EMBEDDING_MODEL", "resume-v1")
					t.Setenv("LADYM_EMBEDDING_DIM", "8")
					t.Setenv("LADYM_EMBEDDING_BASE_URL", server.URL)
					file := filepath.Join(filepath.Dir(cfg.DBPath), "resume.toml")
					if err := os.WriteFile(file, []byte("embedding_dim=8\nembedding_query_cache_size=0\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					previous := globalConfigPath
					t.Cleanup(func() { globalConfigPath = previous })
					store, err := storage.OpenStore(cfg, 8)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					old := schema.EmbeddingIdentity{Provider: "test", Model: "old", Dim: 8, Tokenizer: "v1"}
					if err := store.AdoptEmbeddingIdentity(old); err != nil {
						t.Fatal(err)
					}
					for _, id := range []string{"a", "b", "c", "d"} {
						memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
						memory.ID, memory.Content = id, id
						if err := store.PutMemory(memory, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
							t.Fatal(err)
						}
					}
					provider, target, err := engine.MigrationProvider(cfg)
					if err != nil {
						t.Fatal(err)
					}
					if err := engine.MigrateEmbeddings(t.Context(), store, target, provider, 2, !automatic); err == nil {
						t.Fatal("provider failure did not interrupt staging")
					}
					mu.Lock()
					failedCalls := slices.Clone(calls)
					mu.Unlock()
					if !slices.Equal(failedCalls, []string{"a", "b", "c"}) {
						t.Fatalf("provider did not fail after exactly two stages: %v", failedCalls)
					}
					pending, err := store.EmbeddingMigrationStatus()
					if err != nil {
						t.Fatal(err)
					}
					if pending.State != "staging" || pending.Cursor != "b" || pending.Processed != 2 || pending.AutoFinalize != automatic {
						t.Fatalf("failure lost durable checkpoint/intent: %+v", pending)
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					fail, calls = false, nil
					mu.Unlock()
					var output bytes.Buffer
					run := func(action string) schema.MigrationStatus {
						t.Helper()
						output.Reset()
						cmd := newRootCmd()
						cmd.SetContext(t.Context())
						cmd.SetOut(&output)
						cmd.SetArgs([]string{"--config", file, "migrate", "embeddings", "--db", cfg.DBPath, action, "--batch-size", "2"})
						if err := cmd.Execute(); err != nil {
							t.Fatalf("%s: %v", action, err)
						}
						var status schema.MigrationStatus
						if err := json.Unmarshal(output.Bytes(), &status); err != nil {
							t.Fatal(err)
						}
						return status
					}
					completed := run("--resume")
					mu.Lock()
					gotCalls := slices.Clone(calls)
					mu.Unlock()
					if !slices.Equal(gotCalls, []string{"c", "d"}) {
						t.Fatalf("CLI re-embedded committed rows: %v", gotCalls)
					}
					if completed.ActiveFingerprint != target.Fingerprint() {
						t.Fatal("CLI did not activate the configured target")
					}
					if automatic {
						if completed.State != "idle" || completed.RollbackAvailable || completed.AutoFinalize || completed.Processed != 0 || completed.Generation != "" {
							t.Fatalf("CLI failed to finalize automatic intent: %+v", completed)
						}
					} else {
						if completed.State != "rollback_window" || !completed.RollbackAvailable || completed.AutoFinalize || completed.Processed != 4 || completed.Cursor != "d" || completed.Generation != pending.Generation {
							t.Fatalf("CLI lost explicit rollback/count/generation: %+v", completed)
						}
						again := run("--resume")
						if again.State != "rollback_window" || again.Generation != pending.Generation || !again.RollbackAvailable {
							t.Fatalf("repeated resume erased explicit rollback: %+v", again)
						}
						restored := run("--rollback")
						if restored.ActiveFingerprint != old.Fingerprint() {
							t.Fatal("CLI rollback lost the original identity")
						}
					}
					mu.Lock()
					finalCalls := slices.Clone(calls)
					mu.Unlock()
					if !slices.Equal(finalCalls, []string{"c", "d"}) {
						t.Fatalf("completed resume/control called the provider again: %v", finalCalls)
					}
					store, err = storage.OpenStore(cfg, 8)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { store.Close() })
					vector, err := store.GetEmbedding("a")
					want := []float32{1, 0, 0, 0, 0, 0, 0, 0}
					if automatic {
						want = []float32{0, 1, 0, 0, 0, 0, 0, 0}
					}
					if err != nil || !slices.Equal(vector, want) {
						t.Fatalf("CLI recovery vectors: %v %v", vector, err)
					}
					if err := store.UpdateMemoryContent("a", "writable", "", nil, nil, 1); err != nil {
						t.Fatalf("completed recovery still freezes writes: %v", err)
					}
				})
			}
		})
	}
}
