// Package config holds LadyM's runtime configuration.
//
// All values have sensible defaults so the engine works out-of-the-box with no
// env vars and no network. Anything that needs a key/model is an opt-in
// override.
//
// Configuration sources (highest precedence first):
//
//  1. cli_overrides passed to Load (e.g. from CLI flags).
//  2. Environment variables (LADYM_*).
//  3. The project file ./ladym.toml.
//  4. The global file ~/.ladym/config.toml.
//  5. The defaults (offline: hashing embedding, llm_provider == "none").
//
// Secret literals (api_key/*_key/token/secret/password) found in TOML are
// rejected with a warning and dropped — operators must use <name>_env
// indirection (e.g. llm.api_key_env = "MY_LLM_KEY") so secrets never land on
// disk.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ConfigError is raised when runtime configuration makes an operation
// impossible. The message MUST be actionable and one-line — CLI/MCP surface it
// verbatim instead of dumping a traceback. This is fail-fast, NOT a fallback.
type ConfigError struct {
	Msg string
}

func (e *ConfigError) Error() string { return e.Msg }

func configErrorf(format string, args ...any) *ConfigError {
	return &ConfigError{Msg: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Nested config structs
// ---------------------------------------------------------------------------

// ActivationWeights are the weights for the ACT-R-inspired activation function.
type ActivationWeights struct {
	Similarity       float64
	Recency          float64
	Frequency        float64
	Graph            float64
	TypeBoost        float64
	RecencyHalfLifeS float64
}

// RecallConfig holds two-tier retrieval knobs.
type RecallConfig struct {
	TopKTier1             int
	TopKTier2             int
	GraphHops             int
	ReflectionMinHits     int
	ReflectionMinCoverage float64
	EnableTier2           bool
}

// ConsolidateConfig holds knobs for L1→L2 consolidation.
type ConsolidateConfig struct {
	MinEpisodesToTrigger     int
	DedupSimilarityThreshold float64
}

// CodeIndexConfig holds knobs for codebase indexing.
type CodeIndexConfig struct {
	MaxBodyLinesPerSymbol int
	RespectGitignore      bool
	ExtraIgnoreGlobs      []string
	Languages             []string // nil = all supported
}

// EmbeddingConfig mirrors the flat embedding_* fields (populated by the loader).
type EmbeddingConfig struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKeyEnv        string
	Fallback         string
	QueryCacheSize   int
	TimeoutS         float64
	AllowDimChange   bool
	HTTPRequest      string
	HTTPResponsePath string
}

// LLMConfig mirrors the flat llm_* fields (populated by the loader).
type LLMConfig struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKeyEnv        string
	MaxTokens        int
	Temperature      float64
	StructuredMethod string
	TimeoutS         float64
}

// System2Config holds background reflection cycle knobs.
type System2Config struct {
	Enabled              bool
	IntervalS            int
	MinEpisodesToRun     int
	MaxConsecutiveErrors int
	L5ClusterSimilarity  float64
	L5MinClusterSize     int
	L5MergeSimilarity    float64
	L5MergeEveryNCycles  int
	L6MaxEpisodes        int
	L6HorizonS           float64
}

// AttentionConfig holds pre-write attention gate knobs.
type AttentionConfig struct {
	DedupWindowS float64
	NoiseWords   []string
}

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

// Config is the runtime configuration for LadyM. The flat fields are the
// source of truth for the provider factories; the nested structs are a
// convenience mirror populated by the loader.
type Config struct {
	DBPath          string
	Workspace       string
	PreferSQLiteVec bool
	EnableWAL       bool

	// embedding (flat — source of truth for make_provider)
	EmbeddingProvider         string
	EmbeddingModel            string
	EmbeddingDim              int
	EmbeddingBaseURL          string
	EmbeddingAPIKeyEnv        string
	EmbeddingFallback         string
	EmbeddingQueryCacheSize   int
	EmbeddingTimeoutS         float64
	EmbeddingAllowDimChange   bool
	EmbeddingHTTPRequest      string
	EmbeddingHTTPResponsePath string
	Embedding                 EmbeddingConfig

	// llm (flat — source of truth for make_agent)
	LLMProvider           string // "none" = heuristic / offline mode
	LLMBaseURL            string
	LLMModel              string
	LLMAPIKeyEnv          string
	LLMMaxTokens          int
	LLMTemperature        float64
	LLMStructuredMethod   string
	LLMTimeoutS           float64
	LLMAPIKey             string // plaintext LLM key (only when allow_plaintext_secrets)
	AllowPlaintextSecrets bool   // DEV/testing escape hatch; default stays secure
	LLM                   LLMConfig

	AgentsOverrides map[string]map[string]any
	Activation      ActivationWeights
	Recall          RecallConfig
	Consolidate     ConsolidateConfig
	CodeIndex       CodeIndexConfig
	System2         System2Config
	Attention       AttentionConfig
}

