package ladym_test

import (
	"os"
	"strings"
	"testing"

	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/ladym"
	"github.com/ProjAnvil/LadyM/storage"
)

type sdkCounter struct{}

func (sdkCounter) Name() string                   { return "host-model" }
func (sdkCounter) Count(text string) (int, error) { return len(text), nil }

func TestBootstrapGoHostSDK(t *testing.T) {
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
			e, err := ladym.NewEngine(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			scope := e.Scope("host")
			task, err := scope.SetTaskState("explicit", "user goal", "next", 0)
			if err != nil || task.Type != ladym.TypeTaskState {
				t.Fatalf("task: %v", err)
			}
			var counter ladym.TokenCounter = sdkCounter{}
			var response *ladym.BootstrapResponse
			response, err = client.BootstrapLocal(scope, ladym.BootstrapRequest{TaskID: "explicit", Unit: "tokens"}, counter)
			if err != nil || response.Model != "host-model" || response.Unit != "tokens" || response.Used != len(response.Content) || response.Used > 512 || !strings.Contains(response.Content, task.ID) {
				t.Fatalf("host SDK: %+v %v", response, err)
			}
		})
	}
}
