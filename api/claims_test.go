package api_test

import (
	"encoding/json"
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

func TestClaimHTTPIsolationAndActor(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded in enterprise")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.Workspace, cfg.StoreBackend, cfg.AuthEnabled = "default", backend, true
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
			if err := e.Store.PutUser(&schema.User{Username: "alice", Workspace: "a"}); err != nil {
				t.Fatal(err)
			}
			h := api.NewHandler(e, cfg)
			call := func(method, path, body, user string, status int) []byte {
				t.Helper()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				if user != "" {
					r.SetBasicAuth(user, "")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != status {
					t.Fatalf("%s %s: %d %s; want %d", method, path, w.Code, w.Body.String(), status)
				}
				return w.Body.Bytes()
			}
			var ev schema.Memory
			json.Unmarshal(call("POST", "/api/evidence", `{"kind":"test_result","content":"passed","outcome":"success","actor":"forged","workspace":"b"}`, "alice", 200), &ev)
			if ev.Workspace != "a" || ev.Source != "alice" || ev.MetaString("actor") != "alice" {
				t.Fatalf("identity/scope forged: %+v", ev)
			}
			ref := schema.EvidenceRef{MemoryID: ev.ID, Fingerprint: schema.KnowledgeFingerprint(&ev), Kind: "test_result"}
			refs, _ := json.Marshal([]schema.EvidenceRef{ref})
			var claim schema.Claim
			json.Unmarshal(call("POST", "/api/claims", `{"content":"works","evidence_refs":`+string(refs)+`,"workspace":"b"}`, "alice", 200), &claim)
			if claim.Revision != 1 || claim.Memory.Workspace != "a" {
				t.Fatalf("claim = %+v", claim)
			}
			path := "/api/claims/" + claim.Memory.ID
			call("GET", path+"?workspace=b", "", "alice", 200)
			var verified schema.Claim
			json.Unmarshal(call("POST", path+"/feedback", `{"action":"verify","expected_revision":1,"method":"attested","actor":"forged","evidence_refs":`+string(refs)+`,"workspace":"b"}`, "alice", 200), &verified)
			if verified.Revision != 2 || verified.Verification.Actor != "alice" {
				t.Fatalf("feedback forged: %+v", verified)
			}
			call("POST", path+"/feedback", `{"action":"reject","expected_revision":1,"reason":"bad"}`, "alice", 409)
			for _, body := range []string{`{"status":"verified","content":"bad"}`, `{"content":"bad","evidence_refs":"oops"}`, `{"content":"x"} {}`, `null`, `{}`} {
				call("POST", "/api/claims", body, "alice", 400)
			}
			call("POST", path+"/feedback", `{"expected_revision":1.5,"action":"reject","reason":"bad"}`, "alice", 400)
			foreign, err := e.Scope("b").CreateClaim("foreign private", nil, schema.ClaimScope{}, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			call("GET", "/api/claims/"+foreign.Memory.ID+"?workspace=b", "", "alice", 404)
			call("POST", "/api/claims/"+foreign.Memory.ID+"/feedback", `{"expected_revision":1,"action":"reject","reason":"bad","workspace":"b"}`, "alice", 404)
			foreignEv, err := e.Scope("b").RecordEvidence("observation", "foreign", "", "host")
			if err != nil {
				t.Fatal(err)
			}
			r, _ := json.Marshal([]schema.EvidenceRef{{MemoryID: foreignEv.ID, Fingerprint: schema.KnowledgeFingerprint(foreignEv), Kind: "observation"}})
			call("POST", "/api/claims", `{"content":"bad","evidence_refs":`+string(r)+`}`, "alice", 400)
			if err := e.Store.DeleteMemory(ev.ID); err != nil {
				t.Fatal(err)
			}
			if raw := string(call("GET", path, "", "alice", 200)); strings.Contains(raw, `"content":"works"`) || strings.Contains(raw, `"status":"verified"`) {
				t.Fatalf("stale body leaked: %s", raw)
			}
			cfg.AuthEnabled = false
			h = api.NewHandler(e, cfg)
			json.Unmarshal(call("POST", "/api/evidence", `{"kind":"observation","content":"local","actor":"forged"}`, "", 200), &ev)
			if ev.Source != "local" {
				t.Fatal("auth-off actor not local")
			}
			call(http.MethodPost, "/api/evidence/archive", `{"kind":"observation","content":"archive"}`, "", 400)
			cfg.Archive.Enabled = true
			json.Unmarshal(call("POST", "/api/evidence/archive", `{"kind":"observation","content":"password=p-secret","actor":"forged","metadata":{"archive_marker":"forged","original":"p-secret"}}`, "", 200), &ev)
			if ev.MetaString("actor") != "local" || ev.Meta("original") != nil || strings.Contains(ev.Content, "p-secret") {
				t.Fatalf("archive forged or leaked: %+v", ev)
			}
			call("PUT", "/api/memories/"+ev.ID, `{"content":"password=raw-secret"}`, "", 400)
		})
	}
}
