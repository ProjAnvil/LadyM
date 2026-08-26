//go:build !enterprise

package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestDictDirFlagRegistered: both personal-edition daemon commands accept
// --dict-dir (ladymconsole registers it too, enterprise-tagged).
func TestDictDirFlagRegistered(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"serve":  serveCmd(),
		"worker": workerCmd(),
	} {
		if cmd.Flags().Lookup("dict-dir") == nil {
			t.Errorf("%s: --dict-dir flag not registered", name)
		}
	}
}

// TestLoadConfigExtraDictDir covers the priority chain for the dict
// directory: CLI flag → LADYM_DICT_DIR env → toml → default ~/.ladyM/dict.
func TestLoadConfigExtraDictDir(t *testing.T) {
	cfg, err := loadConfigExtra("", "", "/flag/dict")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CJKDictDir != "/flag/dict" {
		t.Errorf("flag = %q, want /flag/dict", cfg.CJKDictDir)
	}

	t.Setenv("LADYM_DICT_DIR", "/env/dict")
	envCfg, err := loadConfigExtra("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if envCfg.CJKDictDir != "/env/dict" {
		t.Errorf("env = %q, want /env/dict", envCfg.CJKDictDir)
	}
	bothCfg, err := loadConfigExtra("", "", "/flag/wins")
	if err != nil {
		t.Fatal(err)
	}
	if bothCfg.CJKDictDir != "/flag/wins" {
		t.Errorf("flag+env = %q, want flag to win", bothCfg.CJKDictDir)
	}
}
