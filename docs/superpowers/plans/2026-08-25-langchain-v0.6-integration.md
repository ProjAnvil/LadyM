# langchain-golang v0.6.0 Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade ladyM to langchain-golang v0.6.0 and integrate the two new capabilities ladyM actually consumes: OpenAI `reasoning_effort` and OpenAI-native JSON mode for structured output.

**Architecture:** ladyM is a memory library; it consumes langchain-golang only through `providers/` (partner chat models via `makePartnerChatModel` + `LangChainLLM`) and re-exports host-facing LangGraph helpers (`langgraph/`). The v0.5.1–v0.6.0 release adds (a) `ChatModel.WithReasoningEffort` on the OpenAI partner and (b) `ChatModel.WithJSONMode` / `WithResponseFormat` on the OpenAI partner. Everything else in the release (checkpoint savers, prebuilt agents, RecursionLimit, streaming, token counting, embedding batching, shell middleware) is host-facing API surface with no call site inside this repo — ladyM compiles no StateGraph internally, never streams, and does its own embedding HTTP.

**Tech Stack:** Go 1.x, langchain-golang v0.6.0, net/http/httptest for request-capture tests.

## Global Constraints

- No git commits / branch mutations — reviews use working-tree diffs against baseline `061d111` (repo convention from previous rounds).
- Code comments in English only (guarded by `scripts/check-comments.sh`, run via `make lint-comments`).
- Both build tags must stay green: `go build ./...` and `go build -tags enterprise ./...`, plus `go vet ./...`.
- Test patterns: mirror `config/store_config_test.go` (config tests) and existing `providers/langchain_test.go` / `providers/http_llm_test.go` (provider tests). Tests must run with `go test ./...` on the personal edition without PG.
- TDD: every behavior change starts with a failing test that is observed to fail before the implementation is written.
- `MakeLLMProvider` and `makePartnerChatModel` signatures change (new `reasoningEffort` parameter) — update ALL existing callers/tests to the new signature (interface-change adaptation; do not change their existing assertions otherwise).
- `reasoning_effort` valid values: `""` (unset), `"low"`, `"medium"`, `"high"` (case-insensitive, trimmed). Anything else is an error: `unknown reasoning_effort %q (supported: low, medium, high)`.
- `reasoning_effort` is an OpenAI-only knob: anthropic / ollama / http providers accept and silently ignore it (documented in comments), matching how non-reasoning OpenAI models ignore the field.

---

### Task 1: langchain-golang v0.6.0 upgrade + regression verify

**Files:**
- Modify: `go.mod`, `go.sum` (already bumped to v0.6.0 in the working tree)

**Interfaces:**
- Consumes: nothing
- Produces: `github.com/projanvil/langchain-golang v0.6.0` in `go.mod`; green baseline for all later tasks

- [ ] **Step 1: Verify the dependency pin**

Run: `grep 'projanvil/langchain-golang' go.mod`
Expected: `github.com/projanvil/langchain-golang v0.6.0`

- [ ] **Step 2: Build both editions + vet**

Run:
```bash
go build ./... && go build -tags enterprise ./... && go vet ./...
```
Expected: all exit 0, no output

- [ ] **Step 3: Full personal-edition test suite**

Run: `go test ./...`
Expected: all packages `ok` (PG-gated cases skip without `LADYM_TEST_PG_DSN` — that is normal)

- [ ] **Step 4: Enterprise-edition suite + comment lint**

Run: `go test -tags enterprise ./... && make lint-comments`
Expected: all `ok`; lint passes

- [ ] **Step 5: Confirm no checkpoint.Saver breakage**

v0.6.0's only breaking change adds methods to `langgraph/checkpoint.Saver`. Confirm ladyM neither imports the checkpoint package nor implements the interface.

Run: `grep -rn "checkpoint" --include='*.go' . | grep -v _test.go || echo CLEAN`
Expected: `CLEAN`

---

### Task 2: `reasoning_effort` config plumbing (config + providers)

