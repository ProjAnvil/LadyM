package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

type noRepeatEmbedding struct {
	dim   int
	calls int
}

func (p *noRepeatEmbedding) Dim() int { return p.dim }
func (p *noRepeatEmbedding) Embed(text string) ([]float32, error) {
	p.calls++
	return make([]float32, p.dim), nil
}
func (p *noRepeatEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	var out [][]float32
	for _, text := range texts {
		v, _ := p.Embed(text)
		out = append(out, v)
	}
	return out, nil
}
func (p *noRepeatEmbedding) HealthCheck() (bool, string) { return true, "" }

func TestStoredEmbeddingReuse(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, store Store) {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.Content, m.ContentHash = "unchanged", schema.ContentHash("unchanged")
		p := &noRepeatEmbedding{dim: suiteDim}
		vec := make([]float32, suiteDim)
		vec[0] = 1
		if err := store.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			vectors, err := StoredEmbeddings(store, p, []*schema.Memory{m})
			if err != nil || vectors[0][0] != 1 || p.calls != 0 {
				t.Fatalf("unchanged reuse %v %v calls=%d", vectors, err, p.calls)
			}
		}
	})
}

func TestMaintenanceEmbeddingCancellation(t *testing.T) {
	for _, kind := range []string{"openai", "ollama"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(time.Second):
				}
			}))
			defer server.Close()
			var provider ContextEmbeddingProvider
			if kind == "openai" {
				provider = NewOpenAIEmbedding("model", server.URL, "", 5)
			} else {
				provider = NewOllamaEmbedding(server.URL, "model", 5, nil)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			start := time.Now()
			if _, err := provider.EmbedContext(ctx, "text"); err == nil || time.Since(start) > time.Second {
				t.Fatalf("real embedding I/O not cancelled %v", err)
			}
		})
	}
}
