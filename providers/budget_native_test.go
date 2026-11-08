package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaintenanceNativeRawOutput(t *testing.T) {
	for _, kind := range []string{"anthropic", "ollama"} {
		for _, large := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", kind, large), func(t *testing.T) {
				padding := ""
				if large {
					padding = strings.Repeat(" ", 1100)
				}
				text := `{"ok":` + padding + `true}`
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req map[string]any
					_ = json.NewDecoder(r.Body).Decode(&req)
					if kind == "anthropic" {
						if req["max_tokens"] != float64(1024) {
							t.Error("missing Anthropic token cap")
						}
						_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","name":"structured_output","input":` + text + `}]}`))
					} else {
						if req["options"].(map[string]any)["num_predict"] != float64(1024) {
							t.Error("missing Ollama token cap")
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": text}})
					}
				}))
				defer server.Close()
				p := newHTTPLLM(kind, server.URL, "model", "", 4096, 0, 5, "function_calling")
				out, err := p.CompleteStructuredContext(t.Context(), nil, JSONSchema{}, CallOptions{MaxOutputTokens: 1024, MaxOutputBytes: 1024})
				if large {
					if !errors.Is(err, ErrOutputTooLarge) || out.Data != nil || out.RawOutputBytes != len(text) {
						t.Fatalf("raw native cap %+v %v", out, err)
					}
				} else if err != nil || out.RawOutputBytes != len(text) || out.Data["ok"] != true {
					t.Fatalf("native result %+v %v", out, err)
				}
			})
		}
	}
}
