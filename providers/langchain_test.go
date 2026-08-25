package providers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/projanvil/langchain-golang/core/language"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	"github.com/projanvil/langchain-golang/core/runnables"
	"github.com/projanvil/langchain-golang/core/schema"
	"github.com/projanvil/langchain-golang/core/tools"
)

// errChatModel is a ChatModel whose calls always fail, used to exercise the
// error paths of LangChainLLM.
type errChatModel struct{ err error }

func (e *errChatModel) Invoke(context.Context, []lcmessages.Message, ...runnables.Option) (lcmessages.Message, error) {
	return lcmessages.Message{}, e.err
}

func (e *errChatModel) Batch(context.Context, [][]lcmessages.Message, ...runnables.Option) ([]lcmessages.Message, error) {
	return nil, e.err
}

func (e *errChatModel) Stream(context.Context, []lcmessages.Message, ...runnables.Option) (runnables.Stream[lcmessages.Message], error) {
	return nil, e.err
}

func (e *errChatModel) InputSchema() schema.Schema  { return nil }
func (e *errChatModel) OutputSchema() schema.Schema { return nil }

func (e *errChatModel) BindTools([]tools.Tool) (language.ChatModel, error) { return e, nil }

func (e *errChatModel) Capabilities() language.ChatModelCapabilities {
	return language.ChatModelCapabilities{}
}

func TestWrapChatModelBasics(t *testing.T) {
	fake := language.NewFakeChatModel()
	l := WrapChatModel(fake, "")
	if l.Name() != "langchain" {
		t.Fatalf("Name=%q, want langchain", l.Name())
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestToLCMessages(t *testing.T) {
	msgs := toLCMessages([]Message{
		{Role: "system", Content: "s"},
		{Role: "assistant", Content: "a"},
		{Role: "user", Content: "u"},
		{Role: "tool", Content: "t"}, // unknown roles fall back to human
	})
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}
	wantRoles := []lcmessages.Role{
		lcmessages.RoleSystem, lcmessages.RoleAI, lcmessages.RoleHuman, lcmessages.RoleHuman,
	}
	for i, want := range wantRoles {
		if msgs[i].Role != want {
			t.Errorf("msgs[%d].Role=%q, want %q", i, msgs[i].Role, want)
		}
	}
}

func TestLangChainComplete(t *testing.T) {
	fake := language.NewFakeChatModel(language.WithResponses(lcmessages.AI("hello")))
	l := WrapChatModel(fake, "")
	out, err := l.Complete([]Message{{Role: "user", Content: "hi"}})
	if err != nil || out != "hello" {
		t.Fatalf("Complete=(%q, %v)", out, err)
	}
}

func TestLangChainCompleteError(t *testing.T) {
	l := WrapChatModel(&errChatModel{err: errors.New("boom")}, "")
	if _, err := l.Complete([]Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected invoke error")
	}
}

func TestLangChainStructuredNativeFallback(t *testing.T) {
	// FakeChatModel does not implement StructuredCaller, so InvokeStructured
	// takes the JSON-decode + required-key fallback path.
	fake := language.NewFakeChatModel(language.WithResponses(lcmessages.AI(`{"a":1}`)))
	l := WrapChatModel(fake, "")
	out, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 1.0 {
		t.Fatalf("out=%v, want a=1", out)
	}
}

func TestLangChainStructuredJSONMode(t *testing.T) {
	fake := language.NewFakeChatModel(language.WithResponses(lcmessages.AI("```json\n{\"a\": 2}\n```")))
	l := WrapChatModel(fake, "json_mode")
	out, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 2.0 {
		t.Fatalf("fenced JSON not extracted: %v", out)
	}
}

func TestLangChainStructuredUnrecoverableJSON(t *testing.T) {
	fake := language.NewFakeChatModel(language.WithResponses(lcmessages.AI("not json at all")))
	l := WrapChatModel(fake, "json_mode")
	_, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("expected not-valid-JSON error, got %v", err)
	}
}

func TestLangChainStructuredUnknownMethod(t *testing.T) {
	fake := language.NewFakeChatModel()
	l := WrapChatModel(fake, "bogus_method")
	if _, err := l.CompleteStructured(nil, testSchema()); err == nil {
		t.Fatal("expected unknown structured_method error")
	}
}

func TestLangChainStructuredInvokeErrors(t *testing.T) {
	for _, method := range []string{"", "json_mode"} {
		l := WrapChatModel(&errChatModel{err: errors.New("boom")}, method)
		if _, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema()); err == nil {
			t.Fatalf("method %q: expected invoke error", method)
		}
	}
}

func TestMakePartnerChatModel(t *testing.T) {
	t.Run("all partner kinds build", func(t *testing.T) {
		for _, kind := range []string{"openai", "anthropic", "ollama"} {
			cm, err := makePartnerChatModel(kind, "http://localhost:9999", "m", "k", 128, 0.5, 10, "")
			if err != nil {
				t.Fatalf("makePartnerChatModel(%q): %v", kind, err)
			}
			if cm == nil {
				t.Fatalf("makePartnerChatModel(%q) returned nil model", kind)
			}
		}
	})
	t.Run("defaults without optional settings", func(t *testing.T) {
		cm, err := makePartnerChatModel("openai", "", "m", "", 0, 0, 0, "")
		if err != nil || cm == nil {
			t.Fatalf("makePartnerChatModel=(%v, %v)", cm, err)
		}
	})
	t.Run("unknown kind errors", func(t *testing.T) {
		_, err := makePartnerChatModel("bogus", "", "m", "", 0, 0, 0, "")
		if err == nil || !strings.Contains(err.Error(), "unknown llm provider") {
			t.Fatalf("expected unknown-provider error, got %v", err)
		}
	})
}
