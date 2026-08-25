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
