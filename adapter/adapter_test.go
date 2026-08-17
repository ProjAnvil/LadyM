package adapter

import (
	"context"
	"errors"
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

func TestWrapEmbeddingsBatchProbesDim(t *testing.T) {
	// When EmbedBatch runs before any Embed call, it probes the dim itself.
	e := WrapEmbeddings(lcembeddings.NewFake(4))
	batch, err := e.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(batch) != 2 || len(batch[0]) != 4 {
		t.Fatalf("EmbedBatch shape = %v", batch)
	}
	if e.Dim() != 4 {
		t.Fatalf("Dim after EmbedBatch = %d, want 4", e.Dim())
	}
}

// errEmbeddings is an embeddings.Embeddings that always fails, to exercise the
// adapter's error-propagation branches.
type errEmbeddings struct{ err error }

func (e errEmbeddings) EmbedDocuments(context.Context, []string) ([][]float64, error) {
	return nil, e.err
}

func (e errEmbeddings) EmbedQuery(context.Context, string) ([]float64, error) {
	return nil, e.err
}

func TestWrapEmbeddingsErrors(t *testing.T) {
	want := errors.New("boom")
	e := WrapEmbeddings(errEmbeddings{err: want})

	if _, err := e.Embed("hello"); !errors.Is(err, want) {
		t.Fatalf("Embed err = %v, want %v", err, want)
	}
	if _, err := e.EmbedBatch([]string{"a"}); !errors.Is(err, want) {
		t.Fatalf("EmbedBatch err = %v, want %v", err, want)
	}
	ok, msg := e.HealthCheck()
	if ok || msg != want.Error() {
		t.Fatalf("HealthCheck = %v, %q; want false, %q", ok, msg, want.Error())
	}
	if e.Dim() != 0 {
		t.Fatalf("Dim after failed embeds = %d, want 0", e.Dim())
	}
}

func TestModelRoutingGet(t *testing.T) {
	var nilRouting *ModelRouting
	if got := nilRouting.Get("consolidate"); got != nil {
		t.Fatalf("nil routing Get = %v, want nil", got)
	}

	cm := language.NewFakeChatModel(language.WithResponses(lcmessages.AI("x")))
	consolidate := WrapChatModel(cm, "")
	proceduralize := WrapChatModel(cm, "")
	attentionGate := WrapChatModel(cm, "")
	l5 := WrapChatModel(cm, "")
	l6 := WrapChatModel(cm, "")
	r := &ModelRouting{
		Consolidate:     consolidate,
		Proceduralize:   proceduralize,
		AttentionGate:   attentionGate,
		L5MentalModel:   l5,
		L6ForwardIntent: l6,
	}

	cases := []struct {
		op   string
		want providers.LLMProvider
	}{
		{"consolidate", consolidate},
		{"proceduralize", proceduralize},
		{"attention_gate", attentionGate},
		{"l5_mental_model", l5},
		{"l6_forward_intent", l6},
		{"unknown_op", nil},
		{"", nil},
	}
	for _, tc := range cases {
		if got := r.Get(tc.op); got != tc.want {
			t.Errorf("Get(%q) = %v, want %v", tc.op, got, tc.want)
		}
	}

	// Unset fields fall back to nil.
	empty := &ModelRouting{}
	if got := empty.Get("consolidate"); got != nil {
		t.Fatalf("empty routing Get = %v, want nil", got)
	}
}
