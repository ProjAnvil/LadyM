package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFakeLLMProvider(t *testing.T) {
	f := &FakeLLMProvider{}
	if f.Name() != "fake" {
		t.Fatalf("Name=%q, want fake", f.Name())
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := f.Complete([]Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("Complete without CompleteFn should error")
	}
	if _, err := f.CompleteStructured(nil, testSchema()); err == nil {
		t.Fatal("CompleteStructured without StructuredFn should error")
	}

	f.CompleteFn = func(msgs []Message) (string, error) { return "ok:" + msgs[0].Content, nil }
	f.StructuredFn = func(msgs []Message, sc JSONSchema) (map[string]any, error) {
		return map[string]any{"a": 1.0}, nil
	}
	out, err := f.Complete([]Message{{Role: "user", Content: "hi"}})
	if err != nil || out != "ok:hi" {
		t.Fatalf("Complete=%q, %v", out, err)
	}
	sout, err := f.CompleteStructured(nil, testSchema())
	if err != nil || sout["a"] != 1.0 {
		t.Fatalf("CompleteStructured=%v, %v", sout, err)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	cases := map[string]string{
		"anthropic": "https://api.anthropic.com",
		"ollama":    "http://localhost:11434",
		"openai":    "https://api.openai.com/v1",
		"":          "https://api.openai.com/v1",
		"bogus":     "https://api.openai.com/v1",
	}
	for kind, want := range cases {
		if got := defaultBaseURL(kind); got != want {
			t.Errorf("defaultBaseURL(%q)=%q, want %q", kind, got, want)
		}
	}
}

func TestNewHTTPLLMDefaults(t *testing.T) {
	h := newHTTPLLM("anthropic", "", "claude-test", "key", 1024, 0.2, 30, "")
	if h.baseURL != "https://api.anthropic.com" {
		t.Fatalf("baseURL=%q, want anthropic default", h.baseURL)
	}
	if h.Name() != "anthropic" {
		t.Fatalf("Name=%q, want anthropic", h.Name())
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	trimmed := newHTTPLLM("openai", "http://example.com/", "m", "", 1, 0, 1, "")
	if trimmed.baseURL != "http://example.com" {
		t.Fatalf("trailing slash not trimmed: %q", trimmed.baseURL)
	}
}

func TestPostErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)
	llm := newOpenAI(t, srv.URL, "")
	_, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 error, got %v", err)
	}
}

func TestPostInvalidJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not json"))
	}))
	t.Cleanup(srv.Close)
	llm := newOpenAI(t, srv.URL, "")
	_, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "invalid llm response") {
		t.Fatalf("expected invalid-response error, got %v", err)
	}
}

func TestPostMarshalError(t *testing.T) {
	llm := newOpenAI(t, "http://example.com", "")
	_, err := llm.post("http://example.com/x", map[string]any{"bad": make(chan int)}, nil)
	if err == nil {
		t.Fatal("expected marshal error for unencodable payload")
	}
}

func TestPostRequestError(t *testing.T) {
	llm := newOpenAI(t, "http://example.com", "")
	if _, err := llm.post("://bad-url", map[string]any{}, nil); err == nil {
		t.Fatal("expected request construction error for malformed URL")
	}
}

func TestPostDoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // connection now refused
	llm := newOpenAI(t, url, "")
	if _, err := llm.Complete([]Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected transport error against closed server")
	}
}

func TestCompleteDispatchByKind(t *testing.T) {
	t.Run("openai", func(t *testing.T) {
		srv, _ := captureServer(t, openAIReply("hello"))
		llm := newOpenAI(t, srv.URL, "")
		out, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
		if err != nil || out != "hello" {
			t.Fatalf("Complete=%q, %v", out, err)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		srv, _ := captureServer(t, anthropicTextReply("hello"))
		llm := newAnthropic(t, srv.URL, "")
		out, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
		if err != nil || out != "hello" {
			t.Fatalf("Complete=%q, %v", out, err)
		}
	})
}

func TestOpenAIMissingChoices(t *testing.T) {
	srv, _ := captureServer(t, map[string]any{})
	llm := newOpenAI(t, srv.URL, "")
	_, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "missing choices") {
		t.Fatalf("expected missing-choices error, got %v", err)
	}
}

func TestAnthropicMissingContent(t *testing.T) {
	srv, _ := captureServer(t, map[string]any{})
	llm := newAnthropic(t, srv.URL, "")
	_, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "missing content") {
		t.Fatalf("expected missing-content error, got %v", err)
	}
}

func TestAnthropicSystemMessagesJoined(t *testing.T) {
	srv, captured := captureServer(t, anthropicTextReply("ok"))
	llm := newAnthropic(t, srv.URL, "")
	msgs := []Message{
		{Role: "system", Content: "rule one"},
		{Role: "system", Content: "rule two"},
		{Role: "user", Content: "hi"},
	}
	if _, err := llm.Complete(msgs); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	sys, _ := (*captured)["system"].(string)
	if sys != "rule one\nrule two" {
		t.Fatalf("system=%q, want joined parts", sys)
	}
	sent, ok := (*captured)["messages"].([]any)
	if !ok || len(sent) != 1 {
		t.Fatalf("system messages must not appear in messages: %v", (*captured)["messages"])
	}
	m, _ := sent[0].(map[string]any)
	if m["role"] != "user" || m["content"] != "hi" {
		t.Fatalf("unexpected user message: %v", m)
	}
}

