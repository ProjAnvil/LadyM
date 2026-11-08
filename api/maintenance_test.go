package api_test

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestMaintenanceHTTPAndSDK(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend, cfg.AuthEnabled = backend, true
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
			if err := e.Store.PutUser(&schema.User{Username: "alice", Workspace: "owned"}); err != nil {
				t.Fatal(err)
			}
			m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
			m.Workspace, m.Content = "owned", "event"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(api.NewHandler(e, cfg))
			defer server.Close()
			c := client.New(server.URL, client.WithAuth("alice", ""))
			job, err := c.ScheduleMaintenance(t.Context(), schema.ScheduleMaintenanceRequest{Operation: "new_episode", TargetID: m.ID, Workspace: "forged"})
			if err != nil || job.Workspace != "owned" {
				t.Fatalf("principal scope %+v %v", job, err)
			}
			if _, err := c.ScheduleMaintenance(t.Context(), schema.ScheduleMaintenanceRequest{Operation: "run_shell", TargetID: m.ID}); err == nil {
				t.Fatal("arbitrary operation accepted")
			}
			jobs, err := c.ListMaintenanceJobs(t.Context(), "forged", 100)
			if err != nil || len(jobs) != 1 || jobs[0].InputRevisions[m.ID] != 0 || jobs[0].State != "pending" {
				t.Fatalf("jobs %+v %v", jobs, err)
			}
			if _, err := e.RunBudgetedCycle(t.Context(), "owned"); err != nil {
				t.Fatal(err)
			}
			report, err := c.MaintenanceStatus(t.Context(), "forged")
			if err != nil || report.Unit != "bytes" || report.CompletedJobs != 1 {
				t.Fatalf("report %+v %v", report, err)
			}
			if _, err := c.ListMaintenanceJobs(t.Context(), "", -1); err == nil {
				t.Fatal("invalid limit")
			}
		})
	}
}
