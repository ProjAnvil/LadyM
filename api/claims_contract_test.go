package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestManagedHTTPGenericEditRejected(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded in enterprise edition")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("LADYM_TEST_PG_DSN not set")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			e, err := engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			s := e.Scope("claims")
			ev, err := s.RecordEvidence("test_result", "sensitive evidence", "success", "host")
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.CreateClaim("private derived body", []schema.EvidenceRef{{MemoryID: ev.ID, Kind: "test_result", Fingerprint: schema.KnowledgeFingerprint(ev)}}, schema.ClaimScope{}, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			h := api.NewHandler(e, cfg)
			request := httptest.NewRequest(http.MethodPut, "/api/memories/"+c.Memory.ID, strings.NewReader(`{"content":"forged"}`))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, request)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("generic HTTP edit = %d %s", rec.Code, rec.Body.String())
			}
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/remember", strings.NewReader(`{"content":"forged","metadata":{"knowledge_status":"verified"}}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("generic HTTP verification = %d %s", rec.Code, rec.Body.String())
			}
			rows, err := e.Store.IterMemories("", "", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Content == "forged" && row.MetaString("knowledge_status") == "verified" {
					t.Fatal("ignored HTTP metadata forged trust")
				}
			}
			if err := e.Store.DeleteMemory(ev.ID); err != nil {
				t.Fatal(err)
			}
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/memories?workspace=claims", nil))
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private derived body") || strings.Contains(rec.Body.String(), "sensitive evidence") {
				t.Fatalf("HTTP list leaked stale body = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