**Files:**
- Modify: `config/config.go` (LLMConfig struct ~:97, flat fields ~:184, Default() ~:252, applyToml llm case ~:505, applyFlat ~:757, syncNested ~:800, applyEnv ~:853)
- Create: `config/llm_reasoning_test.go`
- Modify: `providers/agents.go` (AgentConfig :22-34, Get :110-122, MakeAgent :166)
- Modify: `providers/agents_test.go`
- Modify: `providers/llm.go` (MakeLLMProvider :374-393)
- Modify: `providers/langchain.go` (makePartnerChatModel :102-132)
- Modify: `providers/http_llm_test.go` (TestMakeLLMProvider :290-330 — signature update only)
- Modify: `providers/langchain_test.go` (TestMakePartnerChatModel :140-163 — signature update; new capture test)
- Create: `providers/reasoning_effort_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 beyond the v0.6.0 pin
- Produces:
  - `config.Config.LLMReasoningEffort string` (flat) and `config.LLMConfig.ReasoningEffort string` (nested mirror)
  - TOML key `[llm] reasoning_effort`, flat key `llm_reasoning_effort`, env var `LADYM_LLM_REASONING_EFFORT`
  - `providers.AgentConfig.ReasoningEffort string`, per-op override key `reasoning_effort`
  - `providers.MakeLLMProvider(kind, baseURL, model, apiKey, structuredMethod, reasoningEffort string, maxTokens int, temperature, timeoutS float64) (LLMProvider, error)`
  - `makePartnerChatModel(kind, baseURL, model, apiKey string, maxTokens int, temperature, timeoutS float64, reasoningEffort string) (language.ChatModel, error)`

- [ ] **Step 1: Write the failing config tests**

Create `config/llm_reasoning_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// llm reasoning_effort: defaults / flat keys / [llm] table / env / nested
// ---------------------------------------------------------------------------

func TestLLMReasoningEffortDefaults(t *testing.T) {
	t.Setenv("LADYM_LLM_REASONING_EFFORT", "")
	cfg := Default()
	if cfg.LLMReasoningEffort != "" {
		t.Errorf("LLMReasoningEffort = %q, want empty", cfg.LLMReasoningEffort)
	}
	if cfg.LLM.ReasoningEffort != "" {
		t.Errorf("nested LLM.ReasoningEffort = %q, want empty", cfg.LLM.ReasoningEffort)
	}
}

func TestApplyFlatReasoningEffortKey(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{"llm_reasoning_effort": "low"})
	if cfg.LLMReasoningEffort != "low" {
		t.Errorf("LLMReasoningEffort = %q, want low", cfg.LLMReasoningEffort)
	}
}

func TestApplyTomlLLMReasoningEffort(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"llm": map[string]any{"reasoning_effort": "high"},
	})
	if cfg.LLMReasoningEffort != "high" {
		t.Errorf("LLMReasoningEffort = %q, want high", cfg.LLMReasoningEffort)
	}
}

func TestApplyEnvReasoningEffort(t *testing.T) {
	t.Setenv("LADYM_LLM_REASONING_EFFORT", "medium")
	cfg := Default()
	if cfg.LLMReasoningEffort != "medium" {
		t.Errorf("LLMReasoningEffort = %q, want medium", cfg.LLMReasoningEffort)
	}
}

func TestLLMReasoningEffortNestedMirrorSync(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{"llm_reasoning_effort": "low"})
	syncNested(cfg)
	if cfg.LLM.ReasoningEffort != "low" {
		t.Errorf("nested LLM.ReasoningEffort = %q, want low", cfg.LLM.ReasoningEffort)
	}
}

