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
