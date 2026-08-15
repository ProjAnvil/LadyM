// Package adapter holds the host-model injection bridge.
//
// In the Python port this wrapped langchain ChatModel/Embeddings objects. The
// Go port integrates langchain-golang: hosts inject their ChatModel /
// Embeddings via WrapChatModel / WrapEmbeddings and assign the results to
// ModelRouting. Unset fields fall back to Config / heuristic mode.
package adapter

import (
	"context"

	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/projanvil/langchain-golang/core/embeddings"
	"github.com/projanvil/langchain-golang/core/language"
)

// WrapChatModel adapts a host-owned langchain-golang ChatModel into an
// LLMProvider suitable for ModelRouting — the Go equivalent of Python's
// ModelRouting fields accepting langchain BaseChatModel objects directly.
// structuredMethod mirrors Python's structured_method ("function_calling" |
// "json_mode" | "json_schema"; "" = provider default).
func WrapChatModel(cm language.ChatModel, structuredMethod string) providers.LLMProvider {
	return providers.WrapChatModel(cm, structuredMethod)
}

// WrapEmbeddings bridges a host-owned langchain-golang Embeddings into
// ladyM's EmbeddingProvider, mirroring Python's LangChainEmbeddingAdapter.
// Dim starts at 0 and is probed on the first Embed call (deferred-dim
// pattern, same as OllamaEmbedding / Python's dim=None).
func WrapEmbeddings(e embeddings.Embeddings) storage.EmbeddingProvider {
	return &langchainEmbedding{lc: e}
}

type langchainEmbedding struct {
	lc  embeddings.Embeddings
	dim int
}

func toFloat32s(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

func (a *langchainEmbedding) Embed(text string) ([]float32, error) {
	vec, err := a.lc.EmbedQuery(context.Background(), text)
	if err != nil {
		return nil, err
	}
	if a.dim == 0 {
		a.dim = len(vec)
	}
	return toFloat32s(vec), nil
}

func (a *langchainEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	vecs, err := a.lc.EmbedDocuments(context.Background(), texts)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(vecs))
	for i, v := range vecs {
		if a.dim == 0 {
			a.dim = len(v)
		}
		out[i] = toFloat32s(v)
	}
	return out, nil
}

func (a *langchainEmbedding) Dim() int { return a.dim }

func (a *langchainEmbedding) HealthCheck() (bool, string) {
	if _, err := a.lc.EmbedQuery(context.Background(), "ping"); err != nil {
		return false, err.Error()
	}
	return true, "ok"
}

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
