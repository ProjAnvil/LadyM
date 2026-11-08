package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	client "github.com/ProjAnvil/LadyM/client/golang"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestTopicHTTPAndSDKLoopback(t *testing.T) {
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
			t.Cleanup(func() { e.Close() })
			if err := e.Store.PutUser(&schema.User{Username: "alice", Workspace: "owned"}); err != nil {
				t.Fatal(err)
			}
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
			m.Workspace, m.Content = "owned", "ExactLoopbackSymbol"
			if err := e.Store.PutMemory(m, nil); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(api.NewHandler(e, cfg))
			t.Cleanup(server.Close)
			c := client.New(server.URL, client.WithAuth("alice", ""))
			topic, err := c.BuildTopic(t.Context(), schema.BuildTopicRequest{Topic: "module/path", InputIDs: []string{m.ID}, Workspace: "forged"})
			if err != nil || topic.Workspace != "owned" {
				t.Fatalf("build: %+v %v", topic, err)
			}
			got, err := c.GetTopic(t.Context(), "module/path", "forged")
			if err != nil || got.ID != topic.ID {
				t.Fatalf("get: %v", err)
			}
			if _, err := c.BuildTopic(t.Context(), schema.BuildTopicRequest{Topic: "module/path", InputIDs: []string{m.ID}, Workspace: "forged"}); err == nil {
				t.Fatal("missing first absent guard")
			}
			hits, err := c.HybridRecall(t.Context(), schema.HybridRecallRequest{Query: "ExactLoopbackSymbol", Limit: 1, Workspace: "forged"})
			if err != nil || len(hits.Results) != 1 || hits.Results[0].Memory.ID != m.ID {
				t.Fatalf("hybrid: %+v %v", hits, err)
			}
			task, err := c.SetTaskState(t.Context(), schema.SetTaskStateRequest{ID: "task", Goal: "explicit goal", Next: "next", TTLS: 60, Workspace: "forged"})
			if err != nil || task.Workspace != "owned" {
				t.Fatalf("task: %v", err)
			}
			b, err := c.Bootstrap(t.Context(), schema.BootstrapRequest{TopicIDs: []string{topic.ID}, TaskID: "task", Workspace: "forged"})
			if err != nil || b.Used != len(b.Content) || !strings.Contains(b.Content, task.ID) || !strings.Contains(b.Content, topic.ID) {
				t.Fatalf("bootstrap: %+v %v", b, err)
			}
			if _, err := c.Bootstrap(t.Context(), schema.BootstrapRequest{Unit: "tokens"}); err == nil {
				t.Fatal("remote tokens accepted")
			}
			for _, path := range []string{"/api/topics", "/api/hybrid_recall", "/api/bootstrap", "/api/task_state"} {
				r, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader("null"))
				if err != nil {
					t.Fatal(err)
				}
				r.SetBasicAuth("alice", "")
				resp, err := server.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 400 {
					t.Fatalf("%s null status %d", path, resp.StatusCode)
				}
			}
			r, _ := http.NewRequest(http.MethodPost, server.URL+"/api/bootstrap", strings.NewReader(`{"unit":"tokens"}`))
			r.SetBasicAuth("alice", "")
			resp, err := server.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatalf("HTTP tokens status %d", resp.StatusCode)
			}
		})
	}
}
