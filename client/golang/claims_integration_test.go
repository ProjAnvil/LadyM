package client

import (
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestClaimHTTPClientPipeline(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend, cfg.AuthEnabled, cfg.Archive.Enabled = backend, true, true
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
			if err := e.Store.PutUser(&schema.User{Username: "alice", Workspace: "w"}); err != nil {
				t.Fatal(err)
			}
			h := httptest.NewServer(api.NewHandler(e, cfg))
			t.Cleanup(h.Close)
			c := New(h.URL, WithAuth("alice", ""), WithActor("forged"))
			ev, err := c.RecordEvidence(t.Context(), RecordEvidenceRequest{Kind: "test_result", Content: "passed", Outcome: "success", Workspace: "other"})
			if err != nil {
				t.Fatal(err)
			}
			if ev.Workspace != "w" || ev.Source != "alice" {
				t.Fatalf("host attribution forged=%+v", ev)
			}
			claim, err := c.CreateClaim(t.Context(), CreateClaimRequest{Content: "works", Workspace: "other", Scope: ClaimScope{Environment: "test", CodeVersion: "v1", Conditions: []string{"ready"}}, Evidence: []EvidenceRef{{MemoryID: ev.ID, Kind: "test_result", Fingerprint: schema.KnowledgeFingerprint(ev)}}})
			if err != nil {
				t.Fatal(err)
			}
			claim, err = c.ApplyClaimFeedback(t.Context(), claim.Memory.ID, ClaimFeedbackRequest{Workspace: "other", ClaimFeedback: ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "attested", Evidence: claim.Evidence}})
			if err != nil || claim.Revision != 2 || claim.Verification.Actor != "alice" {
				t.Fatalf("feedback=%+v %v", claim, err)
			}
			got, err := c.GetClaim(t.Context(), claim.Memory.ID, "other")
			if err != nil || got.Status != "verified" || got.Scope.CodeVersion != "v1" {
				t.Fatalf("get=%+v %v", got, err)
			}
			archive, err := c.ArchiveEvidence(t.Context(), RecordEvidenceRequest{Kind: "observation", Content: `{"token":"synthetic-secret"}`, Workspace: "other"})
			if err != nil || archive.MetaString("archive_marker") == "" || archive.Workspace != "w" {
				t.Fatalf("archive=%+v %v", archive, err)
			}
			for _, tc := range []struct {
				status int
				run    func() error
			}{
				{409, func() error {
					_, err := c.ApplyClaimFeedback(t.Context(), claim.Memory.ID, ClaimFeedbackRequest{ClaimFeedback: ClaimFeedback{ExpectedRevision: 1, Action: "reject", Reason: "bad"}})
					return err
				}},
				{404, func() error { _, err := c.GetClaim(t.Context(), "missing", "w"); return err }},
				{400, func() error {
					_, err := c.CreateClaim(t.Context(), CreateClaimRequest{Content: "bad", Status: "verified"})
					return err
				}},
				{401, func() error { _, err := New(h.URL).GetClaim(t.Context(), claim.Memory.ID, "w"); return err }},
			} {
				err := tc.run()
				if apiErr, ok := errors.AsType[*Error](err); !ok || apiErr.StatusCode != tc.status {
					t.Fatalf("error status want %d: %v", tc.status, err)
				}
			}
		})
	}
}