func TestFromFileLLMReasoningEffort(t *testing.T) {
	t.Setenv("LADYM_LLM_REASONING_EFFORT", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	content := "[llm]\nreasoning_effort = \"high\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMReasoningEffort != "high" {
		t.Errorf("LLMReasoningEffort = %q, want high", cfg.LLMReasoningEffort)
	}
	if cfg.LLM.ReasoningEffort != "high" {
		t.Errorf("nested LLM.ReasoningEffort = %q, want high", cfg.LLM.ReasoningEffort)
	}
}
```

- [ ] **Step 2: Run config tests to verify they fail**

Run: `go test ./config/ -run ReasoningEffort -v`
Expected: FAIL — `cfg.LLMReasoningEffort undefined` / `unknown field` compile errors (fields don't exist yet)

- [ ] **Step 3: Implement config plumbing**

In `config/config.go`:

1. `LLMConfig` struct — add the field after `StructuredMethod`:
```go
type LLMConfig struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKeyEnv        string
	MaxTokens        int
	Temperature      float64
	StructuredMethod string
	ReasoningEffort  string
	TimeoutS         float64
}
```

2. Flat fields — add after `LLMStructuredMethod`:
```go
	LLMReasoningEffort      string // "low" | "medium" | "high" for OpenAI reasoning models; "" = provider default
```

3. `Default()` — add to the literal:
```go
		LLMReasoningEffort:      "",
```

4. `applyFlat` — add the case next to `llm_structured_method`:
```go
	case "llm_reasoning_effort":
		cfg.LLMReasoningEffort = asString(v)
```

5. `applyToml` llm case — extend the key list:
```go
				case "provider", "base_url", "model", "api_key", "api_key_env",
					"max_tokens", "temperature", "structured_method", "reasoning_effort", "timeout_s":
```

6. `syncNested` — add to the `cfg.LLM = LLMConfig{...}` literal:
```go
		ReasoningEffort:  cfg.LLMReasoningEffort,
```

7. `applyEnv` — add next to the other `LADYM_LLM_*` vars:
```go
	if v := os.Getenv("LADYM_LLM_REASONING_EFFORT"); v != "" {
		cfg.LLMReasoningEffort = v
	}
```

- [ ] **Step 4: Run config tests to verify they pass**

Run: `go test ./config/`
Expected: PASS, all tests

- [ ] **Step 5: Write the failing provider tests**

Create `providers/reasoning_effort_test.go`:

```go
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
	cfg := testConfig() // existing helper in agents_test.go
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
```

NOTE for the implementer: check the actual config-fixture helper name in `providers/agents_test.go` before writing `TestAgentConfigReasoningEffort` — if it is not `testConfig()`, use the existing one verbatim.

Also update `providers/agents_test.go:30` and :76 existing assertions ONLY if they compile-break from the new field (they don't — additive field). No changes needed there beyond what compilation demands.

- [ ] **Step 6: Run provider tests to verify they fail**

Run: `go test ./providers/ -run 'ReasoningEffort' -v`
Expected: FAIL — `makePartnerChatModel` arity mismatch / `MakeLLMProvider` arity mismatch / `ac.ReasoningEffort undefined` compile errors

- [ ] **Step 7: Implement provider plumbing**

In `providers/agents.go`:

1. `AgentConfig` — add after `StructuredMethod`:
```go
	ReasoningEffort  string
```
2. `Get` — add to the returned literal:
```go
		ReasoningEffort:  get("reasoning_effort", r.cfg.LLMReasoningEffort),
```
3. `MakeAgent` — new argument:
```go
	return MakeLLMProvider(ac.Provider, ac.BaseURL, ac.Model, apiKey, ac.StructuredMethod, ac.ReasoningEffort, ac.MaxTokens, ac.Temperature, ac.TimeoutS)
