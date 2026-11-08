package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	p := newHTTPLLM("openai", server.URL, "test", "", 4096, 0, 5, "")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := p.CompleteStructuredContext(ctx, nil, JSONSchema{}, CallOptions{MaxOutputTokens: 1024, MaxOutputBytes: 1024})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected real I/O cancellation: %v", err)
	}
	<-started
}

func TestMaintenanceRawOutput(t *testing.T) {
	for _, size := range []int{0, 1100} {
		t.Run(string(rune('a'+size/1100)), func(t *testing.T) {
			text := strings.Repeat(" ", size) + `{"ok":true}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input map[string]any
				_ = json.NewDecoder(r.Body).Decode(&input)
				if input["max_tokens"] != float64(1024) {
					t.Errorf("token cap: %v", input["max_tokens"])
				}

				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}}}})
			}))
			defer server.Close()
			p := newHTTPLLM("openai", server.URL, "test", "", 4096, 0, 5, "")
			result, err := p.CompleteStructuredContext(t.Context(), nil, JSONSchema{}, CallOptions{MaxOutputTokens: 1024, MaxOutputBytes: 1024})
			if size > 0 {
				if !errors.Is(err, ErrOutputTooLarge) || result.RawOutputBytes != len(text) || result.Data != nil {
					t.Fatalf("raw cap before parse: %+v %v", result, err)
				}
			} else if err != nil || result.RawOutputBytes != len(text) {
				t.Fatalf("measurement: %+v %v", result, err)
			}
		})
	}
}
