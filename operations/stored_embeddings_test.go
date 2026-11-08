package operations

import (
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type lifecycleEmbedding struct{ batchCalls int }

func (p *lifecycleEmbedding) Embed(string) ([]float32, error) { return []float32{1, 0}, nil }
func (p *lifecycleEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	p.batchCalls++
	var out [][]float32
	for range texts {
		out = append(out, []float32{1, 0})
	}
	return out, nil
}
func (p *lifecycleEmbedding) Dim() int                    { return 2 }
func (p *lifecycleEmbedding) HealthCheck() (bool, string) { return true, "" }

func TestStoredEmbeddingLifecycleReuse(t *testing.T) {
	for _, operation := range []string{"l5", "proceduralize"} {
		t.Run(operation, func(t *testing.T) {
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
					store, err := storage.OpenStore(cfg, 2)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					for _, content := range []string{"source alpha", "source beta"} {
						m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
						if operation == "proceduralize" {
							m.Layer, m.Type, m.Metadata["outcome"] = schema.LayerEpisodic, schema.TypeEvent, "success"
						}
						m.Workspace, m.Content, m.ContentHash = "test", content, schema.ContentHash(content)
						if err := store.PutMemory(m, []float32{1, 0}); err != nil {
							t.Fatal(err)
						}
					}
					p := &lifecycleEmbedding{}
					if operation == "l5" {
						cfg.System2.L5MinClusterSize = 2
						llm := &providers.FakeLLMProvider{StructuredFn: func([]providers.Message, providers.JSONSchema) (map[string]any, error) {
							return map[string]any{"title": "model", "model": "distilled"}, nil
						}}
						report, err := ExtractL5(store, p, cfg, "test", llm, "")
						if err != nil || report.NewModels != 1 {
							t.Fatalf("L5 %+v %v", report, err)
						}
					} else {
						report, err := Proceduralize(store, p, cfg, "test", 2, 0.5)
						if err != nil || report.PlaybooksCreated != 1 {
							t.Fatalf("proceduralize %+v %v", report, err)
						}
					}
					if p.batchCalls != 0 {
						t.Fatalf("unchanged lifecycle sources re-embedded %d", p.batchCalls)
					}
				})
			}
		})
	}
}
