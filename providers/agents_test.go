package providers

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/secrets"
)

func TestAgentRegistryGetDefaults(t *testing.T) {
	cfg := config.Default()
	reg := NewAgentRegistry(cfg)
	for _, op := range NAMED_OPS {
		ac, err := reg.Get(op)
		if err != nil {
			t.Fatalf("Get(%q): %v", op, err)
		}
		if ac.Op != op {
			t.Errorf("Op=%q, want %q", ac.Op, op)
		}
		if ac.Provider != cfg.LLMProvider {
			t.Errorf("Provider=%q, want %q", ac.Provider, cfg.LLMProvider)
		}
		if ac.Model != cfg.LLMModel || ac.MaxTokens != cfg.LLMMaxTokens ||
			ac.Temperature != cfg.LLMTemperature || ac.TimeoutS != cfg.LLMTimeoutS ||
			ac.StructuredMethod != cfg.LLMStructuredMethod {
			t.Errorf("Get(%q) did not inherit [llm] globals: %+v", op, ac)
		}
	}
}

func TestAgentRegistryUnknownOp(t *testing.T) {
	reg := NewAgentRegistry(config.Default())
	_, err := reg.Get("not_an_op")
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Fatalf("expected unknown-op error, got %v", err)
	}
}

// MaintainL4's three LLM sub-steps are first-class ops: registered in
// NAMED_OPS, resolvable through the registry, and overridable via [agents].
func TestL4MaintenanceOpsRegistered(t *testing.T) {
	cfg := config.Default()
	cfg.LLMProvider = "http"
	cfg.AgentsOverrides["l4_link"] = map[string]any{"model": "linker"}
	reg := NewAgentRegistry(cfg)
	for _, op := range []string{"l4_backfill", "l4_link", "l4_alias_merge"} {
		ac, err := reg.Get(op)
		if err != nil {
			t.Fatalf("Get(%q): %v", op, err)
		}
		if ac.Provider != "http" {
			t.Errorf("Get(%q).Provider = %q, want http (inherits [llm] globals)", op, ac.Provider)
		}
	}
	ac, err := reg.Get("l4_link")
	if err != nil {
		t.Fatal(err)
	}
	if ac.Model != "linker" {
		t.Errorf("l4_link model = %q, want linker ([agents] override)", ac.Model)
	}
}

func TestAgentRegistryEmptyProviderMeansNone(t *testing.T) {
	cfg := config.Default()
	cfg.LLMProvider = ""
	ac, err := NewAgentRegistry(cfg).Get("consolidate")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ac.Provider != "none" {
		t.Fatalf("Provider=%q, want none", ac.Provider)
	}
}

func TestAgentRegistryOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.AgentsOverrides["consolidate"] = map[string]any{
		"provider":          "http",
		"base_url":          "http://localhost:9999",
		"model":             "override-model",
		"api_key":           "plain-key",
		"api_key_env":       "MY_KEY",
		"prompt_template":   "do {op}",
		"max_tokens":        int64(42),
		"temperature":       0.9,
		"structured_method": "json_mode",
		"timeout_s":         5.5,
	}
	ac, err := NewAgentRegistry(cfg).Get("consolidate")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ac.Provider != "http" || ac.BaseURL != "http://localhost:9999" || ac.Model != "override-model" ||
		ac.APIKey != "plain-key" || ac.APIKeyEnv != "MY_KEY" || ac.PromptTemplate != "do {op}" ||
		ac.MaxTokens != 42 || ac.Temperature != 0.9 || ac.StructuredMethod != "json_mode" || ac.TimeoutS != 5.5 {
		t.Fatalf("overrides not applied: %+v", ac)
	}

	// Overrides for one op must not leak into another.
	other, err := NewAgentRegistry(cfg).Get("proceduralize")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if other.Provider != cfg.LLMProvider || other.Model != cfg.LLMModel {
		t.Fatalf("overrides leaked across ops: %+v", other)
	}
}

