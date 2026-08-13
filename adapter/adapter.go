// Package adapter holds the host-model injection bridge.
//
// In the Python port this wrapped langchain ChatModel/Embeddings objects. In
// Go there is no langchain, so ModelRouting carries the equivalent native
// interfaces: a custom EmbeddingProvider and per-operation LLMProvider. Unset
// fields fall back to Config / heuristic mode.
package adapter

import (
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/storage"
)

// ModelRouting injects host-owned models, bypassing LadyM's own config. Unset
// fields fall back to Config / heuristic. Field names mirror providers.NAMED_OPS.
type ModelRouting struct {
	Embedding       storage.EmbeddingProvider
	Consolidate     providers.LLMProvider
	Proceduralize   providers.LLMProvider
	AttentionGate   providers.LLMProvider
	L5MentalModel   providers.LLMProvider
	L6ForwardIntent providers.LLMProvider
}

// Get returns the injected LLM provider for op, or nil.
func (r *ModelRouting) Get(op string) providers.LLMProvider {
	if r == nil {
		return nil
	}
	switch op {
	case "consolidate":
		return r.Consolidate
	case "proceduralize":
		return r.Proceduralize
	case "attention_gate":
		return r.AttentionGate
	case "l5_mental_model":
		return r.L5MentalModel
	case "l6_forward_intent":
		return r.L6ForwardIntent
	default:
		return nil
	}
}
