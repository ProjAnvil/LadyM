//go:build !enterprise

package code

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// indexerProjEnv is a two-file repo: svc.py carries an intra-file call
// (caller→helper, the only ref shape BuildRefs emits), other.py an unrelated
// symbol whose artifacts must survive a reindex of svc.py.
const (
	svcWithHelper = `def helper() -> int:
    return 1

def caller() -> int:
    return helper()
`
	svcWithoutHelper = `def caller() -> int:
    return 2
`
	otherSrc = `def unrelated() -> int:
    return 3
`
)

func newProjectionEnv(t *testing.T) (storage.Store, *storage.HashingEmbedding, *config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	writeProjFile(t, dir, "svc.py", svcWithHelper)
	writeProjFile(t, dir, "other.py", otherSrc)
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	emb := storage.NewHashingEmbedding(256)
	cfg := config.ForTesting(t.TempDir())
	return store, emb, cfg, dir
}

func writeProjFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symbolIDByQName(t *testing.T, s storage.Store, file, qname string) string {
	t.Helper()
	syms, err := s.SymbolsForFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range syms {
		if sym.QualifiedName == qname {
			return sym.MemoryID
		}
	}
	t.Fatalf("symbol %q not found in %s (have %+v)", qname, file, syms)
	return ""
}

func TestIndexerProjectsCodeRefsAsEdges(t *testing.T) {
	store, emb, cfg, dir := newProjectionEnv(t)

	rep, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.RefsWritten != 1 {
		t.Fatalf("refs_written = %d, want 1", rep.RefsWritten)
	}
	if rep.ProjectedEdges != 1 {
		t.Fatalf("projected_edges = %d, want 1", rep.ProjectedEdges)
	}

	callerID := symbolIDByQName(t, store, "svc.py", "svc.caller")
	helperID := symbolIDByQName(t, store, "svc.py", "svc.helper")
	edges, err := store.Neighbors(callerID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("calls edges from svc.caller = %d, want 1", len(edges))
	}
	if edges[0].DstID != helperID || edges[0].SrcID != callerID {
		t.Errorf("calls edge endpoints = %s→%s, want %s→%s",
			edges[0].SrcID, edges[0].DstID, callerID, helperID)
	}
	if want := schema.ProjectedEdgeID(callerID, "calls", helperID); edges[0].ID != want {
		t.Errorf("calls edge id = %s, want projected %s", edges[0].ID, want)
	}

	// The unrelated file's symbol has no edges.
	unrelatedID := symbolIDByQName(t, store, "other.py", "other.unrelated")
	ue, err := store.Neighbors(unrelatedID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ue) != 0 {
		t.Errorf("edges touching other.unrelated = %d, want 0", len(ue))
	}
}

func TestIndexerReindexCleansRemovedSymbol(t *testing.T) {
	store, emb, cfg, dir := newProjectionEnv(t)
	if _, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil); err != nil {
		t.Fatal(err)
	}
	helperID := symbolIDByQName(t, store, "svc.py", "svc.helper")

	// Remove helper from svc.py and reindex (body hash changed).
	writeProjFile(t, dir, "svc.py", svcWithoutHelper)
	rep, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesIndexed != 1 {
		t.Fatalf("files_indexed = %d, want 1 (only svc.py changed)", rep.FilesIndexed)
	}

	// Old symbol memory, projection row, and refs are all cleaned.
	got, err := store.GetMemory(helperID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("removed symbol's memory survived the reindex")
	}
	if syms, err := store.SymbolsForFile("svc.py"); err != nil {
		t.Fatal(err)
	} else if len(syms) != 1 || syms[0].QualifiedName != "svc.caller" {
		t.Errorf("svc.py symbols after reindex = %+v, want only svc.caller", syms)
	}
	if refs, err := store.RefsForSymbol("svc.helper", "both"); err != nil {
		t.Fatal(err)
	} else if len(refs) != 0 {
		t.Errorf("refs touching svc.helper after reindex = %+v, want none", refs)
	}
	// Unrelated file untouched.
	if syms, err := store.SymbolsForFile("other.py"); err != nil {
		t.Fatal(err)
	} else if len(syms) != 1 {
		t.Errorf("other.py symbols after reindex = %+v, want 1 (untouched)", syms)
	}

	// No duplicate edge is projected for the rewritten caller: the new
	// caller memory carries no calls edges. (The old edge's fate — cascade
	// delete here, dangling in legacy no-FK databases — is GC's business,
	// not asserted.)
	newCallerID := symbolIDByQName(t, store, "svc.py", "svc.caller")
	edges, err := store.Neighbors(newCallerID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("calls edges from re-projected svc.caller = %d, want 0 (no duplicate projection)", len(edges))
	}
}

func TestIndexerProjectionIdempotent(t *testing.T) {
	store, emb, cfg, dir := newProjectionEnv(t)
	if _, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil); err != nil {
		t.Fatal(err)
	}
	n1, err := store.CountEdges()
	if err != nil {
		t.Fatal(err)
	}

	// Unchanged files: nothing is re-written or re-projected.
	rep, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProjectedEdges != 0 {
		t.Errorf("projected_edges on no-op pass = %d, want 0", rep.ProjectedEdges)
	}
	n2, err := store.CountEdges()
	if err != nil {
		t.Fatal(err)
	}
	if n2 != n1 {
		t.Errorf("edge count after unchanged reindex = %d, want %d", n2, n1)
	}

	// Forced full re-index rewrites every symbol and re-projects the refs;
	// deterministic ids upsert, so the edge count still does not grow.
	rep, err = IndexCodebase(dir, store, emb, cfg, "test", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ProjectedEdges != 1 {
		t.Errorf("projected_edges on forced pass = %d, want 1", rep.ProjectedEdges)
	}
	n3, err := store.CountEdges()
	if err != nil {
		t.Fatal(err)
	}
	if n3 != n1 {
		t.Errorf("edge count after forced reindex = %d, want %d (re-projection must not duplicate)", n3, n1)
	}
}