func TestAnthropicToolUseWithoutInputFallsThrough(t *testing.T) {
	reply := map[string]any{
		"content": []any{
			map[string]any{"type": "tool_use", "name": "structured_output"}, // no input
			map[string]any{"type": "text", "text": "fallback"},
		},
	}
	srv, _ := captureServer(t, reply)
	llm := newAnthropic(t, srv.URL, "")
	out, err := llm.Complete([]Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// content[0] is the input-less tool_use block, so the text fallback yields "".
	if out != "" {
		t.Fatalf("Complete=%q, want empty text fallback", out)
	}
}

func TestOllamaStructuredSendsSchemaFormat(t *testing.T) {
	reply := map[string]any{"message": map[string]any{"role": "assistant", "content": `{"a":1}`}}
	srv, captured := captureServer(t, reply)
	llm := newHTTPLLM("ollama", srv.URL, "llama-test", "", 1024, 0.7, 30, "")
	out, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 1.0 {
		t.Fatalf("parsed=%v, want a=1", out)
	}
	format, ok := (*captured)["format"].(map[string]any)
	if !ok || format["type"] != "object" {
		t.Fatalf("ollama structured: format should carry the real schema: %v", (*captured)["format"])
	}
	msgs, ok := (*captured)["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected injected system message: %v", (*captured)["messages"])
	}
	sys, _ := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Fatalf("first message should be the schema system prompt: %v", sys)
	}
}

func TestCompleteStructuredPropagatesCompletionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	for _, kind := range []string{"openai", "anthropic", "ollama"} {
		llm := newHTTPLLM(kind, srv.URL, "m", "k", 1, 0, 30, "")
		if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema()); err == nil {
			t.Fatalf("%s: expected error propagation", kind)
		}
	}
}

func TestCompleteStructuredFencedJSON(t *testing.T) {
	srv, _ := captureServer(t, openAIReply("```json\n{\"a\": 3}\n```"))
	llm := newOpenAI(t, srv.URL, "")
	out, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 3.0 {
		t.Fatalf("fenced JSON not extracted: %v", out)
	}
}

func TestCompleteStructuredUnrecoverableJSON(t *testing.T) {
	srv, _ := captureServer(t, openAIReply("totally not json {"))
	llm := newOpenAI(t, srv.URL, "")
	_, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("expected not-valid-JSON error, got %v", err)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"no json at all", "no json at all"},
		{"{unclosed", "{unclosed"},
		{"  {\"a\":1}  ", `{"a":1}`},
	}
	for _, c := range cases {
		if got := extractJSON(c.in); got != c.want {
			t.Errorf("extractJSON(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestSchemaPromptTextMarshalFallback(t *testing.T) {
	bad := JSONSchema{"bad": make(chan int)}
	got := schemaPromptText(bad)
	if got == "" || !strings.Contains(got, "bad") {
		t.Fatalf("schemaPromptText fallback=%q, want fmt rendering", got)
	}
	good := schemaPromptText(testSchema())
	var round map[string]any
	if err := json.Unmarshal([]byte(good), &round); err != nil {
		t.Fatalf("schemaPromptText should emit valid JSON: %v", err)
	}
}

func TestMakeLLMProvider(t *testing.T) {
	t.Run("empty and none return nil", func(t *testing.T) {
		for _, kind := range []string{"", "none", "NONE"} {
			p, err := MakeLLMProvider(kind, "", "m", "k", "", "", 1, 0, 1)
			if err != nil || p != nil {
				t.Fatalf("MakeLLMProvider(%q)=(%v, %v), want (nil, nil)", kind, p, err)
			}
		}
	})
	t.Run("http returns HTTPLLM", func(t *testing.T) {
		p, err := MakeLLMProvider("http", "http://example.com", "m", "k", "", "", 1, 0, 1)
		if err != nil {
			t.Fatalf("MakeLLMProvider: %v", err)
		}
		h, ok := p.(*HTTPLLM)
		if !ok {
			t.Fatalf("http provider is %T, want *HTTPLLM", p)
		}
		if h.Name() != "openai" {
			t.Fatalf("http escape hatch should be openai-compatible, got %q", h.Name())
		}
	})
	t.Run("partner kinds wrap LangChainLLM", func(t *testing.T) {
		for _, kind := range []string{"openai", "anthropic", "ollama"} {
			p, err := MakeLLMProvider(kind, "", "m", "k", "function_calling", "", 1, 0, 1)
			if err != nil {
				t.Fatalf("MakeLLMProvider(%q): %v", kind, err)
			}
			lc, ok := p.(*LangChainLLM)
			if !ok {
				t.Fatalf("%s provider is %T, want *LangChainLLM", kind, p)
			}
			if lc.Name() != "langchain" {
				t.Fatalf("Name=%q, want langchain", lc.Name())
			}
		}
	})
	t.Run("unknown kind errors", func(t *testing.T) {
		_, err := MakeLLMProvider("bogus", "", "m", "k", "", "", 1, 0, 1)
		if err == nil || !strings.Contains(err.Error(), "unknown llm provider") {
			t.Fatalf("expected unknown-provider error, got %v", err)
		}
	})
}

func TestProvidersImplementInterface(t *testing.T) {
	var _ LLMProvider = (*HTTPLLM)(nil)
	var _ LLMProvider = (*FakeLLMProvider)(nil)
	var _ LLMProvider = (*LangChainLLM)(nil)
}
