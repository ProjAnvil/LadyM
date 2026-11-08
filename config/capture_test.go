package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureIdentityModeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "")
	t.Chdir(t.TempDir())
	if got := Default().CaptureIdentityMode; got != "legacy" {
		t.Fatalf("default mode = %q", got)
	}
	p := filepath.Join(".", "capture.toml")
	write := func(mode string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("[capture]\nidentity_mode = \""+mode+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("canonical")
	cfg, err := FromFile(p)
	if err != nil || cfg.CaptureIdentityMode != "canonical" {
		t.Fatalf("TOML canonical: %+v, %v", cfg, err)
	}
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "legacy")
	cfg, err = Load(p, nil)
	if err != nil || cfg.CaptureIdentityMode != "legacy" {
		t.Fatalf("env precedence: %+v, %v", cfg, err)
	}
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "canonical")
	cfg, err = Load("", nil)
	if err != nil || cfg.CaptureIdentityMode != "canonical" {
		t.Fatalf("env canonical: %+v, %v", cfg, err)
	}
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "invalid")
	if _, err := Load("", nil); err == nil {
		t.Fatal("invalid env must fail")
	} else if _, ok := errors.AsType[*ConfigError](err); !ok {
		t.Fatalf("not ConfigError: %v", err)
	}
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "")
	for _, mode := range []string{"invalid", "", "Canonical"} {
		write(mode)
		if _, err := FromFile(p); err == nil {
			t.Fatalf("invalid TOML %q must fail", mode)
		} else if _, ok := errors.AsType[*ConfigError](err); !ok {
			t.Fatalf("not ConfigError: %v", err)
		}
	}
}