// defaultDBPath returns env LADYM_DB or cwd/ladym.db (workspace-local so each
// project gets its own memory by default).
func defaultDBPath() string {
	if env := os.Getenv("LADYM_DB"); env != "" {
		return env
	}
	wd, err := os.Getwd()
	if err != nil {
		return "ladym.db"
	}
	return filepath.Join(wd, "ladym.db")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Default returns a Config with all defaults populated (reading env at call time).
func Default() *Config {
	return &Config{
		DBPath:          defaultDBPath(),
		Workspace:       envOr("LADYM_WORKSPACE", "default"),
		PreferSQLiteVec: true,
		EnableWAL:       false,

		EmbeddingProvider:         envOr("LADYM_EMBEDDING", "hashing"),
		EmbeddingModel:            envOr("LADYM_EMBEDDING_MODEL", ""),
		EmbeddingDim:              256,
		EmbeddingBaseURL:          "",
		EmbeddingAPIKeyEnv:        "",
		EmbeddingFallback:         "none",
		EmbeddingQueryCacheSize:   0,
		EmbeddingTimeoutS:         10.0,
		EmbeddingAllowDimChange:   false,
		EmbeddingHTTPRequest:      `{"input": "{text}"}`,
		EmbeddingHTTPResponsePath: "data",

		LLMProvider:           "none",
		LLMBaseURL:            "",
		LLMModel:              "gpt-4o-mini",
		LLMAPIKeyEnv:          "",
		LLMMaxTokens:          1024,
		LLMTemperature:        0.2,
		LLMStructuredMethod:   "function_calling",
		LLMTimeoutS:           30.0,
		LLMAPIKey:             "",
		AllowPlaintextSecrets: false,

		AgentsOverrides: map[string]map[string]any{},
		Activation: ActivationWeights{
			Similarity: 1.0, Recency: 0.3, Frequency: 0.2, Graph: 0.15,
			TypeBoost: 0.25, RecencyHalfLifeS: 7 * 24 * 3600.0,
		},
		Recall: RecallConfig{
			TopKTier1: 8, TopKTier2: 20, GraphHops: 2,
			ReflectionMinHits: 2, ReflectionMinCoverage: 0.5, EnableTier2: true,
		},
		Consolidate: ConsolidateConfig{
			MinEpisodesToTrigger: 3, DedupSimilarityThreshold: 0.85,
		},
		CodeIndex: CodeIndexConfig{
			MaxBodyLinesPerSymbol: 40,
			RespectGitignore:      true,
			ExtraIgnoreGlobs:      []string{"**/.venv/**", "**/node_modules/**", "**/__pycache__/**"},
		},
		System2: System2Config{
			Enabled: false, IntervalS: 300, MinEpisodesToRun: 3,
			MaxConsecutiveErrors: 10,
			L5ClusterSimilarity:  0.65, L5MinClusterSize: 3, L5MergeSimilarity: 0.80,
			L5MergeEveryNCycles: 5, L6MaxEpisodes: 50, L6HorizonS: 3 * 24 * 3600.0,
		},
		Attention: AttentionConfig{DedupWindowS: 3600.0, NoiseWords: []string{}},
	}
}

// ForTesting returns a Config pointing at a temp db using the offline hashing
// embedding and the in-memory vector index.
func ForTesting(tmpDir string) *Config {
	c := Default()
	c.DBPath = filepath.Join(tmpDir, "test.ladym.db")
	c.Workspace = "test"
	c.EmbeddingProvider = "hashing"
	c.LLMProvider = "none"
	c.PreferSQLiteVec = false
	return c
}

// ---------------------------------------------------------------------------
// Secret handling
// ---------------------------------------------------------------------------

// _secretExact are secret-literal key names (case-insensitive).
var secretExact = map[string]bool{
	"api_key": true, "apikey": true, "secret": true, "token": true,
	"password": true, "passwd": true, "private_key": true, "access_key": true,
	"secret_key": true, "bearer": true, "bearer_token": true,
}

// secretSuffixes mark a key as holding a secret literal.
var secretSuffixes = []string{"_api_key", "_apikey", "_secret", "_token", "_password", "_key"}

const envSuffix = "_env"

// isSecret reports whether key names a secret literal (not an env-var reference).
func isSecret(key string) bool {
	k := strings.ToLower(key)
	if strings.HasSuffix(k, envSuffix) {
		return false
	}
	if secretExact[k] {
		return true
	}
	for _, s := range secretSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

func stripSecrets(data map[string]any, source string, allowPlaintext bool) map[string]any {
	if allowPlaintext {
		return data
	}
	cleaned := map[string]any{}
	for k, v := range data {
		if sub, ok := v.(map[string]any); ok {
			cleaned[k] = stripSecrets(sub, source, false)
		} else if isSecret(k) {
			fmt.Fprintf(os.Stderr, "WARNING: ignoring secret literal %q in %s; use <name>_env = \"VARNAME\" instead\n", k, source)
		} else {
			cleaned[k] = v
		}
	}
	return cleaned
}

// ParseTomlSafely parses TOML text, stripping secret literals (with a stderr
// warning per drop).
func ParseTomlSafely(text string, source string) (map[string]any, error) {
	if source == "" {
		source = "<string>"
	}
	var raw map[string]any
	if _, err := toml.Decode(text, &raw); err != nil {
		return nil, err
	}
	return stripSecrets(raw, source, false), nil
}

// ---------------------------------------------------------------------------
// Deprecation rename
// ---------------------------------------------------------------------------

func renameDeprecated(data map[string]any, source string) map[string]any {
	if _, ok := data["embedding_endpoint"]; ok {
		fmt.Fprintf(os.Stderr, "WARNING: 'embedding_endpoint' in %s is deprecated; use 'embedding_base_url' instead\n", source)
		if _, ok := data["embedding_base_url"]; !ok {
			data["embedding_base_url"] = data["embedding_endpoint"]
		}
		delete(data, "embedding_endpoint")
	}
	if emb, ok := data["embedding"].(map[string]any); ok {
		if _, ok := emb["endpoint"]; ok {
			fmt.Fprintf(os.Stderr, "WARNING: 'endpoint' in [embedding] of %s is deprecated; use 'base_url' instead\n", source)
			if _, ok := emb["base_url"]; !ok {
				emb["base_url"] = emb["endpoint"]
			}
			delete(emb, "endpoint")
		}
	}
	return data
}

// ---------------------------------------------------------------------------
// Deep merge
// ---------------------------------------------------------------------------

func deepMerge(base, overlay map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if vd, ok := v.(map[string]any); ok {
			if bd, ok2 := out[k].(map[string]any); ok2 {
				out[k] = deepMerge(bd, vd)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Applying a parsed dict to a Config
// ---------------------------------------------------------------------------

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return toBool(t)
	case int64:
		return t != 0
	case float64:
		return t != 0
	default:
		return false
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

func toBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func warnUnknown(key string) {
	fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown config key %q\n", key)
}

// applyToml applies a parsed, secret-stripped TOML dict to cfg in place.
func applyToml(cfg *Config, data map[string]any) {
	for k, v := range data {
		switch k {
		case "embedding":
			if t, ok := v.(map[string]any); ok {
				for ek, ev := range t {
					switch ek {
					case "provider", "base_url", "model", "api_key_env", "fallback",
						"query_cache_size", "timeout_s", "allow_dim_change",
						"http_request", "http_response_path":
						applyFlat(cfg, "embedding_"+ek, ev)
					default:
						if !isSecret(ek) {
							fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown embedding key %q\n", ek)
						}
					}
				}
			}
		case "llm":
			if t, ok := v.(map[string]any); ok {
				for lk, lv := range t {
					switch lk {
					case "provider", "base_url", "model", "api_key", "api_key_env",
						"max_tokens", "temperature", "structured_method", "timeout_s":
						applyFlat(cfg, "llm_"+lk, lv)
					default:
						if !isSecret(lk) {
							fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown llm key %q\n", lk)
						}
					}
				}
			}
		case "agents":
			if t, ok := v.(map[string]any); ok {
				for op, overrides := range t {
					od, ok := overrides.(map[string]any)
					if !ok {
						fmt.Fprintf(os.Stderr, "WARNING: ignoring non-table agents.%s\n", op)
						continue
					}
					merged := map[string]any{}
					if existing, ok := cfg.AgentsOverrides[op]; ok {
						for mk, mv := range existing {
							merged[mk] = mv
						}
					}
					for mk, mv := range od {
						merged[mk] = mv
					}
					cfg.AgentsOverrides[op] = merged
				}
			}
		case "activation":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "similarity":
						cfg.Activation.Similarity = asFloat(nv)
					case "recency":
						cfg.Activation.Recency = asFloat(nv)
					case "frequency":
						cfg.Activation.Frequency = asFloat(nv)
					case "graph":
						cfg.Activation.Graph = asFloat(nv)
					case "type_boost":
						cfg.Activation.TypeBoost = asFloat(nv)
					case "recency_half_life_s":
						cfg.Activation.RecencyHalfLifeS = asFloat(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown activation.%s\n", nk)
					}
				}
			}
		case "recall":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "top_k_tier1":
						cfg.Recall.TopKTier1 = asInt(nv)
					case "top_k_tier2":
						cfg.Recall.TopKTier2 = asInt(nv)
					case "graph_hops":
						cfg.Recall.GraphHops = asInt(nv)
					case "reflection_min_hits":
						cfg.Recall.ReflectionMinHits = asInt(nv)
					case "reflection_min_coverage":
						cfg.Recall.ReflectionMinCoverage = asFloat(nv)
					case "enable_tier2":
						cfg.Recall.EnableTier2 = asBool(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown recall.%s\n", nk)
					}
				}
			}
		case "consolidate":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "min_episodes_to_trigger":
						cfg.Consolidate.MinEpisodesToTrigger = asInt(nv)
					case "dedup_similarity_threshold":
						cfg.Consolidate.DedupSimilarityThreshold = asFloat(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown consolidate.%s\n", nk)
					}
				}
			}
		case "code_index":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "max_body_lines_per_symbol":
						cfg.CodeIndex.MaxBodyLinesPerSymbol = asInt(nv)
					case "respect_gitignore":
						cfg.CodeIndex.RespectGitignore = asBool(nv)
					case "extra_ignore_globs":
						cfg.CodeIndex.ExtraIgnoreGlobs = asStringSlice(nv)
					case "languages":
						cfg.CodeIndex.Languages = asStringSlice(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown code_index.%s\n", nk)
					}
				}
			}
		case "system2":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "enabled":
						cfg.System2.Enabled = asBool(nv)
					case "interval_s":
						cfg.System2.IntervalS = asInt(nv)
					case "min_episodes_to_run":
						cfg.System2.MinEpisodesToRun = asInt(nv)
					case "max_consecutive_errors":
						cfg.System2.MaxConsecutiveErrors = asInt(nv)
					case "l5_cluster_similarity":
						cfg.System2.L5ClusterSimilarity = asFloat(nv)
					case "l5_min_cluster_size":
						cfg.System2.L5MinClusterSize = asInt(nv)
					case "l5_merge_similarity":
						cfg.System2.L5MergeSimilarity = asFloat(nv)
					case "l5_merge_every_n_cycles":
						cfg.System2.L5MergeEveryNCycles = asInt(nv)
					case "l6_max_episodes":
						cfg.System2.L6MaxEpisodes = asInt(nv)
					case "l6_horizon_s":
						cfg.System2.L6HorizonS = asFloat(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown system2.%s\n", nk)
					}
				}
			}
		case "attention":
			if t, ok := v.(map[string]any); ok {
				for nk, nv := range t {
					switch nk {
					case "dedup_window_s":
						cfg.Attention.DedupWindowS = asFloat(nv)
					case "noise_words":
						cfg.Attention.NoiseWords = asStringSlice(nv)
					default:
						fmt.Fprintf(os.Stderr, "WARNING: ignoring unknown attention.%s\n", nk)
					}
				}
			}
		default:
			applyFlat(cfg, k, v)
		}
	}
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, asString(x))
		}
		return out
	case []map[string]any: // TOML array of tables — flatten to repr
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, fmt.Sprintf("%v", x))
		}
		return out
	case string:
		return []string{t}
	default:
		return nil
	}
}