```

In `providers/llm.go`:

1. Add the validator next to `normalizedStructuredMethod`:
```go
// normalizedReasoningEffort validates the configured reasoning effort.
// Valid values mirror OpenAI's reasoning_effort levels: "low", "medium",
// "high"; "" leaves the provider default. Unknown values are an error.
func normalizedReasoningEffort(e string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(e)); v {
	case "", "low", "medium", "high":
		return v, nil
	default:
		return "", fmt.Errorf("unknown reasoning_effort %q (supported: low, medium, high)", e)
	}
}
```

2. `MakeLLMProvider` — new signature + validation + pass-through:
```go
// MakeLLMProvider builds a provider for the given kind. Returns nil for
// "none". "openai" / "anthropic" / "ollama" are backed by langchain-golang
// partner chat models (mirroring Python's LangChain-based construction);
// "http" selects the legacy hand-rolled HTTPLLM (OpenAI-compatible
// /chat/completions) as a zero-framework escape hatch. reasoningEffort
// ("low"|"medium"|"high"|"") applies only to the OpenAI partner; the other
// kinds accept and ignore it.
func MakeLLMProvider(kind, baseURL, model, apiKey, structuredMethod, reasoningEffort string, maxTokens int, temperature, timeoutS float64) (LLMProvider, error) {
	k := strings.ToLower(kind)
	if k == "" {
		k = "none"
	}
	effort, err := normalizedReasoningEffort(reasoningEffort)
	if err != nil {
		return nil, err
	}
	switch k {
	case "none":
		return nil, nil
	case "http":
		return newHTTPLLM("openai", baseURL, model, apiKey, maxTokens, temperature, timeoutS, structuredMethod), nil
	case "openai", "anthropic", "ollama":
		cm, err := makePartnerChatModel(k, baseURL, model, apiKey, maxTokens, temperature, timeoutS, effort)
		if err != nil {
			return nil, err
		}
		return WrapChatModel(cm, structuredMethod), nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", kind)
	}
}
```

In `providers/langchain.go` — `makePartnerChatModel` gains the trailing parameter and the openai case binds it:
```go
// makePartnerChatModel builds a langchain-golang partner ChatModel from
// ladyM's LLM config values. kind is "openai" | "anthropic" | "ollama".
// reasoningEffort is OpenAI-only (v0.5.3+): anthropic and ollama ignore it.
func makePartnerChatModel(kind, baseURL, model, apiKey string, maxTokens int, temperature, timeoutS float64, reasoningEffort string) (language.ChatModel, error) {
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
		cm := openai.NewChatModel(opts...).WithChatCompletions()
		if reasoningEffort != "" {
			cm = cm.WithReasoningEffort(reasoningEffort)
		}
		return cm, nil
	case "anthropic":
		return anthropic.NewChatModel(opts...), nil
	case "ollama":
		return ollama.NewChatModel(opts...), nil
	default:
		return nil, fmt.Errorf("unknown llm provider: %s", kind)
	}
}
```

3. Signature adaptations in existing tests (no assertion changes):
- `providers/http_llm_test.go` TestMakeLLMProvider: every `MakeLLMProvider(kind, "", "m", "k", "", 1, 0, 1)`-style call gains one `""` argument after the structured-method argument, e.g. `MakeLLMProvider(kind, "", "m", "k", "", "", 1, 0, 1)`.
- `providers/langchain_test.go` TestMakePartnerChatModel: every `makePartnerChatModel(...)` call gains a trailing `""`.

- [ ] **Step 8: Run provider + config tests to verify they pass**

Run: `go test ./providers/ ./config/`
Expected: PASS, all tests

- [ ] **Step 9: Full regression**

Run: `go build ./... && go build -tags enterprise ./... && go vet ./... && go test ./...`
Expected: all green

---

### Task 3: OpenAI-native JSON mode in LangChainLLM structured output

**Files:**
- Modify: `providers/langchain.go` (CompleteStructured json_mode branch :70-78)
- Create: `providers/json_mode_test.go`

**Interfaces:**
- Consumes: `openai.ChatModel.WithJSONMode()` (v0.6.0), `WrapChatModel` (unchanged)
- Produces: `LangChainLLM.CompleteStructured` with `structured_method="json_mode"` sends `response_format={"type":"json_object"}` when the wrapped model is an `openai.ChatModel`, in addition to the existing schema system prompt; non-OpenAI models keep the prompt-only path unchanged

- [ ] **Step 1: Write the failing tests**

Create `providers/json_mode_test.go`:

```go
package providers

