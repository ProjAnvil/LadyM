package engine

// Port of main:tests/test_regression_baseline.py — pins the public surface
// that must not break (NFR-4) and the "LLM is write-path-only" spirit (NFR-3).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

// unsetLadymEnv keeps config.Default() hermetic: a developer's LADYM_* env
// must not change the defaults this test pins (Python: the _isolate_config
// fixture deleted every LADYM_* var).
func unsetLadymEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		k := strings.SplitN(kv, "=", 2)[0]
		if !strings.HasPrefix(k, "LADYM_") {
			continue
		}
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			}
		})
	}
}

func TestConfigDefaultsUnchanged(t *testing.T) {
	unsetLadymEnv(t)
	cfg := config.Default()
	if cfg.EmbeddingProvider != "hashing" {
		t.Errorf("embedding_provider = %q, want %q", cfg.EmbeddingProvider, "hashing")
	}
	if cfg.LLMProvider != "none" {
		t.Errorf("llm_provider = %q, want %q", cfg.LLMProvider, "none")
	}
	if cfg.Workspace != "default" {
		t.Errorf("workspace = %q, want %q", cfg.Workspace, "default")
	}
	// Accepted for config parity even though the Go store always uses the
	// in-memory vector index (see storage.NewStore).
	if !cfg.PreferSQLiteVec {
		t.Error("prefer_sqlite_vec = false, want true")
	}
	if cfg.Activation.Similarity != 1.0 {
		t.Errorf("activation.similarity = %v, want 1.0", cfg.Activation.Similarity)
	}
	if cfg.Recall.TopKTier1 != 8 {
		t.Errorf("recall.top_k_tier1 = %d, want 8", cfg.Recall.TopKTier1)
	}
}

func TestEngineConstructsWithPlainConfig(t *testing.T) {
	unsetLadymEnv(t)
	cfg := config.Default()
	cfg.DBPath = filepath.Join(t.TempDir(), "x.db")
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	if eng.Provider.Dim() != 256 {
		t.Errorf("provider dim = %d, want 256", eng.Provider.Dim())
	}
	resp, err := eng.Recall("nothing", "", 0, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 1 && resp.TierReached != 2 {
		t.Errorf("tier_reached = %d, want 1 or 2", resp.TierReached)
	}
}

func TestConfiguredLLMDoesNotBreakReadPath(t *testing.T) {
	unsetLadymEnv(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.LLMProvider = "openai" // configured, but no key anywhere

	eng, err := New(cfg) // must NOT fail — no eager LLM wiring
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	if _, err := eng.Recall("anything", "", 0, nil, nil, 0); err != nil {
		t.Fatalf("read path broke with a configured LLM: %v", err)
	}
	if _, err := eng.Stats(); err != nil {
		t.Fatalf("stats broke with a configured LLM: %v", err)
	}

	// The read path must not have constructed any LLM agent.
	eng.mu.Lock()
	nAgents := len(eng.agents)
	eng.mu.Unlock()
	if nAgents != 0 {
		t.Errorf("%d LLM agents constructed on the read path, want 0", nAgents)
	}

	// Go semantics deliberately differ from Python here: the Python port
	// fell back to the heuristic classifier when the LLM extra was missing,
	// while the Go engine is fail-fast — consolidate surfaces a ConfigError
	// for the missing key instead of silently degrading.
	if _, err := eng.Consolidate("", 0); err == nil {
		t.Error("consolidate with a keyless LLM configured: want a fail-fast ConfigError, got nil")
	} else {
		var ce *config.ConfigError
		if !errors.As(err, &ce) {
			t.Errorf("consolidate error = %T (%v), want *config.ConfigError", err, err)
		}
	}
}