func applyFlat(cfg *Config, key string, v any) {
	switch key {
	case "db_path":
		cfg.DBPath = asString(v)
	case "workspace":
		cfg.Workspace = asString(v)
	case "prefer_sqlite_vec":
		cfg.PreferSQLiteVec = asBool(v)
	case "enable_wal":
		cfg.EnableWAL = asBool(v)
	case "embedding_provider":
		cfg.EmbeddingProvider = asString(v)
	case "embedding_model":
		cfg.EmbeddingModel = asString(v)
	case "embedding_dim":
		cfg.EmbeddingDim = asInt(v)
	case "embedding_base_url":
		cfg.EmbeddingBaseURL = asString(v)
	case "embedding_api_key_env":
		cfg.EmbeddingAPIKeyEnv = asString(v)
	case "embedding_fallback":
		cfg.EmbeddingFallback = asString(v)
	case "embedding_query_cache_size":
		cfg.EmbeddingQueryCacheSize = asInt(v)
	case "embedding_timeout_s":
		cfg.EmbeddingTimeoutS = asFloat(v)
	case "embedding_allow_dim_change":
		cfg.EmbeddingAllowDimChange = asBool(v)
	case "embedding_http_request":
		cfg.EmbeddingHTTPRequest = asString(v)
	case "embedding_http_response_path":
		cfg.EmbeddingHTTPResponsePath = asString(v)
	case "llm_provider":
		cfg.LLMProvider = asString(v)
	case "llm_base_url":
		cfg.LLMBaseURL = asString(v)
	case "llm_model":
		cfg.LLMModel = asString(v)
	case "llm_api_key_env":
		cfg.LLMAPIKeyEnv = asString(v)
	case "llm_max_tokens":
		cfg.LLMMaxTokens = asInt(v)
	case "llm_temperature":
		cfg.LLMTemperature = asFloat(v)
	case "llm_structured_method":
		cfg.LLMStructuredMethod = asString(v)
	case "llm_timeout_s":
		cfg.LLMTimeoutS = asFloat(v)
	case "llm_api_key":
		cfg.LLMAPIKey = asString(v)
	case "allow_plaintext_secrets":
		cfg.AllowPlaintextSecrets = asBool(v)
	default:
		warnUnknown(key)
	}
}

