package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// store backend: defaults / flat keys / [store] table / env / dsn_env
// ---------------------------------------------------------------------------

func TestStoreBackendDefaults(t *testing.T) {
	t.Setenv("LADYM_STORE_BACKEND", "")
	t.Setenv("LADYM_STORE_DSN", "")
	cfg := Default()
	if cfg.StoreBackend != "sqlite" {
		t.Errorf("StoreBackend = %q, want sqlite", cfg.StoreBackend)
	}
	if cfg.StoreDSN != "" {
		t.Errorf("StoreDSN = %q, want empty", cfg.StoreDSN)
	}
}

func TestStoreBackendDefaultReadsEnv(t *testing.T) {
	t.Setenv("LADYM_STORE_BACKEND", "postgres")
	cfg := Default()
	if cfg.StoreBackend != "postgres" {
		t.Errorf("StoreBackend = %q, want postgres", cfg.StoreBackend)
	}
}

func TestApplyFlatStoreKeys(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"store_backend": "postgres",
		"store_dsn":     "postgres://u:p@h/db",
		"store_dsn_env": "SOME_DSN_VAR",
	})
	if cfg.StoreBackend != "postgres" {
		t.Errorf("StoreBackend = %q", cfg.StoreBackend)
	}
	if cfg.StoreDSN != "postgres://u:p@h/db" {
		t.Errorf("StoreDSN = %q", cfg.StoreDSN)
	}
	if cfg.StoreDSNEnv != "SOME_DSN_VAR" {
		t.Errorf("StoreDSNEnv = %q", cfg.StoreDSNEnv)
	}
}

func TestApplyTomlStoreTable(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{
		"store": map[string]any{
			"backend":    "postgres",
			"dsn":        "postgres://u:p@h/db",
			"dsn_env":    "MY_DSN",
			"unknown_st": 1,
		},
	})
	if cfg.StoreBackend != "postgres" || cfg.StoreDSN != "postgres://u:p@h/db" {
		t.Errorf("store table wrong: backend=%q dsn=%q", cfg.StoreBackend, cfg.StoreDSN)
	}
	if cfg.StoreDSNEnv != "MY_DSN" {
		t.Errorf("StoreDSNEnv = %q", cfg.StoreDSNEnv)
	}
	// non-table store value is ignored
	applyToml(cfg, map[string]any{"store": "nope"})
}

func TestApplyEnvStore(t *testing.T) {
	t.Setenv("LADYM_STORE_BACKEND", "postgres")
	t.Setenv("LADYM_STORE_DSN", "postgres://env/db")
	cfg := Default()
	applyEnv(cfg)
	if cfg.StoreBackend != "postgres" {
		t.Errorf("StoreBackend = %q", cfg.StoreBackend)
	}
	if cfg.StoreDSN != "postgres://env/db" {
		t.Errorf("StoreDSN = %q", cfg.StoreDSN)
	}
}

func TestStoreDSNEnvResolution(t *testing.T) {
	t.Setenv("LADYM_TEST_DSN", "postgres://from-env/db")
	cfg := Default()
	applyToml(cfg, map[string]any{
		"store": map[string]any{"backend": "postgres", "dsn_env": "LADYM_TEST_DSN"},
	})
	syncNested(cfg)
	if cfg.StoreDSN != "postgres://from-env/db" {
		t.Errorf("StoreDSN = %q, want resolved from dsn_env", cfg.StoreDSN)
	}
	if cfg.Store.DSN != "postgres://from-env/db" {
		t.Errorf("nested Store.DSN = %q", cfg.Store.DSN)
	}
}

func TestStoreDSNDirectWinsOverEnv(t *testing.T) {
	t.Setenv("LADYM_TEST_DSN", "postgres://from-env/db")
	cfg := Default()
	// map iteration order is random; priority must hold either way
	for i := 0; i < 8; i++ {
		c := Default()
		applyToml(c, map[string]any{
			"store": map[string]any{
				"backend": "postgres",
				"dsn":     "postgres://direct/db",
				"dsn_env": "LADYM_TEST_DSN",
			},
		})
		syncNested(c)
		if c.StoreDSN != "postgres://direct/db" {
			t.Fatalf("iter %d: StoreDSN = %q, want direct dsn to win", i, c.StoreDSN)
		}
	}
	_ = cfg
}

func TestStoreNestedMirrorSync(t *testing.T) {
	cfg := Default()
	applyToml(cfg, map[string]any{"store_backend": "postgres", "store_dsn": "postgres://x/db"})
	syncNested(cfg)
	if cfg.Store.Backend != "postgres" || cfg.Store.DSN != "postgres://x/db" {
		t.Errorf("nested Store = %+v", cfg.Store)
	}
}

func TestStoreDsnNotSecret(t *testing.T) {
	// store.dsn may be written directly (design decision); dsn_env passes the
	// secret filter via the _env suffix.
	if isSecret("dsn") {
		t.Error("dsn should not be treated as a secret literal")
	}
	if isSecret("store_dsn") {
		t.Error("store_dsn should not be treated as a secret literal")
	}
	if isSecret("dsn_env") {
		t.Error("dsn_env should pass the secret filter (_env suffix)")
	}
}

func TestFromFileStoreTable(t *testing.T) {
	t.Setenv("LADYM_STORE_BACKEND", "")
	t.Setenv("LADYM_STORE_DSN", "")
	t.Setenv("LADYM_TEST_FROMFILE_DSN", "postgres://env-resolved/db")
	dir := t.TempDir()
	path := filepath.Join(dir, "ladym.toml")
	content := "[store]\nbackend = \"postgres\"\ndsn_env = \"LADYM_TEST_FROMFILE_DSN\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := FromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StoreBackend != "postgres" {
		t.Errorf("StoreBackend = %q", cfg.StoreBackend)
	}
	if cfg.StoreDSN != "postgres://env-resolved/db" {
		t.Errorf("StoreDSN = %q, want dsn_env resolution", cfg.StoreDSN)
	}
	if cfg.Store.Backend != "postgres" || cfg.Store.DSN != "postgres://env-resolved/db" {
		t.Errorf("nested Store = %+v", cfg.Store)
	}
}
