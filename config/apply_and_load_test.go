package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ConfigError
// ---------------------------------------------------------------------------

func TestConfigError(t *testing.T) {
	err := configErrorf("bad value %q for %s", "x", "key")
	if err.Error() != `bad value "x" for key` {
		t.Errorf("Error() = %q", err.Error())
	}
	var ce *ConfigError
	if !errorAs(err, &ce) {
		t.Error("configErrorf should return a *ConfigError")
	}
}

func errorAs(err error, target **ConfigError) bool {
	ce, ok := err.(*ConfigError)
	if ok {
		*target = ce
	}
	return ok
}

// ---------------------------------------------------------------------------
// defaultDBPath / envOr
// ---------------------------------------------------------------------------

func TestDefaultDBPathFromEnv(t *testing.T) {
	t.Setenv("LADYM_DB", "/tmp/custom.db")
	if got := defaultDBPath(); got != "/tmp/custom.db" {
		t.Errorf("defaultDBPath() = %q, want /tmp/custom.db", got)
	}
}

func TestDefaultDBPathFallback(t *testing.T) {
	t.Setenv("LADYM_DB", "")
	got := defaultDBPath()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, "ladym.db")
	if got != want {
		t.Errorf("defaultDBPath() = %q, want %q", got, want)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("LADYM_TEST_ENV_OR", "from-env")
	if got := envOr("LADYM_TEST_ENV_OR", "def"); got != "from-env" {
		t.Errorf("envOr set = %q", got)
	}
	if got := envOr("LADYM_TEST_ENV_OR_UNSET", "def"); got != "def" {
		t.Errorf("envOr unset = %q", got)
	}
}