// syncNested rebuilds the nested embedding/llm structs from the flat fields.
func syncNested(cfg *Config) {
	cfg.Embedding = EmbeddingConfig{
		Provider:         cfg.EmbeddingProvider,
		BaseURL:          cfg.EmbeddingBaseURL,
		Model:            cfg.EmbeddingModel,
		APIKeyEnv:        cfg.EmbeddingAPIKeyEnv,
		Fallback:         cfg.EmbeddingFallback,
		QueryCacheSize:   cfg.EmbeddingQueryCacheSize,
		TimeoutS:         cfg.EmbeddingTimeoutS,
		AllowDimChange:   cfg.EmbeddingAllowDimChange,
		HTTPRequest:      cfg.EmbeddingHTTPRequest,
		HTTPResponsePath: cfg.EmbeddingHTTPResponsePath,
	}
	cfg.LLM = LLMConfig{
		Provider:         cfg.LLMProvider,
		BaseURL:          cfg.LLMBaseURL,
		Model:            cfg.LLMModel,
		APIKeyEnv:        cfg.LLMAPIKeyEnv,
		MaxTokens:        cfg.LLMMaxTokens,
		Temperature:      cfg.LLMTemperature,
		StructuredMethod: cfg.LLMStructuredMethod,
		TimeoutS:         cfg.LLMTimeoutS,
	}
}