func TestAgentRegistryOverrideTypeCoercion(t *testing.T) {
	cases := []struct {
		name      string
		overrides map[string]any
		check     func(t *testing.T, ac *AgentConfig)
	}{
		{
			name:      "getInt from int",
			overrides: map[string]any{"max_tokens": 7},
			check:     func(t *testing.T, ac *AgentConfig) { wantInt(t, ac.MaxTokens, 7) },
		},
		{
			name:      "getInt from float64",
			overrides: map[string]any{"max_tokens": 8.0},
			check:     func(t *testing.T, ac *AgentConfig) { wantInt(t, ac.MaxTokens, 8) },
		},
		{
			name:      "getInt from string",
			overrides: map[string]any{"max_tokens": "9"},
			check:     func(t *testing.T, ac *AgentConfig) { wantInt(t, ac.MaxTokens, 9) },
		},
		{
			name:      "getFloat from int64",
			overrides: map[string]any{"temperature": int64(1)},
			check:     func(t *testing.T, ac *AgentConfig) { wantFloat(t, ac.Temperature, 1.0) },
		},
		{
			name:      "getFloat from int",
			overrides: map[string]any{"timeout_s": 2},
			check:     func(t *testing.T, ac *AgentConfig) { wantFloat(t, ac.TimeoutS, 2.0) },
		},
		{
			name:      "getFloat from string",
			overrides: map[string]any{"temperature": "0.5"},
			check:     func(t *testing.T, ac *AgentConfig) { wantFloat(t, ac.Temperature, 0.5) },
		},
		{
			name:      "get from non-string renders value",
			overrides: map[string]any{"model": 123},
			check: func(t *testing.T, ac *AgentConfig) {
				if ac.Model != "123" {
					t.Fatalf("Model=%q, want rendered 123", ac.Model)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.AgentsOverrides["consolidate"] = c.overrides
			ac, err := NewAgentRegistry(cfg).Get("consolidate")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			c.check(t, ac)
		})
	}
}

func TestAgentRegistryRejectsInvalidNumericOverrides(t *testing.T) {
	for _, field := range []string{"max_tokens", "temperature", "timeout_s"} {
		t.Run(field, func(t *testing.T) {
			invalid := []any{nil, true, "", "bad", "9junk", "NaN", "Inf", math.NaN(), math.Inf(-1), []int{1}}
			if field == "max_tokens" {
				invalid = append(invalid, 8.5, "9.5", "999999999999999999999999999")
			}
			for _, value := range invalid {
				cfg := config.Default()
				cfg.AgentsOverrides["consolidate"] = map[string]any{field: value}
				ac, err := NewAgentRegistry(cfg).Get("consolidate")
				if _, ok := errors.AsType[*config.ConfigError](err); !ok {
					t.Fatalf("Get(%s=%v) error = %v, want ConfigError", field, value, err)
				}
				if ac != nil || !strings.Contains(err.Error(), "agents.consolidate."+field) {
					t.Fatalf("invalid result: config=%v, error=%v", ac, err)
				}
				agent, err := MakeAgent(cfg, "consolidate")
				if _, ok := errors.AsType[*config.ConfigError](err); !ok || agent != nil {
					t.Fatalf("provider=none must not hide numeric errors: agent=%v, err=%v", agent, err)
				}
			}
		})
	}
}

func TestAgentRegistryRejectsInvalidGlobalNumericValues(t *testing.T) {
	for _, field := range []string{"temperature", "timeout_s"} {
		cfg := config.Default()
		if field == "temperature" {
			cfg.LLMTemperature = math.NaN()
		} else {
			cfg.LLMTimeoutS = math.Inf(1)
		}
		cfg.AgentsOverrides["consolidate"] = map[string]any{field: 1.0}
		_, err := NewAgentRegistry(cfg).Get("consolidate")
		if _, ok := errors.AsType[*config.ConfigError](err); !ok {
			t.Fatalf("invalid inherited %s masked by override: %v", field, err)
		}
		if !strings.Contains(err.Error(), "llm."+field) {
			t.Errorf("error = %v, want global field %s", err, field)
		}
	}
}

func TestAgentRegistryPreservesNumericZeroAndNegativeValues(t *testing.T) {
	for _, value := range []int{0, -2} {
		cfg := config.Default()
		cfg.AgentsOverrides["consolidate"] = map[string]any{
			"max_tokens": value, "temperature": value, "timeout_s": value,
		}
		ac, err := NewAgentRegistry(cfg).Get("consolidate")
		if err != nil {
			t.Fatal(err)
		}
		if ac.MaxTokens != value || ac.Temperature != float64(value) || ac.TimeoutS != float64(value) {
			t.Errorf("explicit numeric values replaced by defaults: %+v", ac)
		}
	}
}

func TestMakeAgentNumericErrorPrecedesSecretResolution(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(home, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.LLMProvider = "http"
	cfg.LLMAPIKeyEnv = "LADYM_TEST_UNRESOLVABLE_KEY"
	cfg.AgentsOverrides["consolidate"] = map[string]any{"timeout_s": "bad"}
	_, err := MakeAgent(cfg, "consolidate")
	if _, ok := errors.AsType[*config.ConfigError](err); !ok || !strings.Contains(err.Error(), "timeout_s") {
		t.Fatalf("numeric error must precede secret access: %v", err)
	}
}

func wantInt(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func wantFloat(t *testing.T, got, want float64) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMissingKeyMsg(t *testing.T) {
	msg := missingKeyMsg("openai", "OPENAI_API_KEY")
	for _, want := range []string{"openai", "OPENAI_API_KEY", "set-master-key"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missingKeyMsg missing %q: %s", want, msg)
		}
	}
}

func TestResolveAPIKey(t *testing.T) {
	t.Run("plaintext wins", func(t *testing.T) {
		got, err := resolveAPIKey("plain", "SOME_ENV")
		if err != nil || got != "plain" {
			t.Fatalf("resolveAPIKey=(%q, %v)", got, err)
		}
	})
	t.Run("no env name returns empty", func(t *testing.T) {
		got, err := resolveAPIKey("", "")
		if err != nil || got != "" {
			t.Fatalf("resolveAPIKey=(%q, %v)", got, err)
		}
	})
	t.Run("falls back to environment variable", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir()) // isolate the secret store
		t.Setenv("LADYM_TEST_RESOLVE_KEY", "from-env")
		got, err := resolveAPIKey("", "LADYM_TEST_RESOLVE_KEY")
		if err != nil || got != "from-env" {
			t.Fatalf("resolveAPIKey=(%q, %v)", got, err)
		}
	})
	t.Run("missing everywhere returns empty", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		got, err := resolveAPIKey("", "LADYM_TEST_DEFINITELY_UNSET")
		if err != nil || got != "" {
			t.Fatalf("resolveAPIKey=(%q, %v)", got, err)
		}
	})
	t.Run("secret store hit wins over environment", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("LADYM_TEST_STORED_KEY", "from-env")
		store := secrets.NewStore(filepath.Join(home, ".ladyM"))
		if _, err := store.SetMasterKey("test-master"); err != nil {
			t.Fatalf("SetMasterKey: %v", err)
		}
		if err := store.Set("LADYM_TEST_STORED_KEY", "from-store"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, err := resolveAPIKey("", "LADYM_TEST_STORED_KEY")
		if err != nil || got != "from-store" {
			t.Fatalf("resolveAPIKey=(%q, %v), want (from-store, nil)", got, err)
		}
	})
	t.Run("corrupt secret store errors", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".ladyM")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "secrets.enc"), []byte("LADYM_TEST_BAD = !!!not-base64!!!\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveAPIKey("", "LADYM_TEST_BAD"); err == nil {
			t.Fatal("expected error from corrupt secrets.enc entry")
		}
	})
}

