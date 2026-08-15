package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestNewWithModelsInjectsProviders(t *testing.T) {
	emb := storage.NewHashingEmbedding(32)
	classifier := &providers.FakeLLMProvider{
		StructuredFn: func(msgs []providers.Message, schema providers.JSONSchema) (map[string]any, error) {
			return map[string]any{"action": "ADD", "new_text": nil}, nil
		},
	}
	routing := &adapter.ModelRouting{Embedding: emb, Consolidate: classifier}

	eng, err := NewWithModels(config.ForTesting(t.TempDir()), routing)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if eng.Provider != storage.EmbeddingProvider(emb) {
		t.Error("injected embedding provider not used")
	}
	if eng.Provider.Dim() != 32 {
		t.Errorf("dim = %d, want 32", eng.Provider.Dim())
	}

	// consolidate should route through the injected classifier (ADD action)
	_, _ = eng.RecordEvent("claude", "action", "obs", "success", nil, nil)
	report, err := eng.Consolidate("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Actions["ADD"] != 1 {
		t.Errorf("ADD = %d, want 1 (injected classifier)", report.Actions["ADD"])
	}
}

func TestRememberProceduralSnippet(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("fmt.Println(\"hi\")", schema.LayerProcedural, schema.TypeSnippet, nil, nil, "", "hello_snippet")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != schema.TypeSnippet || m.Layer != schema.LayerProcedural {
		t.Errorf("got layer=%s type=%s, want procedural/snippet", m.Layer, m.Type)
	}
}
