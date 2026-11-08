package engine

import (
	"errors"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestArchivePersistedQuotaAcrossHandles(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		e.Config.Archive.Enabled = true
		e.Config.Archive.MaxWorkspaceBytes = 8
		a, err := e.Scope("archive").ArchiveEvidence("observation", "1234", "", "host")
		if err != nil {
			t.Fatal(err)
		}
		reopened, err := New(e.Config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reopened.Close() })
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, eng := range []*Engine{e, reopened} {
			wg.Go(func() {
				_, err := eng.Scope("archive").ArchiveEvidence("observation", "😊", "", "host")
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
				t.Fatalf("unexpected error=%v", err)
			}
		}
		if wins != 1 {
			t.Fatalf("cross-handle byte quota wins=%d", wins)
		}
		rows, err := reopened.Store.IterMemories("archive", "", "")
		if err != nil {
			t.Fatal(err)
		}
		bytes := 0
		for _, row := range rows {
			bytes += len(row.Content)
		}
		if bytes != 8 {
			t.Fatalf("persisted bytes=%d", bytes)
		}
		ref := schema.EvidenceRef{MemoryID: a.ID, Fingerprint: schema.KnowledgeFingerprint(a), Kind: "observation"}
		if _, err := reopened.Scope("archive").CreateClaim("from persisted archive", []schema.EvidenceRef{ref}, schema.ClaimScope{}, "", 0); err != nil {
			t.Fatal(err)
		}
	})
}
