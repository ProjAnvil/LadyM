package ladym_test

import (
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/ladym"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestClaimLocalSDK(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "sqlite" && storage.Edition == "enterprise" {
				t.Skip("SQLite excluded")
			}
			cfg := config.ForTesting(t.TempDir())
			cfg.StoreBackend = backend
			cfg.Archive = ladym.ArchiveConfig{Enabled: true, MaxEntryBytes: 65536, MaxWorkspaceBytes: 8388608, RetentionS: 60}
			if backend == "postgres" {
				dsn := os.Getenv("LADYM_TEST_PG_DSN")
				if dsn == "" {
					t.Skip("LADYM_TEST_PG_DSN not set")
				}
				cfg.StoreDSN = testutil.PGDatabase(t, dsn)
			}
			e, err := ladym.NewEngine(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { e.Close() })
			ev, err := e.ArchiveEvidence("test_result", "passed", "success", "host", "w")
			if err != nil {
				t.Fatal(err)
			}
			if ev.Type != ladym.TypeEvidence {
				t.Fatal("wrong evidence type")
			}
			refs := []ladym.EvidenceRef{{MemoryID: ev.ID, Fingerprint: ladym.KnowledgeFingerprint(ev), Kind: "test_result"}}
			c, err := e.CreateClaim("works", refs, ladym.ClaimScope{}, "", 0, "w")
			if err != nil {
				t.Fatal(err)
			}
			var claim *ladym.Claim = c
			claim, err = e.ApplyClaimFeedback(claim.Memory.ID, ladym.ClaimFeedback{ExpectedRevision: 1, Action: "verify", Method: "attested", Actor: "host", Evidence: refs}, "w")
			if err != nil || claim.Memory.Type != ladym.TypeClaim || claim.Revision != 2 {
				t.Fatalf("local SDK=%+v %v", claim, err)
			}
		})
	}
}
