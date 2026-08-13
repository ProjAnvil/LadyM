// Package providers holds LadyM's LLM provider abstraction and per-operation
// agent configuration.
package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is a single message in an LLM conversation.
type Message struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

// LLMProvider is the abstract base for LLM providers.
type LLMProvider interface {
	Name() string
	// Complete returns the model's text reply.
	Complete(messages []Message) (string, error)
	// CompleteStructured returns a parsed JSON object. schemaDesc is a compact
	// description of the expected JSON shape.
	CompleteStructured(messages []Message, schemaDesc string) (map[string]any, error)
	Close() error
}

// FakeLLMProvider is a scriptable test double.
type FakeLLMProvider struct {
	CompleteFn   func([]Message) (string, error)
	StructuredFn func([]Message, string) (map[string]any, error)
}

func (f *FakeLLMProvider) Name() string { return "fake" }

func (f *FakeLLMProvider) Complete(messages []Message) (string, error) {
	if f.CompleteFn == nil {
		return "", fmt.Errorf("FakeLLMProvider has no complete_fn scripted")
	}
	return f.CompleteFn(messages)
}

func (f *FakeLLMProvider) CompleteStructured(messages []Message, schemaDesc string) (map[string]any, error) {
	if f.StructuredFn == nil {
		return nil, fmt.Errorf("FakeLLMProvider has no structured_fn scripted")
	}
	return f.StructuredFn(messages, schemaDesc)
}

func (f *FakeLLMProvider) Close() error { return nil }

// HTTPLLM is a net/http-backed provider supporting OpenAI-compatible,
// Anthropic, and Ollama chat endpoints.
type HTTPLLM struct {
	name             string
	baseURL          string
	apiKey           string
	model            string
	maxTokens        int
	temperature      float64
	client           *http.Client
	structuredMethod string
	kind             string // "openai" | "anthropic" | "ollama"
}

func defaultBaseURL(kind string) string {
	switch kind {
	case "anthropic":
		return "https://api.anthropic.com"
	case "ollama":
		return "http://localhost:11434"
	default:
		return "https://api.openai.com/v1"
	}
}

func newHTTPLLM(kind, baseURL, model, apiKey string, maxTokens int, temperature float64, timeoutS float64, structuredMethod string) *HTTPLLM {
	if baseURL == "" {
		baseURL = defaultBaseURL(kind)
	}
	return &HTTPLLM{
		name: kind, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey,
		model: model, maxTokens: maxTokens, temperature: temperature,
		client:           &http.Client{Timeout: time.Duration(timeoutS * float64(time.Second))},
		structuredMethod: structuredMethod, kind: kind,
	}
}

func (h *HTTPLLM) Name() string { return h.name }

func (h *HTTPLLM) Close() error { return nil }

func (h *HTTPLLM) post(url string, payload map[string]any, headers map[string]string) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("llm endpoint returned %s: %s", resp.Status, string(respBody))
	}
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("invalid llm response: %w", err)
	}
	return out, nil
}

func (h *HTTPLLM) Complete(messages []Message) (string, error) {
	switch h.kind {
	case "anthropic":
		return h.anthropicComplete(messages, false, "")
	case "ollama":
		return h.ollamaComplete(messages, false, "")
	default:
		return h.openAIComplete(messages, false, "")
	}
}

func (h *HTTPLLM) CompleteStructured(messages []Message, schemaDesc string) (map[string]any, error) {
	var content string
	var err error
	switch h.kind {
	case "anthropic":
		content, err = h.anthropicComplete(messages, true, schemaDesc)
	case "ollama":
		content, err = h.ollamaComplete(messages, true, schemaDesc)
	default:
		content, err = h.openAIComplete(messages, true, schemaDesc)
	}
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		// Some models wrap JSON in fences; try to extract.
		extracted := extractJSON(content)
		if err := json.Unmarshal([]byte(extracted), &out); err != nil {
			return nil, fmt.Errorf("structured output was not valid JSON: %w", err)
		}
	}
	return out, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return s[i : j+1]
		}
	}
	return s
}

func (h *HTTPLLM) openAIComplete(messages []Message, structured bool, schemaDesc string) (string, error) {
	msgs := append([]Message{}, messages...)
	payload := map[string]any{
		"model":       h.model,
		"messages":    msgs,
		"max_tokens":  h.maxTokens,
		"temperature": h.temperature,
	}
	if structured {
		sys := "Reply ONLY with JSON matching this schema: " + schemaDesc
		msgs = append([]Message{{Role: "system", Content: sys}}, msgs...)
		payload["messages"] = msgs
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	headers := map[string]string{}
	if h.apiKey != "" {
		headers["Authorization"] = "Bearer " + h.apiKey
	}
	resp, err := h.post(h.baseURL+"/chat/completions", payload, headers)
	if err != nil {
		return "", err
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		return "", fmt.Errorf("llm response missing choices")
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	content, _ := msg["content"].(string)
	return content, nil
}

func (h *HTTPLLM) anthropicComplete(messages []Message, structured bool, schemaDesc string) (string, error) {
	var systemParts []string
	var userMsgs []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			systemParts = append(systemParts, m.Content)
		} else {
			userMsgs = append(userMsgs, map[string]any{"role": m.Role, "content": m.Content})
		}
	}
	if structured {
		systemParts = append(systemParts, "Reply ONLY with JSON matching this schema: "+schemaDesc)
	}
	payload := map[string]any{
		"model":       h.model,
		"max_tokens":  h.maxTokens,
		"temperature": h.temperature,
		"messages":    userMsgs,
	}
	if len(systemParts) > 0 {
		payload["system"] = strings.Join(systemParts, "\n")
	}
	headers := map[string]string{
		"x-api-key":         h.apiKey,
		"anthropic-version": "2023-06-01",
	}
	resp, err := h.post(h.baseURL+"/v1/messages", payload, headers)
	if err != nil {
		return "", err
	}
	content, _ := resp["content"].([]any)
	if len(content) == 0 {
		return "", fmt.Errorf("anthropic response missing content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text, nil
}

func (h *HTTPLLM) ollamaComplete(messages []Message, structured bool, schemaDesc string) (string, error) {
	msgs := append([]Message{}, messages...)
	payload := map[string]any{
		"model":    h.model,
		"messages": msgs,
		"stream":   false,
	}
	if structured {
		sys := "Reply ONLY with JSON matching this schema: " + schemaDesc
		msgs = append([]Message{{Role: "system", Content: sys}}, msgs...)
		payload["messages"] = msgs
		payload["format"] = "json"
	}
	resp, err := h.post(h.baseURL+"/api/chat", payload, nil)
	if err != nil {
		return "", err
	}
	msg, _ := resp["message"].(map[string]any)
	content, _ := msg["content"].(string)
	return content, nil
}

// MakeLLMProvider builds a provider for the given kind. Returns nil for "none".
func MakeLLMProvider(kind, baseURL, model, apiKey, structuredMethod string, maxTokens int, temperature, timeoutS float64) (LLMProvider, error) {
	k := strings.ToLower(kind)
	if k == "" {
		k = "none"
	}
	switch k {
	case "none":
		return nil, nil
	case "openai", "http":
		return newHTTPLLM("openai", baseURL, model, apiKey, maxTokens, temperature, timeoutS, structuredMethod), nil
	case "anthropic":
		return newHTTPLLM("anthropic", baseURL, model, apiKey, maxTokens, temperature, timeoutS, structuredMethod), nil
	case "ollama":
		return newHTTPLLM("ollama", baseURL, model, apiKey, maxTokens, temperature, timeoutS, structuredMethod), nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", kind)
	}
}
