package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureServer records the last request body and replies with the given
// provider-shaped response.
func captureServer(t *testing.T, reply map[string]any) (*httptest.Server, *map[string]any) {
	t.Helper()
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func openAIReply(content string) map[string]any {
	return map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{"role": "assistant", "content": content}},
		},
	}
}

func openAIToolCallReply(arguments string) map[string]any {
	return map[string]any{
		"choices": []any{
			map[string]any{"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []any{
					map[string]any{"type": "function", "function": map[string]any{
						"name":      "structured_output",
						"arguments": arguments,
					}},
				},
			}},
		},
	}
}

func newOpenAI(t *testing.T, baseURL, method string) *HTTPLLM {
	t.Helper()
	return newHTTPLLM("openai", baseURL, "gpt-test", "key", 1024, 0.2, 30, method)
}

func newAnthropic(t *testing.T, baseURL, method string) *HTTPLLM {
	t.Helper()
	return newHTTPLLM("anthropic", baseURL, "claude-test", "key", 1024, 0.2, 30, method)
}

func TestOpenAIStructuredJSONModeUsesJSONObject(t *testing.T) {
	srv, captured := captureServer(t, openAIReply(`{"a":1}`))
	llm := newOpenAI(t, srv.URL, "json_mode")
	if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object"); err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	rf, ok := (*captured)["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("missing response_format in payload: %v", *captured)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("json_mode: got response_format.type=%v, want json_object", rf["type"])
	}
	if _, hasTools := (*captured)["tools"]; hasTools {
		t.Fatalf("json_mode must not send tools: %v", *captured)
	}
}

func TestOpenAIStructuredDefaultUsesJSONObject(t *testing.T) {
	srv, captured := captureServer(t, openAIReply(`{"a":1}`))
	llm := newOpenAI(t, srv.URL, "")
	if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object"); err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	rf, ok := (*captured)["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_object" {
		t.Fatalf("empty structured_method: got response_format=%v, want type=json_object", (*captured)["response_format"])
	}
}

func TestOpenAIStructuredJSONSchema(t *testing.T) {
	srv, captured := captureServer(t, openAIReply(`{"a":1}`))
	llm := newOpenAI(t, srv.URL, "json_schema")
	if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object"); err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	rf, ok := (*captured)["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("missing response_format: %v", *captured)
	}
	if rf["type"] != "json_schema" {
		t.Fatalf("json_schema: got response_format.type=%v, want json_schema", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema: missing json_schema object: %v", rf)
	}
	if js["name"] == nil || js["name"] == "" {
		t.Fatalf("json_schema: missing name: %v", js)
	}
	schema, ok := js["schema"].(map[string]any)
	if !ok || schema["type"] != "object" {
		t.Fatalf("json_schema: missing/invalid schema: %v", js)
	}
}

func TestOpenAIStructuredFunctionCalling(t *testing.T) {
	srv, captured := captureServer(t, openAIToolCallReply(`{"a":1}`))
	llm := newOpenAI(t, srv.URL, "function_calling")
	out, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object")
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 1.0 {
		t.Fatalf("tool_call arguments not parsed: %v", out)
	}
	tools, ok := (*captured)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("function_calling: missing tools: %v", *captured)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Fatalf("function_calling: tool type=%v, want function", tool["type"])
	}
	fn, _ := tool["function"].(map[string]any)
	if fn["name"] != "structured_output" {
		t.Fatalf("function_calling: function name=%v", fn["name"])
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok || params["type"] != "object" {
		t.Fatalf("function_calling: missing parameters schema: %v", fn)
	}
	tc, ok := (*captured)["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "function" {
		t.Fatalf("function_calling: missing tool_choice: %v", *captured)
	}
	if _, hasRF := (*captured)["response_format"]; hasRF {
		t.Fatalf("function_calling must not send response_format: %v", *captured)
	}
}

func anthropicTextReply(text string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

func anthropicToolUseReply(input map[string]any) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{
			"type":  "tool_use",
			"name":  "structured_output",
			"input": input,
		}},
	}
}

func TestAnthropicStructuredFunctionCalling(t *testing.T) {
	srv, captured := captureServer(t, anthropicToolUseReply(map[string]any{"a": 1}))
	llm := newAnthropic(t, srv.URL, "function_calling")
	out, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object")
	if err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if out["a"] != 1.0 {
		t.Fatalf("tool_use input not parsed: %v", out)
	}
	tools, ok := (*captured)["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("function_calling: missing tools: %v", *captured)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "structured_output" {
		t.Fatalf("function_calling: tool name=%v", tool["name"])
	}
	if _, ok := tool["input_schema"].(map[string]any); !ok {
		t.Fatalf("function_calling: missing input_schema: %v", tool)
	}
	tc, ok := (*captured)["tool_choice"].(map[string]any)
	if !ok || tc["type"] != "tool" || tc["name"] != "structured_output" {
		t.Fatalf("function_calling: missing/invalid tool_choice: %v", (*captured)["tool_choice"])
	}
}

func TestAnthropicStructuredJSONSchemaUsesTools(t *testing.T) {
	srv, captured := captureServer(t, anthropicToolUseReply(map[string]any{"a": 1}))
	llm := newAnthropic(t, srv.URL, "json_schema")
	if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object"); err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if _, ok := (*captured)["tools"].([]any); !ok {
		t.Fatalf("json_schema on anthropic should use tools: %v", *captured)
	}
}

func TestAnthropicStructuredJSONModeUsesPromptInjection(t *testing.T) {
	srv, captured := captureServer(t, anthropicTextReply(`{"a":1}`))
	llm := newAnthropic(t, srv.URL, "json_mode")
	if _, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object"); err != nil {
		t.Fatalf("CompleteStructured: %v", err)
	}
	if _, hasTools := (*captured)["tools"]; hasTools {
		t.Fatalf("json_mode on anthropic must not send tools: %v", *captured)
	}
	sys, _ := (*captured)["system"].(string)
	if sys == "" {
		t.Fatalf("json_mode: expected schema prompt in system: %v", *captured)
	}
}

func TestUnknownStructuredMethodErrors(t *testing.T) {
	srv, _ := captureServer(t, openAIReply(`{"a":1}`))
	llm := newOpenAI(t, srv.URL, "bogus_method")
	_, err := llm.CompleteStructured([]Message{{Role: "user", Content: "hi"}}, "an object")
	if err == nil {
		t.Fatal("expected error for unknown structured_method")
	}
}

func TestOllamaSendsTemperature(t *testing.T) {
	reply := map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}
	srv, captured := captureServer(t, reply)
	llm := newHTTPLLM("ollama", srv.URL, "llama-test", "", 1024, 0.7, 30, "")
	if _, err := llm.Complete([]Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	opts, ok := (*captured)["options"].(map[string]any)
	if !ok {
		t.Fatalf("ollama payload missing options: %v", *captured)
	}
	if opts["temperature"] != 0.7 {
		t.Fatalf("ollama options.temperature=%v, want 0.7", opts["temperature"])
	}
}