// applyDict applies a CLI-style dict (same shape as the TOML dict), stripping
// secret literals first.
func applyDict(cfg *Config, d map[string]any) {
	cleaned := stripSecrets(d, "<cli-overrides>", false)
	applyToml(cfg, cleaned)
}

// ---------------------------------------------------------------------------
// Env overlay
// ---------------------------------------------------------------------------

func applyEnv(cfg *Config) {
	if v := os.Getenv("LADYM_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("LADYM_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}
	if v := os.Getenv("LADYM_EMBEDDING"); v != "" {
		cfg.EmbeddingProvider = v
	}
	if v := os.Getenv("LADYM_EMBEDDING_MODEL"); v != "" {
		cfg.EmbeddingModel = v
	}
	if v := os.Getenv("LADYM_EMBEDDING_BASE_URL"); v != "" {
		cfg.EmbeddingBaseURL = v
	}
	if v := os.Getenv("LADYM_EMBEDDING_API_KEY_ENV"); v != "" {
		cfg.EmbeddingAPIKeyEnv = v
	}
	if v := os.Getenv("LADYM_EMBEDDING_TIMEOUT_S"); v != "" {
		cfg.EmbeddingTimeoutS = asFloat(v)
	}
	if v := os.Getenv("LADYM_LLM_PROVIDER"); v != "" {
		cfg.LLMProvider = v
	}
	if v := os.Getenv("LADYM_LLM_BASE_URL"); v != "" {
		cfg.LLMBaseURL = v
	}
	if v := os.Getenv("LADYM_LLM_MODEL"); v != "" {
		cfg.LLMModel = v
	}
	if v := os.Getenv("LADYM_LLM_API_KEY_ENV"); v != "" {
		cfg.LLMAPIKeyEnv = v
	}
	if v := os.Getenv("LADYM_LLM_MAX_TOKENS"); v != "" {
		cfg.LLMMaxTokens = asInt(v)
	}
	if v := os.Getenv("LADYM_LLM_TEMPERATURE"); v != "" {
		cfg.LLMTemperature = asFloat(v)
	}
	if v := os.Getenv("LADYM_PREFER_SQLITE_VEC"); v != "" {
		cfg.PreferSQLiteVec = toBool(v)
	}
	if v := os.Getenv("LADYM_ENABLE_WAL"); v != "" {
		cfg.EnableWAL = toBool(v)
	}
}

