// LangChainLLM adapts a langchain-golang language.ChatModel to ladyM's
// LLMProvider — the Go equivalent of Python's LangChainLLMProvider
// (adapter.py on the main branch). Structured output goes through
// language.InvokeStructured with a real JSON Schema, so providers enforce it
// natively (OpenAI json_schema response_format, Anthropic tool use, Ollama
// format schema) instead of ladyM's former prompt-only fallback.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/projanvil/langchain-golang/core/language"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	"github.com/projanvil/langchain-golang/core/modelconfig"
	"github.com/projanvil/langchain-golang/core/schema"
	"github.com/projanvil/langchain-golang/partners/anthropic"
	"github.com/projanvil/langchain-golang/partners/ollama"
	"github.com/projanvil/langchain-golang/partners/openai"
)

// LangChainLLM wraps a langchain-golang ChatModel as an LLMProvider.
type LangChainLLM struct {
	cm               language.ChatModel
	structuredMethod string
}

// WrapChatModel adapts a host-owned langchain-golang ChatModel, mirroring
// Python's LangChainLLMProvider(chat_model, structured_method).
func WrapChatModel(cm language.ChatModel, structuredMethod string) *LangChainLLM {
	return &LangChainLLM{cm: cm, structuredMethod: structuredMethod}
}

func (l *LangChainLLM) Name() string { return "langchain" }

func (l *LangChainLLM) Close() error { return nil }

func toLCMessages(messages []Message) []lcmessages.Message {
	out := make([]lcmessages.Message, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case "system":
			out = append(out, lcmessages.System(m.Content))
		case "assistant":
			out = append(out, lcmessages.AI(m.Content))
		default:
			out = append(out, lcmessages.Human(m.Content))
		}
	}
	return out
}

func (l *LangChainLLM) Complete(messages []Message) (string, error) {
	resp, err := l.cm.Invoke(context.Background(), toLCMessages(messages))
	if err != nil {
		return "", err
	}
	return lcmessages.Text(resp), nil
}

func (l *LangChainLLM) CompleteStructured(messages []Message, sc JSONSchema) (map[string]any, error) {
	method, err := normalizedStructuredMethod(l.structuredMethod)
	if err != nil {
		return nil, err
	}
	lcMsgs := toLCMessages(messages)
	var text string
	if method == "json_mode" {
		// Prompt-based path: no provider-native enforcement, mirror the
		// HTTPLLM json_mode behavior.
		sys := lcmessages.System("Reply ONLY with JSON matching this JSON schema: " + schemaPromptText(sc))
		resp, err := l.cm.Invoke(context.Background(), append([]lcmessages.Message{sys}, lcMsgs...))
		if err != nil {
			return nil, err
		}
		text = lcmessages.Text(resp)
	} else {
		// Native path (function_calling / json_schema / provider default):
		// InvokeStructured prefers the model's StructuredCaller
		// implementation and falls back to JSON decode + required-key
		// validation, mirroring LangChain's with_structured_output.
		resp, err := language.InvokeStructured(context.Background(), l.cm, lcMsgs, schema.Schema(sc))
		if err != nil {
			return nil, err
		}
		text = lcmessages.Text(resp)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		extracted := extractJSON(text)
		if err := json.Unmarshal([]byte(extracted), &out); err != nil {
			return nil, fmt.Errorf("structured output was not valid JSON: %w", err)
		}
	}
	return out, nil
}

// makePartnerChatModel builds a langchain-golang partner ChatModel from
// ladyM's LLM config values. kind is "openai" | "anthropic" | "ollama".
func makePartnerChatModel(kind, baseURL, model, apiKey string, maxTokens int, temperature, timeoutS float64) (language.ChatModel, error) {
	opts := []modelconfig.Option{
		modelconfig.WithModel(model),
		modelconfig.WithTemperature(temperature),
	}
	if baseURL != "" {
		opts = append(opts, modelconfig.WithBaseURL(baseURL))
	}
	if apiKey != "" {
		opts = append(opts, modelconfig.WithAPIKey(apiKey))
	}
	if maxTokens > 0 {
		opts = append(opts, modelconfig.WithMaxTokens(maxTokens))
	}
	if timeoutS > 0 {
		opts = append(opts, modelconfig.WithTimeout(time.Duration(timeoutS*float64(time.Second))))
	}
	switch kind {
	case "openai":
		// Python's ChatOpenAI (and most OpenAI-compatible base_url servers)
		// target /chat/completions; langchain-golang defaults to the Responses
		// API, so switch explicitly for parity.
		return openai.NewChatModel(opts...).WithChatCompletions(), nil
	case "anthropic":
		return anthropic.NewChatModel(opts...), nil
	case "ollama":
		return ollama.NewChatModel(opts...), nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", kind)
	}
}
