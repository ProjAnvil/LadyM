package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
)

// Offline contract (Python parity): when no LLM provider is configured
// (provider "" / "none"), a System2 worker cycle must SKIP L5 mental-model
// extraction and L6 forward-intent prediction entirely — zero L5_mental /
// L6_predictive memories, even when episodes and facts give them material
// to work with.

// seedSystem2Material writes enough episodes and facts that L5/L6 would
// produce output if they ran.
func seedSystem2Material(t *testing.T, eng *Engine) {
	t.Helper()
	for _, a := range []string{"wrote auth module", "tested auth module", "fixed auth bug", "deployed auth service"} {
		if _, err := eng.RecordEvent("bot", a, "observation about "+a, "success", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{
		"auth uses JWT tokens for sessions",
		" billing retries failed charges twice ",
		"metrics export pushes to the gateway",
	} {
		if _, err := eng.Remember(f, schema.LayerSemantic, schema.TypeFact, nil, nil, "test", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func system2TestConfig(t *testing.T) *config.Config {
	cfg := config.ForTesting(t.TempDir()) // LLMProvider = "none"
	cfg.System2.L5ClusterSimilarity = 0.0 // force all candidates into one cluster
	cfg.System2.L5MinClusterSize = 2
	cfg.System2.L5MergeEveryNCycles = 0 // skip the merge pass
	return cfg
}

func countLayer(t *testing.T, eng *Engine, layer schema.Layer) int {
	t.Helper()
	ms, err := eng.Store.IterMemories(eng.Config.Workspace, string(layer), "")
	if err != nil {
		t.Fatal(err)
	}
	return len(ms)
}

func TestSystem2CycleSkipsL5L6WithoutLLM(t *testing.T) {
	eng, err := New(system2TestConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	seedSystem2Material(t, eng)

	rep, err := operations.RunSystem2Cycle(eng, eng.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	l5, _ := rep.L5.(*operations.L5ExtractionReport)
	if l5 == nil || !l5.Skipped {
		t.Errorf("L5 report = %+v, want Skipped=true (no LLM configured)", rep.L5)
	}
	l6, _ := rep.L6.(*operations.L6PredictionReport)
	if l6 == nil || !l6.Skipped {
		t.Errorf("L6 report = %+v, want Skipped=true (no LLM configured)", rep.L6)
	}
	if n := countLayer(t, eng, schema.LayerL5Mental); n != 0 {
		t.Errorf("L5_mental memories = %d, want 0 (worker must skip L5 without an LLM)", n)
	}
	if n := countLayer(t, eng, schema.LayerL6Predictive); n != 0 {
		t.Errorf("L6_predictive memories = %d, want 0 (worker must skip L6 without an LLM)", n)
	}
}

// Positive control: with an LLM bound to the l5/l6 ops the same cycle keeps
// producing L5/L6 memories (existing behavior preserved).
func TestSystem2CycleProducesL5L6WithInjectedLLM(t *testing.T) {
	fake := &providers.FakeLLMProvider{
		StructuredFn: func(_ []providers.Message, sc providers.JSONSchema) (map[string]any, error) {
			if props, _ := sc["properties"].(map[string]any); props != nil {
				if _, isL6 := props["intents"]; isL6 {
					return map[string]any{"intents": []any{
						map[string]any{"intent": "ship the auth service", "confidence": 0.9},
					}}, nil
				}
			}
			return map[string]any{"title": "delivery work", "model": "recurring build-test-fix-deploy loop"}, nil
		},
	}
	routing := &adapter.ModelRouting{L5MentalModel: fake, L6ForwardIntent: fake}
	eng, err := NewWithModels(system2TestConfig(t), routing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	seedSystem2Material(t, eng)

	rep, err := operations.RunSystem2Cycle(eng, eng.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	l5, _ := rep.L5.(*operations.L5ExtractionReport)
	if l5 == nil || l5.Skipped || l5.NewModels == 0 {
		t.Errorf("L5 report = %+v, want a new mental model (LLM injected)", rep.L5)
	}
	l6, _ := rep.L6.(*operations.L6PredictionReport)
	if l6 == nil || l6.Skipped || l6.Predictions == 0 {
		t.Errorf("L6 report = %+v, want a prediction (LLM injected)", rep.L6)
	}
	if n := countLayer(t, eng, schema.LayerL5Mental); n == 0 {
		t.Error("L5_mental memories = 0, want >= 1 with an LLM configured")
	}
	if n := countLayer(t, eng, schema.LayerL6Predictive); n == 0 {
		t.Error("L6_predictive memories = 0, want >= 1 with an LLM configured")
	}
}
