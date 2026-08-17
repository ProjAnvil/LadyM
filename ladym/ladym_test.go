package ladym

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
)

func TestOneShotRememberRecall(t *testing.T) {
	db := filepath.Join(t.TempDir(), "mem.db")
	m, err := Remember("auth uses JWT with 24h expiry", db, "ws", []string{"auth"}, "sdk")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" {
		t.Fatal("remember returned empty id")
	}
	resp, err := Recall("how does authentication work", db, "ws", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected recall results")
	}
}

// dbPathUnderFile returns a db path whose parent component is a regular file,
// so storage.NewStore's MkdirAll fails and engine.New returns an error.
func dbPathUnderFile(t *testing.T) string {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "mem.db")
}

func TestNamedOps(t *testing.T) {
	ops := NamedOps()
	if len(ops) != 5 {
		t.Fatalf("NamedOps len = %d, want 5", len(ops))
	}
	if ops[0] != "consolidate" {
		t.Errorf("NamedOps[0] = %q", ops[0])
	}
	// Mutating the returned slice must not affect the registry.
	ops[0] = "mutated"
	if NamedOps()[0] != "consolidate" {
		t.Error("NamedOps returned slice aliases the registry")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.DBPath == "" {
		t.Error("DefaultConfig left DBPath empty")
	}
}

func TestNewEngine(t *testing.T) {
	t.Run("nil config uses defaults", func(t *testing.T) {
		eng, err := NewEngine(nil)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
	})
	t.Run("explicit config", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBPath = filepath.Join(t.TempDir(), "mem.db")
		cfg.Workspace = "ws"
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
	})
	t.Run("invalid db path errors", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBPath = dbPathUnderFile(t)
		if _, err := NewEngine(cfg); err == nil {
			t.Fatal("expected error for unreachable db path")
		}
	})
}

func TestNewEngineWithModels(t *testing.T) {
	t.Run("nil models", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBPath = filepath.Join(t.TempDir(), "mem.db")
		eng, err := NewEngineWithModels(cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
	})
	t.Run("empty routing falls back to config providers", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBPath = filepath.Join(t.TempDir(), "mem.db")
		eng, err := NewEngineWithModels(cfg, &adapter.ModelRouting{})
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
	})
}

func TestOpenEngine(t *testing.T) {
	t.Run("dbPath and workspace overrides", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "mem.db")
		var gotWS string
		err := OpenEngine(DefaultConfig(), db, "custom-ws", func(eng *Engine) error {
			gotWS = eng.Config.Workspace
			if eng.Config.DBPath != db {
				t.Errorf("DBPath = %q, want %q", eng.Config.DBPath, db)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if gotWS != "custom-ws" {
			t.Errorf("Workspace = %q, want custom-ws", gotWS)
		}
	})
	t.Run("fn error propagates", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "mem.db")
		want := os.ErrNotExist
		err := OpenEngine(nil, db, "ws", func(eng *Engine) error {
			return want
		})
		if err != want {
			t.Errorf("err = %v, want %v", err, want)
		}
	})
	t.Run("engine build error propagates", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.DBPath = dbPathUnderFile(t)
		err := OpenEngine(cfg, "", "", func(eng *Engine) error {
			t.Fatal("fn must not run when engine build fails")
			return nil
		})
		if err == nil {
			t.Fatal("expected error for unreachable db path")
		}
	})
}

func TestOneShotIndexCode(t *testing.T) {
	src := t.TempDir()
	goFile := filepath.Join(src, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "mem.db")
	report, err := IndexCode(src, db, "ws", false)
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("IndexCode returned nil report")
	}
	if report.FilesIndexed == 0 {
		t.Error("expected at least one indexed file")
	}
}
