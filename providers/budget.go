package providers

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var ErrContextUnsupported = errors.New("provider does not support cancellable, raw-measured calls")
var ErrOutputTooLarge = errors.New("raw completion exceeds byte limit")

type CallOptions struct {
	MaxOutputBytes  int
	MaxOutputTokens int
}

type StructuredResult struct {
	Data           map[string]any
	RawOutputBytes int
}

type ContextLLMProvider interface {
	CompleteStructuredContext(context.Context, []Message, JSONSchema, CallOptions) (StructuredResult, error)
}

func decodeMeasured(text string, options CallOptions) (StructuredResult, error) {
	result := StructuredResult{RawOutputBytes: len(text)}
	if options.MaxOutputBytes <= 0 || len(text) > options.MaxOutputBytes {
		return result, ErrOutputTooLarge
	}
	if err := json.Unmarshal([]byte(extractJSON(text)), &result.Data); err != nil {
		return result, err
	}
	return result, nil
}

func (h *HTTPLLM) CompleteStructuredContext(ctx context.Context, messages []Message, sc JSONSchema, options CallOptions) (StructuredResult, error) {
	if err := ctx.Err(); err != nil {
		return StructuredResult{}, err
	}
	if options.MaxOutputTokens <= 0 || options.MaxOutputBytes <= 0 {
		return StructuredResult{}, ErrOutputTooLarge
	}
	view := *h
	view.ctx, view.maxTokens, view.maxOutputBytes = ctx, options.MaxOutputTokens, options.MaxOutputBytes
	method, err := normalizedStructuredMethod(view.structuredMethod)
	if err != nil {
		return StructuredResult{RawOutputBytes: view.rawOutputBytes}, err
	}
	var text string
	switch view.kind {
	case "anthropic":
		text, err = view.anthropicComplete(messages, true, sc, method)
	case "ollama":
		text, err = view.ollamaComplete(messages, true, sc)
	default:
		text, err = view.openAIComplete(messages, true, sc, method)
	}
	if err != nil {
		return StructuredResult{RawOutputBytes: view.rawOutputBytes}, err
	}
	if err := ctx.Err(); err != nil {
		return StructuredResult{RawOutputBytes: view.rawOutputBytes}, err
	}
	result, err := decodeMeasured(text, options)
	result.RawOutputBytes = view.rawOutputBytes
	return result, err
}

func measureRawCompletion(raw []byte, kind string) (int, string, error) {
	switch kind {
	case "anthropic":
		var envelope struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return 0, "", err
		}
		total, tool := 0, ""
		for _, block := range envelope.Content {
			if block.Type == "tool_use" {
				total += len(block.Input)
				if tool == "" {
					tool = string(block.Input)
				}
			} else {
				total += len(block.Text)
			}
		}
		return total, tool, nil
	case "ollama":
		var envelope struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return 0, "", err
		}
		return len(envelope.Message.Content), "", nil
	default:
		var envelope struct {
			Choices []struct {
				Message struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
					FunctionCall struct {
						Arguments string `json:"arguments"`
					} `json:"function_call"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return 0, "", err
		}
		total := 0
		for _, choice := range envelope.Choices {
			total += len(choice.Message.Content) + len(choice.Message.FunctionCall.Arguments)
			for _, tool := range choice.Message.ToolCalls {
				total += len(tool.Function.Arguments)
			}
		}
		return total, "", nil
	}
}

func (l *LangChainLLM) CompleteStructuredContext(ctx context.Context, messages []Message, sc JSONSchema, options CallOptions) (StructuredResult, error) {
	if capable, ok := l.cm.(ContextLLMProvider); ok {
		return capable.CompleteStructuredContext(ctx, messages, sc, options)
	}
	if l.contextHTTP == nil {
		return StructuredResult{}, ErrContextUnsupported
	}
	// InvokeStructured may parse/re-encode native tool output, losing raw size.
	// Configured partners use their equivalent HTTP endpoint for bounded calls.
	return l.contextHTTP.CompleteStructuredContext(ctx, messages, sc, options)
}

type Budget struct {
	mu                         sync.Mutex
	MaxCalls, MaxReservedBytes int
	Calls, ReservedBytes       int
	InputBytes, OutputBytes    int
	StopReason                 string
}

func SerializedInputBytes(messages []Message, sc JSONSchema) (int, error) {
	raw, err := json.Marshal(struct {
		Messages []Message  `json:"messages"`
		Schema   JSONSchema `json:"schema"`
	}{messages, sc})
	return len(raw), err
}

func (b *Budget) Reserve(inputBytes, outputBytes int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Calls >= b.MaxCalls {
		b.StopReason = "calls"
		return false
	}
	if inputBytes < 0 || outputBytes <= 0 || inputBytes+outputBytes > b.MaxReservedBytes-b.ReservedBytes {
		b.StopReason = "reserved_bytes"
		return false
	}
	b.Calls++
	b.ReservedBytes += inputBytes + outputBytes
	b.InputBytes += inputBytes
	return true
}

func (b *Budget) Measure(outputBytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.OutputBytes += max(0, outputBytes)
}

func (b *Budget) Counters() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{"calls": b.Calls, "reserved_bytes": b.ReservedBytes, "measured_input_bytes": b.InputBytes, "measured_output_bytes": b.OutputBytes, "unit": "bytes"}
}