import (
	"encoding/json"
	"fmt"
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
```

(`io` import needed; `testSchema()` already exists in the package's tests.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./providers/ -run 'JSONModeSendsNative|JSONModeNativeInvokeError' -v`
Expected: FAIL — `TestJSONModeSendsNativeResponseFormat` reports `response_format = nil` (prompt-only path sends no response_format). `TestJSONModeNativeInvokeError` may already pass; if it does, keep it (it pins the error path) and note that in the report.

- [ ] **Step 3: Implement the native json_mode branch**

In `providers/langchain.go`, `CompleteStructured`, replace the `method == "json_mode"` branch:

```go
	if method == "json_mode" {
		// Prompt-based schema instruction, mirroring HTTPLLM json_mode; when
		// the wrapped model is an OpenAI partner model, additionally bind the
		// provider-native response_format={"type":"json_object"} (v0.6.0
		// WithJSONMode) so the schema is enforced upstream too.
		sys := lcmessages.System("Reply ONLY with JSON matching this JSON schema: " + schemaPromptText(sc))
		msgs := append([]lcmessages.Message{sys}, lcMsgs...)
		cm := l.cm
		if om, ok := cm.(openai.ChatModel); ok {
			cm = om.WithJSONMode()
		}
		resp, err := cm.Invoke(context.Background(), msgs)
		if err != nil {
			return nil, err
		}
		text = lcmessages.Text(resp)
	} else {
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./providers/`
Expected: PASS, all tests — including the pre-existing `TestLangChainStructuredJSONMode` (FakeChatModel fallback path unchanged) and `TestLangChainStructuredInvokeErrors` (errChatModel is not an openai.ChatModel, so it still hits the plain branch)

- [ ] **Step 5: Full regression**

Run: `go build ./... && go build -tags enterprise ./... && go vet ./... && go test ./... && make lint-comments`
Expected: all green

---

### Task 4: Coverage gate ≥95% + final regression (controller-run, no subagent)

**Files:**
- Create (only if the gate fails): additional `*_test.go` top-ups in the under-covered package

**Interfaces:**
- Consumes: all of Tasks 1-3
- Produces: total statement coverage ≥ 95.0% (personal edition + live PG, the same口径 as the previous 95.9% baseline); `providers` and `config` packages each ≥ 95%

- [ ] **Step 1: Start the PG test container (pgvector)**

```bash
docker rm -f ladym-pg 2>/dev/null; docker run -d --name ladym-pg \
  -p 55432:5432 -e POSTGRES_PASSWORD=ladym -e POSTGRES_DB=ladym \
  pgvector/pgvector:pg16
# wait for readiness
for i in $(seq 1 30); do docker exec ladym-pg pg_isready -U postgres && break; sleep 1; done
```
Expected: `accepting connections`

- [ ] **Step 2: Measure coverage**

```bash
LADYM_TEST_PG_DSN='postgres://postgres:ladym@127.0.0.1:55432/ladym?sslmode=disable' \
  go test -coverprofile=/tmp/ladym-cover.out ./...
go tool cover -func=/tmp/ladym-cover.out | tail -1
go tool cover -func=/tmp/ladym-cover.out | grep -E 'LadyM/(providers|config)/' | awk '$3+0 < 95.0'
```
Expected: total ≥ 95.0%; the grep prints nothing (every providers/config function ≥ 95%)

- [ ] **Step 3: Top up if short**

If any function in `providers` or `config` is below 95%, or the total is below 95.0%, write focused tests for exactly those functions (test-only changes; no production edits), then re-run Step 2. If a function is genuinely unreachable dead code, report it — do NOT delete production code in this task.

- [ ] **Step 4: Final full regression**

```bash
make test-all
```
Expected: personal + PG + enterprise + lint all green
