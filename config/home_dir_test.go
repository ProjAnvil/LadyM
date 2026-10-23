package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeDirUsesCapitalM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got, want := HomeDir(), filepath.Join(home, DirName); got != want {
		t.Errorf("HomeDir() = %q, want %q", got, want)
	}
	if DirName != ".ladyM" {
		t.Errorf("DirName = %q, want .ladyM", DirName)
	}
}

func TestHomeDirFallsBackToCwdWhenHomeUnset(t *testing.T) {
	t.Setenv("HOME", "")

	if got, want := HomeDir(), filepath.Join(".", DirName); got != want {
		t.Errorf("HomeDir() with empty HOME = %q, want %q", got, want)
	}
}

func TestGlobalConfigPathFindsRealFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	current := filepath.Join(home, DirName, "config.toml")
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("workspace = \"current\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := GlobalConfigPath(); got != current {
		t.Errorf("GlobalConfigPath() = %q, want %q", got, current)
	}
}

// caseSensitiveExists simulates a filesystem that distinguishes ~/.ladym from
// ~/.ladyM, which macOS APFS does not. Only the listed paths exist. Without it,
// writing only the legacy file on macOS also makes ~/.ladyM/config.toml exist,
// and every assertion about the two spellings becomes unfalsifiable.
func caseSensitiveExists(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestGlobalConfigPathPrefersCurrentSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	current := filepath.Join(home, DirName, "config.toml")
	legacy := filepath.Join(home, LegacyDirName, "config.toml")
	exists := caseSensitiveExists(current, legacy)

	if got := globalConfigPath(exists); got != current {
		t.Errorf("globalConfigPath() = %q, want %q", got, current)
	}
}

// There is no fallback to the legacy spelling: an install that still keeps its
// config at ~/.ladym would otherwise run on defaults while looking configured.
func TestGlobalConfigPathIgnoresLegacySpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacy := filepath.Join(home, LegacyDirName, "config.toml")
	exists := caseSensitiveExists(legacy)

	if got := globalConfigPath(exists); got != "" {
		t.Errorf("globalConfigPath() = %q, want empty (legacy config is not loaded)", got)
	}
}

func TestGlobalConfigPathEmptyWhenAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := globalConfigPath(caseSensitiveExists()); got != "" {
		t.Errorf("globalConfigPath() = %q, want empty", got)
	}
	if got := GlobalConfigPath(); got != "" {
		t.Errorf("GlobalConfigPath() = %q, want empty", got)
	}
}

func TestGlobalConfigPathEmptyWhenHomeUnset(t *testing.T) {
	t.Setenv("HOME", "")

	if got := GlobalConfigPath(); got != "" {
		t.Errorf("GlobalConfigPath() with empty HOME = %q, want empty", got)
	}
}
