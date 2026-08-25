package providers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonModeServer records the request body and replies with a chat.completion
// whose content is the fixed JSON string reply.
func jsonModeServer(t *testing.T, captured *[]byte, reply string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*captured = body
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": "m",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newOpenAILangChainLLM(t *testing.T, baseURL, method string) *LangChainLLM {
	t.Helper()
	cm, err := makePartnerChatModel("openai", baseURL, "m", "k", 64, 0.2, 5, "")
	if err != nil {
		t.Fatalf("makePartnerChatModel: %v", err)
	}
	return WrapChatModel(cm, method)
}

func TestJSONModeSendsNativeResponseFormat(t *testing.T) {
	var body []byte
	srv := jsonModeServer(t, &body, `{"a": 3}`, http.StatusOK)
	defer srv.Close()
	l := newOpenAILangChainLLM(t, srv.URL, "json_mode")
	out, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema())
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 3.0 {
		t.Fatalf("out = %v, want a=3", out)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	rf, _ := payload["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format = %v, want {\"type\":\"json_object\"} (body: %s)", payload["response_format"], body)
	}
	// The schema system prompt is retained alongside native enforcement,
	// mirroring HTTPLLM's json_mode path.
	msgs, _ := payload["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || !strings.Contains(fmt.Sprint(first["content"]), "JSON schema") {
		t.Errorf("expected leading schema system prompt, got %v", first)
	}
}

func TestJSONModeNativeInvokeError(t *testing.T) {
	var body []byte
	srv := jsonModeServer(t, &body, "", http.StatusInternalServerError)
	defer srv.Close()
	l := newOpenAILangChainLLM(t, srv.URL, "json_mode")
	if _, err := l.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, testSchema()); err == nil {
		t.Fatal("expected invoke error from 500 response")
	}
}