func TestDefaultReadsEnv(t *testing.T) {
	t.Setenv("LADYM_DB", "/tmp/env.db")
	t.Setenv("LADYM_WORKSPACE", "envws")
	t.Setenv("LADYM_EMBEDDING", "openai")
	t.Setenv("LADYM_EMBEDDING_MODEL", "text-embedding-3-small")
	cfg := Default()
	if cfg.DBPath != "/tmp/env.db" {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if cfg.Workspace != "envws" {
		t.Errorf("Workspace = %q", cfg.Workspace)
	}
	if cfg.EmbeddingProvider != "openai" {
		t.Errorf("EmbeddingProvider = %q", cfg.EmbeddingProvider)
	}
	if cfg.EmbeddingModel != "text-embedding-3-small" {
		t.Errorf("EmbeddingModel = %q", cfg.EmbeddingModel)
	}
}

// ---------------------------------------------------------------------------
// stripSecrets / ParseTomlSafely
// ---------------------------------------------------------------------------

func TestStripSecretsAllowPlaintext(t *testing.T) {
	data := map[string]any{"api_key": "sk-x", "model": "m"}
	got := stripSecrets(data, "src", true)
	if got["api_key"] != "sk-x" {
		t.Error("allowPlaintext should keep secrets untouched")
	}
}

func TestStripSecretsNested(t *testing.T) {
	data := map[string]any{
		"llm":   map[string]any{"token": "abc", "model": "m"},
		"model": "top",
	}
	got := stripSecrets(data, "src", false)
	llm := got["llm"].(map[string]any)
	if _, ok := llm["token"]; ok {
		t.Error("nested secret should be stripped")
	}
	if llm["model"] != "m" {
		t.Error("nested non-secret should survive")
	}
	if got["model"] != "top" {
		t.Error("top-level non-secret should survive")
	}
}

func TestParseTomlSafelyErrors(t *testing.T) {
	if _, err := ParseTomlSafely("not = [valid", ""); err == nil {
		t.Error("expected parse error for invalid TOML")
	}
	// empty source falls back to "<string>" and still parses
	data, err := ParseTomlSafely("model = \"m\"", "")
	if err != nil {
		t.Fatal(err)
	}
	if data["model"] != "m" {
		t.Errorf("model = %v", data["model"])
	}
}

// ---------------------------------------------------------------------------
// renameDeprecated
// ---------------------------------------------------------------------------

func TestRenameDeprecated(t *testing.T) {
	// flat rename
	data := map[string]any{"embedding_endpoint": "http://x"}
	got := renameDeprecated(data, "src")
	if got["embedding_base_url"] != "http://x" {
		t.Errorf("embedding_base_url = %v", got["embedding_base_url"])
	}
	if _, ok := got["embedding_endpoint"]; ok {
		t.Error("embedding_endpoint should be deleted")
	}

	// existing base_url wins
	data = map[string]any{"embedding_endpoint": "http://old", "embedding_base_url": "http://new"}
	got = renameDeprecated(data, "src")
	if got["embedding_base_url"] != "http://new" {
		t.Errorf("existing base_url should win, got %v", got["embedding_base_url"])
	}

	// nested [embedding] endpoint
	data = map[string]any{"embedding": map[string]any{"endpoint": "http://y"}}
	got = renameDeprecated(data, "src")
	emb := got["embedding"].(map[string]any)
	if emb["base_url"] != "http://y" {
		t.Errorf("nested base_url = %v", emb["base_url"])
	}
	if _, ok := emb["endpoint"]; ok {
		t.Error("nested endpoint should be deleted")
	}

	// nested existing base_url wins
	data = map[string]any{"embedding": map[string]any{"endpoint": "http://old", "base_url": "http://new"}}
	got = renameDeprecated(data, "src")
	emb = got["embedding"].(map[string]any)
	if emb["base_url"] != "http://new" {
		t.Errorf("nested existing base_url should win, got %v", emb["base_url"])
	}
}

// ---------------------------------------------------------------------------
// coercion helpers
// ---------------------------------------------------------------------------

func TestAsString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"s", "s"},
		{int64(42), "42"},
		{float64(1.5), "1.5"},
		{true, "true"},
		{[]string{"a"}, "[a]"},
	}
	for _, c := range cases {
		if got := asString(c.in); got != c.want {
			t.Errorf("asString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAsBool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{"yes", true},
		{"off", false},
		{int64(2), true},
		{int64(0), false},
		{float64(0.5), true},
		{float64(0), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := asBool(c.in); got != c.want {
			t.Errorf("asBool(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAsInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{int64(7), 7},
		{9, 9},
		{float64(3.9), 3},
		{"11", 11},
		{"bad", 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := asInt(c.in); got != c.want {
			t.Errorf("asInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestAsFloat(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{float64(1.5), 1.5},
		{int64(2), 2.0},
		{3, 3.0},
		{"0.25", 0.25},
		{"bad", 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := asFloat(c.in); got != c.want {
			t.Errorf("asFloat(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestToBool(t *testing.T) {
	for _, s := range []string{"1", "true", "TRUE", " yes ", "on"} {
		if !toBool(s) {
			t.Errorf("toBool(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"0", "false", "no", "off", "", "maybe"} {
		if toBool(s) {
			t.Errorf("toBool(%q) = true, want false", s)
		}
	}
}

func TestWarnUnknown(t *testing.T) {
	// smoke: just exercise the stderr warning path
	warnUnknown("bogus_key")
}

// ---------------------------------------------------------------------------
// asStringSlice
// ---------------------------------------------------------------------------

func TestAsStringSlice(t *testing.T) {
	if got := asStringSlice([]string{"a", "b"}); len(got) != 2 || got[0] != "a" {
		t.Errorf("[]string case = %v", got)
	}
	got := asStringSlice([]any{"x", int64(1), true})
	if len(got) != 3 || got[0] != "x" || got[1] != "1" || got[2] != "true" {
		t.Errorf("[]any case = %v", got)
	}
	got = asStringSlice([]map[string]any{{"k": "v"}})
	if len(got) != 1 || !strings.Contains(got[0], "k") {
		t.Errorf("[]map case = %v", got)
	}
	got = asStringSlice("solo")
	if len(got) != 1 || got[0] != "solo" {
		t.Errorf("string case = %v", got)
	}
	if got := asStringSlice(int64(5)); got != nil {
		t.Errorf("default case = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// applyFlat (via applyToml default branch)
// ---------------------------------------------------------------------------

func TestApplyFlatAllKeys(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"db_path":                      "/tmp/x.db",
		"workspace":                    "ws",
		"prefer_sqlite_vec":            false,
		"enable_wal":                   false,
		"embedding_provider":           "openai",
		"embedding_model":              "m",
		"embedding_dim":                int64(128),
		"embedding_base_url":           "http://e",
		"embedding_api_key_env":        "E_KEY",
		"embedding_fallback":           "hashing",
		"embedding_query_cache_size":   int64(10),
		"embedding_timeout_s":          5.0,
		"embedding_allow_dim_change":   true,
		"embedding_http_request":       "{}",
		"embedding_http_response_path": "emb",
		"llm_provider":                 "openai",
		"llm_base_url":                 "http://l",
		"llm_model":                    "gpt",
		"llm_api_key_env":              "L_KEY",
		"llm_max_tokens":               int64(99),
		"llm_temperature":              0.7,
		"llm_structured_method":        "json",
		"llm_timeout_s":                2.0,
		"llm_api_key":                  "plain",
		"allow_plaintext_secrets":      true,
		"totally_unknown_key":          1,
	})
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DBPath", cfg.DBPath, "/tmp/x.db"},
		{"Workspace", cfg.Workspace, "ws"},
		{"PreferSQLiteVec", cfg.PreferSQLiteVec, false},
		{"EnableWAL", cfg.EnableWAL, false},
		{"EmbeddingProvider", cfg.EmbeddingProvider, "openai"},
		{"EmbeddingModel", cfg.EmbeddingModel, "m"},
		{"EmbeddingDim", cfg.EmbeddingDim, 128},
		{"EmbeddingBaseURL", cfg.EmbeddingBaseURL, "http://e"},
		{"EmbeddingAPIKeyEnv", cfg.EmbeddingAPIKeyEnv, "E_KEY"},
		{"EmbeddingFallback", cfg.EmbeddingFallback, "hashing"},
		{"EmbeddingQueryCacheSize", cfg.EmbeddingQueryCacheSize, 10},
		{"EmbeddingTimeoutS", cfg.EmbeddingTimeoutS, 5.0},
		{"EmbeddingAllowDimChange", cfg.EmbeddingAllowDimChange, true},
		{"EmbeddingHTTPRequest", cfg.EmbeddingHTTPRequest, "{}"},
		{"EmbeddingHTTPResponsePath", cfg.EmbeddingHTTPResponsePath, "emb"},
		{"LLMProvider", cfg.LLMProvider, "openai"},
		{"LLMBaseURL", cfg.LLMBaseURL, "http://l"},
		{"LLMModel", cfg.LLMModel, "gpt"},
		{"LLMAPIKeyEnv", cfg.LLMAPIKeyEnv, "L_KEY"},
		{"LLMMaxTokens", cfg.LLMMaxTokens, 99},
		{"LLMTemperature", cfg.LLMTemperature, 0.7},
		{"LLMStructuredMethod", cfg.LLMStructuredMethod, "json"},
		{"LLMTimeoutS", cfg.LLMTimeoutS, 2.0},
		{"LLMAPIKey", cfg.LLMAPIKey, "plain"},
		{"AllowPlaintextSecrets", cfg.AllowPlaintextSecrets, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// applyToml sections
// ---------------------------------------------------------------------------

func TestApplyTomlEmbeddingAndLLM(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"embedding": map[string]any{
			"provider": "ollama", "base_url": "http://e", "model": "m",
			"api_key_env": "EK", "fallback": "none", "query_cache_size": int64(3),
			"timeout_s": 1.5, "allow_dim_change": true,
			"http_request": "{}", "http_response_path": "d",
			"unknown_emb_key": 1,
			"api_key":         "should-be-ignored-silently", // secret key in default branch
		},
		"llm": map[string]any{
			"provider": "openai", "base_url": "http://l", "model": "g",
			"api_key": "k", "api_key_env": "LK", "max_tokens": int64(7),
			"temperature": 0.1, "structured_method": "fc", "timeout_s": 3.0,
			"unknown_llm_key": 1,
		},
	})
	if cfg.EmbeddingProvider != "ollama" || cfg.EmbeddingBaseURL != "http://e" {
		t.Errorf("embedding section wrong: %+v", cfg.Embedding)
	}
	if cfg.EmbeddingQueryCacheSize != 3 || cfg.EmbeddingTimeoutS != 1.5 || !cfg.EmbeddingAllowDimChange {
		t.Errorf("embedding knobs wrong")
	}
	if cfg.LLMProvider != "openai" || cfg.LLMAPIKey != "k" || cfg.LLMMaxTokens != 7 {
		t.Errorf("llm section wrong")
	}
	// non-table section values are ignored
	applyToml(cfg, map[string]any{"embedding": "nope", "llm": "nope"})
}

func TestApplyTomlAgents(t *testing.T) {
	cfg := Default()
	cfg.AgentsOverrides["op"] = map[string]any{"keep": "old", "model": "old-m"}
	applyToml(cfg, map[string]any{
		"agents": map[string]any{
			"op":      map[string]any{"model": "new-m"},
			"bad":     "not-a-table",
			"freshop": map[string]any{"temperature": 0.5},
		},
	})
	op := cfg.AgentsOverrides["op"]
	if op["keep"] != "old" || op["model"] != "new-m" {
		t.Errorf("agents merge wrong: %v", op)
	}
	if _, ok := cfg.AgentsOverrides["bad"]; ok {
		t.Error("non-table agents entry should be skipped")
	}
	if cfg.AgentsOverrides["freshop"]["temperature"] != 0.5 {
		t.Errorf("freshop = %v", cfg.AgentsOverrides["freshop"])
	}
	// non-table agents value ignored
	applyToml(cfg, map[string]any{"agents": "nope"})
}

func TestApplyTomlNestedSections(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"activation": map[string]any{
			"similarity": 0.9, "recency": 0.4, "frequency": 0.3,
			"graph": 0.2, "type_boost": 0.1, "recency_half_life_s": 100.0,
			"unknown_act": 1,
		},
		"recall": map[string]any{
			"top_k_tier1": int64(1), "top_k_tier2": int64(2),
			"graph_hops": int64(3), "reflection_min_hits": int64(4),
			"reflection_min_coverage": 0.9, "enable_tier2": false,
			"unknown_recall": 1,
		},
		"consolidate": map[string]any{
			"min_episodes_to_trigger": int64(5), "dedup_similarity_threshold": 0.7,
			"unknown_cons": 1,
		},
		"code_index": map[string]any{
			"max_body_lines_per_symbol": int64(50), "respect_gitignore": false,
			"extra_ignore_globs": []any{"a", "b"}, "languages": []any{"go"},
			"unknown_ci": 1,
		},
		"system2": map[string]any{
			"enabled": true, "interval_s": int64(60), "min_episodes_to_run": int64(2),
			"max_consecutive_errors": int64(3), "l5_cluster_similarity": 0.5,
			"l5_min_cluster_size": int64(2), "l5_merge_similarity": 0.6,
			"l5_merge_every_n_cycles": int64(4), "l6_max_episodes": int64(10),
			"l6_horizon_s": 50.0,
			"unknown_s2":   1,
		},
		"attention": map[string]any{
			"dedup_window_s": 30.0, "noise_words": []any{"lol"},
			"unknown_att": 1,
		},
	})
	if cfg.Activation.Similarity != 0.9 || cfg.Activation.RecencyHalfLifeS != 100.0 {
		t.Errorf("activation wrong: %+v", cfg.Activation)
	}
	if cfg.Recall.TopKTier1 != 1 || cfg.Recall.EnableTier2 != false || cfg.Recall.ReflectionMinCoverage != 0.9 {
		t.Errorf("recall wrong: %+v", cfg.Recall)
	}
	if cfg.Consolidate.MinEpisodesToTrigger != 5 || cfg.Consolidate.DedupSimilarityThreshold != 0.7 {
		t.Errorf("consolidate wrong: %+v", cfg.Consolidate)
	}
	if cfg.CodeIndex.MaxBodyLinesPerSymbol != 50 || cfg.CodeIndex.RespectGitignore != false {
		t.Errorf("code_index wrong: %+v", cfg.CodeIndex)
	}
	if len(cfg.CodeIndex.ExtraIgnoreGlobs) != 2 || len(cfg.CodeIndex.Languages) != 1 {
		t.Errorf("code_index slices wrong: %+v", cfg.CodeIndex)
	}
	if !cfg.System2.Enabled || cfg.System2.IntervalS != 60 || cfg.System2.L6HorizonS != 50.0 {
		t.Errorf("system2 wrong: %+v", cfg.System2)
	}
	if cfg.Attention.DedupWindowS != 30.0 || len(cfg.Attention.NoiseWords) != 1 {
		t.Errorf("attention wrong: %+v", cfg.Attention)
	}
	// non-table section values are ignored
	applyToml(cfg, map[string]any{
		"activation": "x", "recall": "x", "consolidate": "x",
		"code_index": "x", "system2": "x", "attention": "x",
	})
}

// ---------------------------------------------------------------------------
// applyDict
// ---------------------------------------------------------------------------

func TestApplyDict(t *testing.T) {
	cfg := Default()
	applyDict(cfg, map[string]any{
		"workspace": "cli-ws",
		"api_key":   "should-be-stripped",
	})
	if cfg.Workspace != "cli-ws" {
		t.Errorf("workspace = %q", cfg.Workspace)
	}
	if cfg.LLMAPIKey != "" {
		t.Errorf("secret literal should be stripped, got LLMAPIKey = %q", cfg.LLMAPIKey)
	}
}

// ---------------------------------------------------------------------------
// applyEnv
// ---------------------------------------------------------------------------

func TestApplyEnv(t *testing.T) {
	t.Setenv("LADYM_DB", "/tmp/env.db")
	t.Setenv("LADYM_WORKSPACE", "env-ws")
	t.Setenv("LADYM_EMBEDDING", "openai")
	t.Setenv("LADYM_EMBEDDING_MODEL", "em")
	t.Setenv("LADYM_EMBEDDING_BASE_URL", "http://e")
	t.Setenv("LADYM_EMBEDDING_API_KEY_ENV", "EK")
	t.Setenv("LADYM_EMBEDDING_TIMEOUT_S", "2.5")
	t.Setenv("LADYM_LLM_PROVIDER", "openai")
	t.Setenv("LADYM_LLM_BASE_URL", "http://l")
	t.Setenv("LADYM_LLM_MODEL", "lm")
	t.Setenv("LADYM_LLM_API_KEY_ENV", "LK")
	t.Setenv("LADYM_LLM_MAX_TOKENS", "512")
	t.Setenv("LADYM_LLM_TEMPERATURE", "0.9")
	t.Setenv("LADYM_PREFER_SQLITE_VEC", "false")
	t.Setenv("LADYM_ENABLE_WAL", "0")

	cfg := Default()
	applyEnv(cfg)
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"DBPath", cfg.DBPath, "/tmp/env.db"},
		{"Workspace", cfg.Workspace, "env-ws"},
		{"EmbeddingProvider", cfg.EmbeddingProvider, "openai"},
		{"EmbeddingModel", cfg.EmbeddingModel, "em"},
		{"EmbeddingBaseURL", cfg.EmbeddingBaseURL, "http://e"},
		{"EmbeddingAPIKeyEnv", cfg.EmbeddingAPIKeyEnv, "EK"},
		{"EmbeddingTimeoutS", cfg.EmbeddingTimeoutS, 2.5},
		{"LLMProvider", cfg.LLMProvider, "openai"},
		{"LLMBaseURL", cfg.LLMBaseURL, "http://l"},
		{"LLMModel", cfg.LLMModel, "lm"},
		{"LLMAPIKeyEnv", cfg.LLMAPIKeyEnv, "LK"},
		{"LLMMaxTokens", cfg.LLMMaxTokens, 512},
		{"LLMTemperature", cfg.LLMTemperature, 0.9},
		{"PreferSQLiteVec", cfg.PreferSQLiteVec, false},
		{"EnableWAL", cfg.EnableWAL, false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// fileExists
// ---------------------------------------------------------------------------

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f.toml")
	if err := os.WriteFile(f, []byte("x = 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(f) {
		t.Error("fileExists(file) = false")
	}
	if fileExists(dir) {
		t.Error("fileExists(dir) = true, want false")
	}
	if fileExists(filepath.Join(dir, "missing")) {
		t.Error("fileExists(missing) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

func TestLoadPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// isolate from any real env overlays
	for _, k := range []string{
		"LADYM_DB", "LADYM_WORKSPACE", "LADYM_EMBEDDING", "LADYM_EMBEDDING_MODEL",
		"LADYM_EMBEDDING_BASE_URL", "LADYM_EMBEDDING_API_KEY_ENV",
		"LADYM_EMBEDDING_TIMEOUT_S", "LADYM_LLM_PROVIDER", "LADYM_LLM_BASE_URL",
		"LADYM_LLM_MODEL", "LADYM_LLM_API_KEY_ENV", "LADYM_LLM_MAX_TOKENS",
		"LADYM_LLM_TEMPERATURE", "LADYM_PREFER_SQLITE_VEC", "LADYM_ENABLE_WAL",
	} {
		t.Setenv(k, "")
	}

	// global layer
	globalDir := filepath.Join(home, ".ladym")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"),
		[]byte("workspace = \"global\"\n[llm]\nmodel = \"global-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// project layer
	projDir := t.TempDir()
	t.Chdir(projDir)
	if err := os.WriteFile(filepath.Join(projDir, "ladym.toml"),
		[]byte("workspace = \"project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// explicit configPath layer
	explicit := filepath.Join(t.TempDir(), "extra.toml")
	if err := os.WriteFile(explicit,
		[]byte("workspace = \"explicit\"\n[llm]\nmodel = \"explicit-model\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// env beats files; cli_overrides beat env
	t.Setenv("LADYM_LLM_MODEL", "env-model")

	cfg, err := Load(explicit, map[string]any{"workspace": "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "cli" {
		t.Errorf("workspace = %q, want cli (cli_overrides win)", cfg.Workspace)
	}
	if cfg.LLMModel != "env-model" {
		t.Errorf("llm model = %q, want env-model (env beats files)", cfg.LLMModel)
	}
	if cfg.LLM.Model != "env-model" {
		t.Errorf("nested llm model not synced: %q", cfg.LLM.Model)
	}
}

func TestLoadFileLayersOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LADYM_DB", "")
	t.Setenv("LADYM_WORKSPACE", "")

	globalDir := filepath.Join(home, ".ladym")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "config.toml"),
		[]byte("workspace = \"global\"\n[recall]\ntop_k_tier1 = 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projDir := t.TempDir()
	t.Chdir(projDir)
	if err := os.WriteFile(filepath.Join(projDir, "ladym.toml"),
		[]byte("workspace = \"project\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "project" {
		t.Errorf("workspace = %q, want project (project beats global)", cfg.Workspace)
	}
	if cfg.Recall.TopKTier1 != 3 {
		t.Errorf("recall.top_k_tier1 = %d, want 3 from global layer", cfg.Recall.TopKTier1)
	}
}

func TestLoadNoLayers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LADYM_DB", "")
	t.Chdir(t.TempDir())
	cfg, err := Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "default" {
		t.Errorf("workspace = %q, want default", cfg.Workspace)
	}
}

func TestLoadInvalidToml(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("not = [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad, nil); err == nil {
		t.Error("expected error for invalid TOML layer")
	}
}

func TestLoadAllowPlaintextSecrets(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LADYM_DB", "")
	t.Chdir(t.TempDir())
	p := filepath.Join(t.TempDir(), "secrets.toml")
	content := "allow_plaintext_secrets = true\n[llm]\napi_key = \"sk-plain\"\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMAPIKey != "sk-plain" {
		t.Errorf("LLMAPIKey = %q, want sk-plain (allowed via allow_plaintext_secrets)", cfg.LLMAPIKey)
	}
	if !cfg.AllowPlaintextSecrets {
		t.Error("AllowPlaintextSecrets should be true")
	}
}

func TestLoadDeprecatedRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LADYM_DB", "")
	t.Chdir(t.TempDir())
	p := filepath.Join(t.TempDir(), "dep.toml")
	// flat endpoint only — flat + nested together would race on map order
	content := "embedding_endpoint = \"http://old\"\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingBaseURL != "http://old" {
		t.Errorf("EmbeddingBaseURL = %q, want http://old", cfg.EmbeddingBaseURL)
	}
}

// ---------------------------------------------------------------------------
// FromFile edge cases
// ---------------------------------------------------------------------------

func TestFromFileInvalid(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(bad, []byte("x = [bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FromFile(bad); err == nil {
		t.Error("expected error for invalid TOML")
	}
	if _, err := FromFile(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestFromFileDeprecatedAndSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	// nested endpoint only — flat + nested together would race on map order
	content := "[embedding]\nendpoint = \"http://nested-old\"\napi_key = \"sk-drop\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmbeddingBaseURL != "http://nested-old" {
		t.Errorf("EmbeddingBaseURL = %q", cfg.EmbeddingBaseURL)
	}
	if cfg.EmbeddingAPIKeyEnv != "" {
		t.Errorf("secret should have been stripped; EmbeddingAPIKeyEnv = %q", cfg.EmbeddingAPIKeyEnv)
	}
}
