package adapter

import (
	"testing"

	lcembeddings "github.com/projanvil/langchain-golang/core/embeddings"
	language "github.com/projanvil/langchain-golang/core/language"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"

	"github.com/ProjAnvil/LadyM/providers"
)

func TestWrapChatModelComplete(t *testing.T) {
	cm := language.NewFakeChatModel(language.WithResponses(lcmessages.AI("hello")))
	p := WrapChatModel(cm, "")
	out, err := p.Complete([]providers.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "hello" {
		t.Fatalf("Complete = %q, want hello", out)
	}
}

func TestWrapChatModelStructured(t *testing.T) {
	// FakeChatModel does not implement StructuredCaller, so this exercises
	// InvokeStructured's fallback: JSON decode + required-key validation.
	cm := language.NewFakeChatModel(language.WithResponses(lcmessages.AI(`{"action":"ADD"}`)))
	p := WrapChatModel(cm, "")
	out, err := p.CompleteStructured([]providers.Message{{Role: "user", Content: "hi"}}, providers.JSONSchema{
		"type":       "object",
		"properties": map[string]any{"action": map[string]any{"type": "string"}},
		"required":   []string{"action"},
	})
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["action"] != "ADD" {
		t.Fatalf("action = %v, want ADD", out["action"])
	}
}

func TestWrapChatModelStructuredMissingRequiredKey(t *testing.T) {
	cm := language.NewFakeChatModel(language.WithResponses(lcmessages.AI(`{"other":1}`)))
	p := WrapChatModel(cm, "")
	_, err := p.CompleteStructured([]providers.Message{{Role: "user", Content: "hi"}}, providers.JSONSchema{
		"type":     "object",
		"required": []string{"action"},
	})
	if err == nil {
		t.Fatal("expected schema violation for missing required key")
	}
}

func TestWrapEmbeddingsDeferredDim(t *testing.T) {
	e := WrapEmbeddings(lcembeddings.NewFake(8))
	if e.Dim() != 0 {
		t.Fatalf("Dim before first embed = %d, want 0 (deferred)", e.Dim())
	}
	vec, err := e.Embed("hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 8 || e.Dim() != 8 {
		t.Fatalf("len(vec)=%d dim=%d, want 8/8", len(vec), e.Dim())
	}
	batch, err := e.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != 2 || len(batch[0]) != 8 {
		t.Fatalf("EmbedBatch shape = %v", batch)
	}
	if ok, msg := e.HealthCheck(); !ok {
		t.Fatalf("HealthCheck = %v, %s", ok, msg)
	}
}
