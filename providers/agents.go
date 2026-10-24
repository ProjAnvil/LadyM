package providers

import (
	"cmp"
	"fmt"
	"os"
	"slices"

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
	"l4_backfill",
	"l4_link",
	"l4_alias_merge",
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
	ReasoningEffort  string
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
	return slices.Contains(NAMED_OPS, op)
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
	temperature, err := config.ParseFloatValue("llm.temperature", r.cfg.LLMTemperature)
	if err != nil {
		return nil, err
	}
	timeoutS, err := config.ParseFloatValue("llm.timeout_s", r.cfg.LLMTimeoutS)
	if err != nil {
		return nil, err
	}
	maxTokens := r.cfg.LLMMaxTokens
	if v, ok := overrides["max_tokens"]; ok {
		maxTokens, err = config.ParseIntValue("agents."+op+".max_tokens", v)
		if err != nil {
			return nil, err
		}
	}
	if v, ok := overrides["temperature"]; ok {
		temperature, err = config.ParseFloatValue("agents."+op+".temperature", v)
		if err != nil {
			return nil, err
		}
	}
	if v, ok := overrides["timeout_s"]; ok {
		timeoutS, err = config.ParseFloatValue("agents."+op+".timeout_s", v)
		if err != nil {
			return nil, err
		}
	}

	provider := get("provider", r.cfg.LLMProvider)
	provider = cmp.Or(provider, "none")
	return &AgentConfig{
		Op:               op,
		Provider:         provider,
		BaseURL:          get("base_url", r.cfg.LLMBaseURL),
		Model:            get("model", r.cfg.LLMModel),
		APIKey:           get("api_key", r.cfg.LLMAPIKey),
		APIKeyEnv:        get("api_key_env", r.cfg.LLMAPIKeyEnv),
		PromptTemplate:   get("prompt_template", ""),
		MaxTokens:        maxTokens,
		Temperature:      temperature,
		StructuredMethod: get("structured_method", r.cfg.LLMStructuredMethod),
		ReasoningEffort:  get("reasoning_effort", r.cfg.LLMReasoningEffort),
		TimeoutS:         timeoutS,
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
	return MakeLLMProvider(ac.Provider, ac.BaseURL, ac.Model, apiKey, ac.StructuredMethod, ac.ReasoningEffort, ac.MaxTokens, ac.Temperature, ac.TimeoutS)
}
