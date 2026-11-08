package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestArchiveOwnershipAndRedactedEntryLimit(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("archive")
		e.Config.Archive.Enabled = true
		e.Config.Archive.MaxEntryBytes = 10
		if _, err := s.ArchiveEvidence("observation", "token=x", "", "host"); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("expanded redacted text exceeded entry cap: %v", err)
		}
		if _, err := s.Remember("original secret", schema.LayerSemantic, schema.TypeFact, nil, map[string]any{"archive_marker": "ladym.archive.v1"}, "host", ""); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("caller forged archive ownership: %v", err)
		}
	})
}

func TestArchiveQuotaAndRedaction(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("archive")
		if _, err := s.ArchiveEvidence("test_result", "passed", "success", "host"); err == nil {
			t.Fatal("archive enabled by default")
		}

		e.Config.Archive.Enabled = true
		if _, err := s.ArchiveEvidence("observation", "password="+strings.Repeat("s", 65528), "", "host"); err == nil {
			t.Fatal("oversized raw input accepted after redaction")
		}
		content := `{"password":"p-secret","api_key":"k-secret","token":"t-secret"}
Authorization: Bearer b-secret
PASSWORD='a secret with spaces'
api-key = k2-secret`
		m, err := s.ArchiveEvidence("test_result", content, "success", "host")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := e.Store.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		meta, err := json.Marshal(raw.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"p-secret", "k-secret", "t-secret", "b-secret", "a secret with spaces", "k2-secret"} {
			if strings.Contains(raw.Content, secret) || strings.Contains(string(meta), secret) {
				t.Fatalf("stored secret %q", secret)
			}
		}
		if raw.MetaString("archive_marker") == "" || raw.ContentHash != schema.ContentHash(raw.Content) {
			t.Fatal("archive provenance missing")
		}
		until, ok := raw.MetaFloat("valid_until")
		if !ok || until <= schema.Now() {
			t.Fatal("retention missing")
		}
		ref := schema.EvidenceRef{MemoryID: raw.ID, Fingerprint: schema.KnowledgeFingerprint(raw), Kind: "test_result"}
		c, err := s.CreateClaim("derived", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		raw.Metadata["valid_until"], raw.Metadata["pinned"] = schema.Now()-1, true
		if err := e.Store.PutMemory(raw, nil); err != nil {
			t.Fatal(err)
		}
		c, err = s.GetClaim(c.Memory.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: c.Revision, Action: "verify", Actor: "host", Method: "attested", Evidence: []schema.EvidenceRef{ref}}); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("expired archive verified: %v", err)
		}
		e.Config.Archive.MaxWorkspaceBytes = len(raw.Content) + 10
		e.Config.Archive.MaxEntryBytes = 64
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				_, err := s.ArchiveEvidence("observation", "1234567890", "", "host")
				results <- err
			})
		}
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if !errors.Is(err, storage.ErrMemoryQuota) {
				t.Fatalf("quota error = %v", err)
			}
		}
		if wins != 1 {
			t.Fatalf("concurrent inserts = %d; expired pinned bytes excluded or race", wins)
		}
		// Other workspaces have independent quotas; reopen retains accounting.
		if _, err := e.Scope("other").ArchiveEvidence("observation", "1234567890", "", "host"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ArchiveEvidence("observation", "x", "", "host"); !errors.Is(err, storage.ErrMemoryQuota) {
			t.Fatalf("persisted quota = %v", err)
		}
		if err := e.Store.DeleteMemory(raw.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetClaim(c.Memory.ID); err != nil || got.Status != "stale" || got.Memory.Content != "" {
			t.Fatalf("explicit delete propagation = %+v %v", got, err)
		}
		if _, err := s.ArchiveEvidence("observation", "x", "", "host"); err != nil {
			t.Fatal(err)
		}
	})
}
