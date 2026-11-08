package mcp

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMaintenanceMCP(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("PG unconfigured")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			s := &server{eng: e, cfg: cfg}
			m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
			m.Workspace, m.Content = "mcp", "event"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			raw, err := s.call("schedule_maintenance", map[string]any{"operation": "new_episode", "target_id": m.ID, "workspace": "mcp"})
			if err != nil {
				t.Fatal(err)
			}
			var job schema.Memory
			if err := json.Unmarshal([]byte(raw), &job); err != nil {
				t.Fatal(err)
			}
			raw, err = s.call("list_maintenance_jobs", map[string]any{"workspace": "mcp", "limit": float64(10)})
			if err != nil {
				t.Fatal(err)
			}
			var jobs []schema.MaintenanceJob
			if err := json.Unmarshal([]byte(raw), &jobs); err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
				t.Fatalf("jobs %v %v", jobs, err)
			}
			raw, err = s.call("maintenance_status", map[string]any{"workspace": "mcp"})
			if err != nil {
				t.Fatal(err)
			}
			var report schema.MaintenanceReport
			if err := json.Unmarshal([]byte(raw), &report); err != nil || report.Unit != "bytes" {
				t.Fatalf("report %v %v", report, err)
			}
			if _, err := s.call("schedule_maintenance", map[string]any{"operation": "exec", "target_id": m.ID, "workspace": "mcp"}); err == nil {
				t.Fatal("arbitrary operation")
			}
			if _, err := s.call("list_maintenance_jobs", map[string]any{"limit": 2.5}); err == nil {
				t.Fatal("fractional limit")
			}
		})
	}
}
