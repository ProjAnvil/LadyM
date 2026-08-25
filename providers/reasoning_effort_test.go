package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
)

// chatCompletionsServer replies with a minimal valid chat.completion and
// records the last request body.
func chatCompletionsServer(t *testing.T, captured *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		*captured = body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","object":"chat.completion","created":1,"model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
}

func TestReasoningEffortSentOnOpenAIRequest(t *testing.T) {
	var body []byte
	srv := chatCompletionsServer(t, &body)
	defer srv.Close()
	cm, err := makePartnerChatModel("openai", srv.URL, "m", "k", 16, 0.5, 5, "high")
	if err != nil {
		t.Fatalf("makePartnerChatModel: %v", err)
	}
	if _, err := cm.Invoke(context.Background(), []lcmessages.Message{lcmessages.Human("hi")}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if payload["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high (body: %s)", payload["reasoning_effort"], body)
	}
}

func TestReasoningEffortOmittedWhenUnset(t *testing.T) {
	var body []byte
	srv := chatCompletionsServer(t, &body)
	defer srv.Close()
	cm, err := makePartnerChatModel("openai", srv.URL, "m", "k", 16, 0.5, 5, "")
	if err != nil {
		t.Fatalf("makePartnerChatModel: %v", err)
	}
	if _, err := cm.Invoke(context.Background(), []lcmessages.Message{lcmessages.Human("hi")}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if strings.Contains(string(body), "reasoning_effort") {
		t.Errorf("empty effort must not send reasoning_effort (body: %s)", body)
	}
}

func TestMakeLLMProviderReasoningEffortValidation(t *testing.T) {
	if _, err := MakeLLMProvider("openai", "", "m", "k", "", "bogus", 1, 0, 1); err == nil ||
		!strings.Contains(err.Error(), "unknown reasoning_effort") {
		t.Fatalf("expected unknown reasoning_effort error, got %v", err)
	}
	// Case/whitespace-insensitive valid values are accepted by every provider
	// kind (non-OpenAI kinds accept and ignore the knob).
	for _, kind := range []string{"openai", "anthropic", "ollama", "http"} {
		if _, err := MakeLLMProvider(kind, "http://localhost:9999", "m", "k", "", " High ", 1, 0, 1); err != nil {
			t.Fatalf("MakeLLMProvider(%q, effort=High): %v", kind, err)
		}
	}
}

func TestAgentConfigReasoningEffort(t *testing.T) {
	cfg := config.Default()
	cfg.LLMReasoningEffort = "low"
	ac, err := NewAgentRegistry(cfg).Get("consolidate")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ac.ReasoningEffort != "low" {
		t.Errorf("ReasoningEffort = %q, want low (global)", ac.ReasoningEffort)
	}
	cfg.AgentsOverrides["consolidate"] = map[string]any{"reasoning_effort": "high"}
	ac, err = NewAgentRegistry(cfg).Get("consolidate")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ac.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort = %q, want high (override)", ac.ReasoningEffort)
	}
}