// ---------------------------------------------------------------------------
// Loaders
// ---------------------------------------------------------------------------

// FromFile builds a Config from a single TOML file (defaults + file, no
// env/CLI). Strips secret literals, renames deprecated keys, and populates the
// nested structs.
func FromFile(path string) (*Config, error) {
	var raw map[string]any
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, err
	}
	allow := asBool(raw["allow_plaintext_secrets"])
	data := stripSecrets(raw, path, allow)
	data = renameDeprecated(data, path)
	cfg := Default()
	applyToml(cfg, data)
	syncNested(cfg)
	return cfg, nil
}

// Load resolves a Config through the 4-layer precedence: defaults →
// ~/.ladym/config.toml → ./ladym.toml → configPath, then env vars, then
// cli_overrides.
func Load(configPath string, cliOverrides map[string]any) (*Config, error) {
	var layers []string
	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, ".ladym", "config.toml")
		if fileExists(globalPath) {
			layers = append(layers, globalPath)
		}
	}
	projectPath := filepath.Join(".", "ladym.toml")
	if fileExists(projectPath) {
		layers = append(layers, projectPath)
	}
	if configPath != "" {
		layers = append(layers, configPath)
	}

	type rawLayer struct {
		path string
		data map[string]any
	}
	var rawLayers []rawLayer
	allowPlaintext := false
	for _, p := range layers {
		var raw map[string]any
		if _, err := toml.DecodeFile(p, &raw); err != nil {
			return nil, err
		}
		rawLayers = append(rawLayers, rawLayer{p, raw})
		if asBool(raw["allow_plaintext_secrets"]) {
			allowPlaintext = true
		}
	}

	merged := map[string]any{}
	for _, rl := range rawLayers {
		rl.data = stripSecrets(rl.data, rl.path, allowPlaintext)
		rl.data = renameDeprecated(rl.data, rl.path)
		merged = deepMerge(merged, rl.data)
	}

	cfg := Default()
	applyToml(cfg, merged)
	syncNested(cfg)

	applyEnv(cfg)
	syncNested(cfg)

	if len(cliOverrides) > 0 {
		applyDict(cfg, cliOverrides)
		syncNested(cfg)
	}
	return cfg, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