func TestMakeAgent(t *testing.T) {
	t.Run("unknown op errors", func(t *testing.T) {
		if _, err := MakeAgent(config.Default(), "not_an_op"); err == nil {
			t.Fatal("expected unknown-op error")
		}
	})
	t.Run("provider none returns nil", func(t *testing.T) {
		cfg := config.Default() // LLMProvider defaults to "none"
		p, err := MakeAgent(cfg, "consolidate")
		if err != nil || p != nil {
			t.Fatalf("MakeAgent=(%v, %v), want (nil, nil)", p, err)
		}
	})
	t.Run("plaintext key builds provider", func(t *testing.T) {
		cfg := config.Default()
		cfg.LLMProvider = "http"
		cfg.LLMAPIKey = "plain-key"
		p, err := MakeAgent(cfg, "consolidate")
		if err != nil {
			t.Fatalf("MakeAgent: %v", err)
		}
		if p == nil || p.Name() != "openai" {
			t.Fatalf("MakeAgent=%v, want openai-compatible HTTPLLM", p)
		}
	})
	t.Run("key from environment builds provider", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("LADYM_TEST_AGENT_KEY", "env-key")
		cfg := config.Default()
		cfg.LLMProvider = "http"
		cfg.LLMAPIKeyEnv = "LADYM_TEST_AGENT_KEY"
		p, err := MakeAgent(cfg, "consolidate")
		if err != nil || p == nil {
			t.Fatalf("MakeAgent=(%v, %v), want provider", p, err)
		}
	})
	t.Run("missing key is a ConfigError", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		cfg := config.Default()
		cfg.LLMProvider = "http"
		cfg.LLMAPIKeyEnv = "LADYM_TEST_AGENT_MISSING"
		_, err := MakeAgent(cfg, "consolidate")
		if err == nil {
			t.Fatal("expected missing-key error")
		}
		if _, ok := errors.AsType[*config.ConfigError](err); !ok {
			t.Fatalf("error is %T, want *config.ConfigError", err)
		}
	})
	t.Run("unknown provider errors", func(t *testing.T) {
		cfg := config.Default()
		cfg.LLMProvider = "bogus"
		cfg.LLMAPIKey = "k"
		if _, err := MakeAgent(cfg, "consolidate"); err == nil {
			t.Fatal("expected unknown-provider error")
		}
	})
	t.Run("secret store error propagates", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".ladyM")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "secrets.enc"), []byte("LADYM_TEST_AGENT_BAD = !!!not-base64!!!\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.LLMProvider = "http"
		cfg.LLMAPIKeyEnv = "LADYM_TEST_AGENT_BAD"
		if _, err := MakeAgent(cfg, "consolidate"); err == nil {
			t.Fatal("expected error from corrupt secret store")
		}
	})
}
