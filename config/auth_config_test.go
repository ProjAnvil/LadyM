package config

// Tests for the [auth] table — the HTTP data-plane's Basic-auth master switch.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeToml(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ladym.toml")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAuthEnabledFlatKey(t *testing.T) {
	cfg, err := FromFile(writeToml(t, `auth_enabled = true`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AuthEnabled {
		t.Error("AuthEnabled = false, want true")
	}
	// Nested mirror is populated by the loader (flat is source of truth).
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled = false, want true")
	}
}

func TestAuthTableToml(t *testing.T) {
	cfg, err := FromFile(writeToml(t, `
[auth]
enabled = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AuthEnabled {
		t.Error("AuthEnabled = false, want true")
	}
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled = false, want true")
	}
}

func TestAuthEnabledEnvOverride(t *testing.T) {
	t.Setenv("LADYM_AUTH_ENABLED", "true")
	cfg := Default()
	applyEnv(cfg)
	syncNested(cfg)
	if !cfg.AuthEnabled {
		t.Error("AuthEnabled = false, want true (env)")
	}
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled = false, want true (env)")
	}
}

func TestAuthDefaultsDisabled(t *testing.T) {
	cfg := Default()
	if cfg.AuthEnabled {
		t.Error("AuthEnabled default = true, want false (personal mode unchanged)")
	}
	if cfg.Auth.Enabled {
		t.Error("Auth.Enabled default = true, want false")
	}
}

// The removed [server] table (bearer-token era) must not crash the loader:
// it degrades to the standard unknown-key warning.
func TestLegacyServerTableWarnsNotCrashes(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	cfg, loadErr := FromFile(writeToml(t, `
[server]
token_env = "MY_ADMIN_TOKEN"
`))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if loadErr != nil {
		t.Fatalf("legacy [server] table must not crash the loader: %v", loadErr)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if !strings.Contains(string(out), "unknown config key") {
		t.Errorf("expected unknown-key warning for [server], got: %q", out)
	}
}
