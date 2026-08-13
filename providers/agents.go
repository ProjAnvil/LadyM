package providers

import (
	"fmt"
	"os"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/secrets"
)

// NAMED_OPS is the set of cognitive operations that can bind an LLM provider.
var NAMED_OPS = []string{
	"consolidate",
	"proceduralize",
	"attention_gate",
	"l5_mental_model",
	"l6_forward_intent",
}

// AgentConfig is the resolved per-operation LLM configuration. Provider ==
// "none" (or "") means heuristic mode — no LLM call is made.
type AgentConfig struct {
	Op               string
	Provider         string
	BaseURL          string
	Model            string
	APIKeyEnv        string
	PromptTemplate   string
	MaxTokens        int
	Temperature      float64
	StructuredMethod string
	TimeoutS         float64
	APIKey           string
}

// AgentRegistry builds AgentConfig by layering per-op overrides on [llm] globals.
type AgentRegistry struct {
	cfg *config.Config
}

// NewAgentRegistry builds an AgentRegistry over cfg.
func NewAgentRegistry(cfg *config.Config) *AgentRegistry { return &AgentRegistry{cfg: cfg} }

func knownOp(op string) bool {
	for _, o := range NAMED_OPS {
		if o == op {
			return true
		}
	}
	return false
}

// Get resolves the AgentConfig for op.
func (r *AgentRegistry) Get(op string) (*AgentConfig, error) {
	if !knownOp(op) {
		return nil, fmt.Errorf("unknown op %q; expected one of %v", op, NAMED_OPS)
	}
	overrides := map[string]any{}
	if ov, ok := r.cfg.AgentsOverrides[op]; ok {
		overrides = ov
	}
	get := func(k, def string) string {
		if v, ok := overrides[k]; ok {
			if s, ok2 := v.(string); ok2 {
				return s
			}
			return fmt.Sprintf("%v", v)
		}
		return def
	}
	getInt := func(k string, def int) int {
		if v, ok := overrides[k]; ok {
			switch t := v.(type) {
			case int64:
				return int(t)
			case int:
				return t
			case float64:
				return int(t)
			case string:
				var n int
				fmt.Sscanf(t, "%d", &n)
				return n
			}
		}
		return def
	}
	getFloat := func(k string, def float64) float64 {
		if v, ok := overrides[k]; ok {
			switch t := v.(type) {
			case float64:
				return t
			case int64:
				return float64(t)
			case int:
				return float64(t)
			case string:
				var f float64
				fmt.Sscanf(t, "%g", &f)
				return f
			}
		}
		return def
	}

	provider := get("provider", r.cfg.LLMProvider)
	if provider == "" {
		provider = "none"
	}
	return &AgentConfig{
		Op:               op,
		Provider:         provider,
		BaseURL:          get("base_url", r.cfg.LLMBaseURL),
		Model:            get("model", r.cfg.LLMModel),
		APIKey:           get("api_key", r.cfg.LLMAPIKey),
		APIKeyEnv:        get("api_key_env", r.cfg.LLMAPIKeyEnv),
		PromptTemplate:   get("prompt_template", ""),
		MaxTokens:        getInt("max_tokens", r.cfg.LLMMaxTokens),
		Temperature:      getFloat("temperature", r.cfg.LLMTemperature),
		StructuredMethod: get("structured_method", r.cfg.LLMStructuredMethod),
		TimeoutS:         getFloat("timeout_s", r.cfg.LLMTimeoutS),
	}, nil
}

func missingKeyMsg(provider, envName string) string {
	return fmt.Sprintf(
		`LLM provider "%s" needs an API key but "%s" is neither registered in the secret store nor set as an environment variable. Run `+"`ladym config set-master-key`"+` then `+"`ladym config set %s <value>`"+`, or set llm.provider="none" in ladym.toml for offline mode.`,
		provider, envName, envName)
}

func resolveAPIKey(plaintext, envName string) (string, error) {
	if plaintext != "" {
		return plaintext, nil
	}
	if envName != "" {
		v, err := secrets.NewStore("").Get(envName)
		if err != nil {
			return "", err
		}
		if v != "" {
			return v, nil
		}
		return os.Getenv(envName), nil
	}
	return "", nil
}

// MakeAgent builds (or skips) the LLM provider bound to one operation.
// Returns nil for heuristic mode (provider "none"). Fails fast (ConfigError)
// when a provider is configured but no key resolves.
func MakeAgent(cfg *config.Config, op string) (LLMProvider, error) {
	ac, err := NewAgentRegistry(cfg).Get(op)
	if err != nil {
		return nil, err
	}
	if ac.Provider == "none" {
		return nil, nil
	}
	apiKey, err := resolveAPIKey(ac.APIKey, ac.APIKeyEnv)
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, &config.ConfigError{Msg: missingKeyMsg(ac.Provider, ac.APIKeyEnv)}
	}
	return MakeLLMProvider(ac.Provider, ac.BaseURL, ac.Model, apiKey, ac.StructuredMethod, ac.MaxTokens, ac.Temperature, ac.TimeoutS)
}
