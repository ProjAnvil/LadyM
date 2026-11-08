package config

import "testing"

func TestMaintenanceDefaults(t *testing.T) {
	cfg := Default()
	c := cfg.System2
	if c.BudgetedEnabled || c.MaxCandidates != 50 || c.MaxCalls != 8 || c.MaxReservedBytes != 8192 || c.MaxWallS != 30 || c.Concurrency != 1 || c.LeaseS != 60 || c.MaxAttempts != 3 {
		t.Fatalf("defaults %+v", c)
	}
	applyToml(cfg, map[string]any{"system2": map[string]any{"budgeted_enabled": true, "max_candidates": 12, "max_calls": 2, "max_reserved_bytes": 4096, "max_wall_s": 10, "concurrency": 4, "lease_s": 20, "max_attempts": 2}})
	if !cfg.System2.BudgetedEnabled || cfg.System2.MaxReservedBytes != 4096 || cfg.System2.Concurrency != 4 || cfg.System2.MaxAttempts != 2 {
		t.Fatalf("config %+v", cfg.System2)
	}
}
