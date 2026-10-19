package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSecret(t *testing.T) {
	cases := map[string]bool{
		"api_key":               true,
		"API_KEY":               true,
		"embedding_api_key":     true,
		"llm_api_key":           true,
		"secret":                true,
		"token":                 true,
		"password":              true,
		"master_key":            true,
		"api_key_env":           false,
		"embedding_api_key_env": false,
		"model":                 false,
		"provider":              false,
	}
	for k, want := range cases {
		if got := isSecret(k); got != want {
			t.Errorf("isSecret(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestParseTomlSafelyStripsSecrets(t *testing.T) {
	data, err := ParseTomlSafely(`api_key = "sk-secret"`+"\n"+`model = "gpt-4o"`+"\n"+`[llm]`+"\n"+`api_key_env = "MY_KEY"`+"\n", "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["api_key"]; ok {
		t.Error("api_key should be stripped")
	}
	if _, ok := data["model"]; !ok {
		t.Error("model should survive")
	}
	llm := data["llm"].(map[string]any)
	if llm["api_key_env"] != "MY_KEY" {
		t.Errorf("api_key_env = %v, want MY_KEY", llm["api_key_env"])
	}
}

func TestFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	content := "workspace = \"team\"\n" +
		"[embedding]\nprovider = \"hashing\"\ndim = 128\n" +
		"[llm]\nprovider = \"none\"\n" +
		"[activation]\nsimilarity = 0.9\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "team" {
		t.Errorf("workspace = %q, want team", cfg.Workspace)
	}
	if cfg.EmbeddingProvider != "hashing" {
		t.Errorf("embedding provider = %q", cfg.EmbeddingProvider)
	}
	if cfg.Activation.Similarity != 0.9 {
		t.Errorf("similarity = %v", cfg.Activation.Similarity)
	}
	// nested mirror synced
	if cfg.Embedding.Provider != "hashing" {
		t.Errorf("nested embedding provider = %q", cfg.Embedding.Provider)
	}
}

func TestDeepMerge(t *testing.T) {
	base := map[string]any{"a": 1, "agents": map[string]any{"x": map[string]any{"p": "a", "m": "old"}}}
	overlay := map[string]any{"b": 2, "agents": map[string]any{"x": map[string]any{"m": "new"}}}
	merged := deepMerge(base, overlay)
	if merged["a"] != 1 || merged["b"] != 2 {
		t.Errorf("scalar merge wrong: %v", merged)
	}
	agents := merged["agents"].(map[string]any)
	x := agents["x"].(map[string]any)
	if x["p"] != "a" || x["m"] != "new" {
		t.Errorf("nested merge wrong: %v", x)
	}
}

func TestDefaultEnableWAL(t *testing.T) {
	// WAL is on by default so MCP serve / worker / one-shot CLI processes can
	// share a single db without "database is locked" failures.
	if !Default().EnableWAL {
		t.Error("EnableWAL default should be true")
	}
}

func TestForTesting(t *testing.T) {
	cfg := ForTesting(t.TempDir())
	if cfg.Workspace != "test" {
		t.Errorf("workspace = %q", cfg.Workspace)
	}
	if cfg.EmbeddingProvider != "hashing" || cfg.LLMProvider != "none" {
		t.Errorf("offline defaults wrong")
	}
	if cfg.PreferSQLiteVec {
		t.Errorf("PreferSQLiteVec should be false for testing")
	}
}

func TestMaintainConfigDefaults(t *testing.T) {
	m := Default().Maintain
	if !m.Enabled {
		t.Error("Maintain.Enabled default should be true")
	}
	if m.BackfillBatch != 50 {
		t.Errorf("Maintain.BackfillBatch = %d, want 50", m.BackfillBatch)
	}
	if !m.LinkingEnabled {
		t.Error("Maintain.LinkingEnabled default should be true")
	}
	if m.LinkingBudget != 20 {
		t.Errorf("Maintain.LinkingBudget = %d, want 20", m.LinkingBudget)
	}
	if m.LinkingSimilarity != 0.60 {
		t.Errorf("Maintain.LinkingSimilarity = %v, want 0.60", m.LinkingSimilarity)
	}
	if m.AliasMergeSimilarity != 0.92 {
		t.Errorf("Maintain.AliasMergeSimilarity = %v, want 0.92", m.AliasMergeSimilarity)
	}
}

func TestMaintainConfigToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	content := "[maintain]\n" +
		"enabled = false\n" +
		"backfill_batch = 10\n" +
		"linking_enabled = false\n" +
		"linking_budget = 5\n" +
		"linking_similarity = 0.7\n" +
		"alias_merge_similarity = 0.95\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Maintain
	if m.Enabled {
		t.Error("Maintain.Enabled = true, want false")
	}
	if m.BackfillBatch != 10 {
		t.Errorf("Maintain.BackfillBatch = %d, want 10", m.BackfillBatch)
	}
	if m.LinkingEnabled {
		t.Error("Maintain.LinkingEnabled = true, want false")
	}
	if m.LinkingBudget != 5 {
		t.Errorf("Maintain.LinkingBudget = %d, want 5", m.LinkingBudget)
	}
	if m.LinkingSimilarity != 0.7 {
		t.Errorf("Maintain.LinkingSimilarity = %v, want 0.7", m.LinkingSimilarity)
	}
	if m.AliasMergeSimilarity != 0.95 {
		t.Errorf("Maintain.AliasMergeSimilarity = %v, want 0.95", m.AliasMergeSimilarity)
	}
}

func TestResourceConfigDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Resource.ResolveThreshold != 0.85 {
		t.Errorf("Resource.ResolveThreshold = %v, want 0.85", cfg.Resource.ResolveThreshold)
	}
	if cfg.Resource.ReadThreshold != 0.75 {
		t.Errorf("Resource.ReadThreshold = %v, want 0.75", cfg.Resource.ReadThreshold)
	}
	if cfg.Resource.Actions == nil {
		t.Error("Resource.Actions should be non-nil empty slice by default")
	}
	if cfg.Resource.ActionAliases == nil {
		t.Error("Resource.ActionAliases should be non-nil empty map by default")
	}
}

func TestResourceConfigToml(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	content := "[resource]\n" +
		"resolve_threshold = 0.9\n" +
		"read_threshold = 0.8\n" +
		"actions = [\"frobnicate\"]\n" +
		"action_aliases = { zap = \"execute\" }\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.ResolveThreshold != 0.9 {
		t.Errorf("ResolveThreshold = %v, want 0.9", cfg.Resource.ResolveThreshold)
	}
	if cfg.Resource.ReadThreshold != 0.8 {
		t.Errorf("ReadThreshold = %v, want 0.8", cfg.Resource.ReadThreshold)
	}
	if len(cfg.Resource.Actions) != 1 || cfg.Resource.Actions[0] != "frobnicate" {
		t.Errorf("Actions = %v, want [frobnicate]", cfg.Resource.Actions)
	}
	if cfg.Resource.ActionAliases["zap"] != "execute" {
		t.Errorf("ActionAliases = %v, want zap->execute", cfg.Resource.ActionAliases)
	}
}
