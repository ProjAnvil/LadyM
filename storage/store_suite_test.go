//go:build !enterprise

package storage

// Backend-parametrised behaviour suite: every test here runs the same case
// against SQLiteStore (always) and PostgresStore (when LADYM_TEST_PG_DSN is
// set), pinning the Store contract to identical observable behaviour on both
// backends. SQLite-specific tests (PRAGMA, BLOB codec, in-memory index
// internals, flock file) stay in store_crud_test.go.
//
// Personal edition only: the sqlite leg needs SQLiteStore/NewStore, which
// enterprise builds exclude. The shared PG gate (freshPGDatabase, suiteDim)
// lives in pg_gate_test.go so PG-only tests still run under -tags enterprise.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// runStoreBackends runs fn against every Store implementation: sqlite always
// (temp-dir database), postgres when LADYM_TEST_PG_DSN is set (the postgres
// subtest skips otherwise, leaving the sqlite subtest unaffected).
func runStoreBackends(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		s, err := NewStore(filepath.Join(t.TempDir(), "suite.db"), suiteDim, false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("LADYM_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("LADYM_TEST_PG_DSN not set")
		}
		s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
}

func suitePutMemory(t *testing.T, s Store, mem *schema.Memory, vector []float32) {
	t.Helper()
	if err := s.PutMemory(mem, vector); err != nil {
		t.Fatal(err)
	}
}

func TestSuitePutGetAndUpsert(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		// Not found → nil, nil.
		got, err := s.GetMemory("nope")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("GetMemory(nope) = %v, want nil", got)
		}

		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.Content = "the sky is blue"
		m.Summary = "sky fact"
		m.Tags = []string{"nature"}
		m.Metadata = map[string]any{"k": "v"}
		m.Source = "test"
		m.Workspace = "w1"
		m.ContentHash = "hash1"
		suitePutMemory(t, s, m, []float32{1, 0, 0, 0, 0, 0, 0, 0})

		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("GetMemory returned nil for stored memory")
		}
		if got.Content != m.Content || got.Summary != m.Summary || got.Source != m.Source ||
			got.Workspace != m.Workspace || got.ContentHash != m.ContentHash {
			t.Errorf("roundtrip mismatch: got %+v", got)
		}
		if len(got.Tags) != 1 || got.Tags[0] != "nature" {
			t.Errorf("tags = %v", got.Tags)
		}
		if got.Metadata["k"] != "v" {
			t.Errorf("metadata = %v", got.Metadata)
		}

		// Upsert same id (ON CONFLICT path) without a vector keeps working.
		m.Content = "updated content"
		m.Tags = nil // nil tags marshal to "null" → scan normalises to []
		m.Metadata = nil
		suitePutMemory(t, s, m, nil)
		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != "updated content" {
			t.Errorf("content after upsert = %q", got.Content)
		}
		if got.Tags == nil || len(got.Tags) != 0 {
			t.Errorf("tags after upsert = %v, want empty non-nil", got.Tags)
		}
		if got.Metadata == nil || len(got.Metadata) != 0 {
			t.Errorf("metadata after upsert = %v, want empty non-nil", got.Metadata)
		}

		// Vector dim mismatch is an error on both backends (in-memory index
		// dim check / pgvector vector(8) column check).
		if err := s.PutMemory(m, []float32{1, 2}); err == nil {
			t.Error("expected dim-mismatch error from PutMemory")
		}
	})
}

func TestSuiteDeleteAndTouch(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m.Content = "event"
		m2 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m2.Content = "event2"
		suitePutMemory(t, s, m, []float32{1, 0, 0, 0, 0, 0, 0, 0})
		suitePutMemory(t, s, m2, nil)

		// One batch call bumps every listed id in a single UPDATE.
		if err := s.TouchMemories([]string{m.ID, m2.ID}, 123.5); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{m.ID, m2.ID} {
			got, err := s.GetMemory(id)
			if err != nil {
				t.Fatal(err)
			}
			if got.AccessCount != 1 || got.LastAccessAt != 123.5 {
				t.Errorf("%s after touch: access_count=%d last_access_at=%v", id, got.AccessCount, got.LastAccessAt)
			}
		}

		// A second batch touches only the listed ids.
		if err := s.TouchMemories([]string{m.ID}, 200); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.AccessCount != 2 || got.LastAccessAt != 200 {
			t.Errorf("m after second touch: access_count=%d last_access_at=%v", got.AccessCount, got.LastAccessAt)
		}
		got2, err := s.GetMemory(m2.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got2.AccessCount != 1 || got2.LastAccessAt != 123.5 {
			t.Errorf("m2 must be untouched by second batch: access_count=%d last_access_at=%v", got2.AccessCount, got2.LastAccessAt)
		}

		// Empty slice is a no-op (no SQL round-trip).
		if err := s.TouchMemories(nil, 300); err != nil {
			t.Fatal(err)
		}
		if err := s.TouchMemories([]string{}, 300); err != nil {
			t.Fatal(err)
		}
		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.AccessCount != 2 || got.LastAccessAt != 200 {
			t.Errorf("empty touch changed state: access_count=%d last_access_at=%v", got.AccessCount, got.LastAccessAt)
		}

		// Ping succeeds on a live store.
		if err := s.Ping(); err != nil {
			t.Errorf("Ping on live store: %v", err)
		}

		if err := s.DeleteMemory(m.ID); err != nil {
			t.Fatal(err)
		}
		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("GetMemory after delete = %v, want nil", got)
		}
		// Deleting a missing id is a no-op.
		if err := s.DeleteMemory("missing"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSuiteIterMemoriesFilters(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m1 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m1.Workspace = "w1"
		m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m2.Workspace = "w1"
		m3 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m3.Workspace = "w2"
		for _, m := range []*schema.Memory{m1, m2, m3} {
			suitePutMemory(t, s, m, nil)
		}

		cases := []struct {
			name                  string
			workspace, layer, typ string
			want                  int
		}{
			{"all", "", "", "", 3},
			{"by workspace", "w1", "", "", 2},
			{"by layer", "", string(schema.LayerSemantic), "", 2},
			{"by type", "", "", string(schema.TypeEvent), 1},
			{"combined", "w2", string(schema.LayerSemantic), string(schema.TypeFact), 1},
			{"no match", "w9", "", "", 0},
		}
		for _, c := range cases {
			got, err := s.IterMemories(c.workspace, c.layer, c.typ)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if len(got) != c.want {
				t.Errorf("%s: len = %d, want %d", c.name, len(got), c.want)
			}
		}
	})
}

func TestSuiteFindByHash(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ContentHash = "h1"
		m.Workspace = "w1"
		suitePutMemory(t, s, m, nil)

		got, err := s.FindByHash("h1", "")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != m.ID {
			t.Errorf("FindByHash(h1, \"\") = %v", got)
		}
		got, err = s.FindByHash("h1", "w1")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.ID != m.ID {
			t.Errorf("FindByHash(h1, w1) = %v", got)
		}
		got, err = s.FindByHash("h1", "other")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("FindByHash(h1, other) = %v, want nil", got)
		}
		got, err = s.FindByHash("missing", "")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("FindByHash(missing) = %v, want nil", got)
		}
	})
}

func TestSuiteEpisodicContentsSince(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		old := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		old.Content = "old event"
		old.Workspace = "w"
		old.CreatedAt = 100
		recent := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		recent.Content = "recent event"
		recent.Workspace = "w"
		recent.CreatedAt = 200
		otherLayer := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		otherLayer.Content = "a fact"
		otherLayer.Workspace = "w"
		otherLayer.CreatedAt = 300
		otherWs := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		otherWs.Content = "other ws event"
		otherWs.Workspace = "w2"
		otherWs.CreatedAt = 400
		for _, m := range []*schema.Memory{old, recent, otherLayer, otherWs} {
			suitePutMemory(t, s, m, nil)
		}

		got, err := s.EpisodicContentsSince("w", 150)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != "recent event" {
			t.Errorf("EpisodicContentsSince(w, 150) = %v, want [recent event]", got)
		}
		got, err = s.EpisodicContentsSince("w", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Errorf("EpisodicContentsSince(w, 0) = %v, want 2 items", got)
		}
	})
}

func TestSuiteCount(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m1 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m1.Workspace = "w1"
		m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m2.Workspace = "w1"
		m3 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m3.Workspace = "w2"
		for _, m := range []*schema.Memory{m1, m2, m3} {
			suitePutMemory(t, s, m, nil)
		}

		all, err := s.Count("")
		if err != nil {
			t.Fatal(err)
		}
		if all["L1_episodic/event"] != 1 || all["L2_semantic/fact"] != 2 {
			t.Errorf("Count(\"\") = %v", all)
		}
		w1, err := s.Count("w1")
		if err != nil {
			t.Fatal(err)
		}
		if w1["L2_semantic/fact"] != 1 || w1["L1_episodic/event"] != 1 {
			t.Errorf("Count(w1) = %v", w1)
		}
	})
}

func TestSuiteEdgeCRUD(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		c := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		for _, m := range []*schema.Memory{a, b, c} {
			suitePutMemory(t, s, m, nil)
		}

		e1 := schema.NewEdge(a.ID, "related_to", b.ID)
		e1.Metadata = map[string]any{"why": "test"}
		e2 := schema.NewEdge(a.ID, "supports", c.ID)
		end := 999.0
		e3 := schema.NewEdge(b.ID, "related_to", c.ID)
		e3.ValidTo = &end // invalidated edge must not show up
		for _, e := range []*schema.Edge{e1, e2, e3} {
			if err := s.PutEdge(e); err != nil {
				t.Fatal(err)
			}
		}

		n, err := s.CountEdges()
		if err != nil {
			t.Fatal(err)
		}
		if n != 3 {
			t.Errorf("CountEdges = %d, want 3", n)
		}

		// Neighbors without relation filter: valid edges touching a only.
		edges, err := s.Neighbors(a.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 2 {
			t.Fatalf("Neighbors(a) = %d edges, want 2", len(edges))
		}
		for _, e := range edges {
			if e.ValidTo != nil {
				t.Errorf("unexpected invalidated edge %s", e.ID)
			}
			if e.Metadata["why"] == "test" && (e.SrcID != a.ID || e.DstID != b.ID || e.Relation != "related_to") {
				t.Errorf("roundtrip mismatch: %+v", e)
			}
		}

		// Relation filter.
		edges, err = s.Neighbors(a.ID, "supports")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].DstID != c.ID {
			t.Errorf("Neighbors(a, supports) = %v", edges)
		}

		// Upsert: same id updates in place (ON CONFLICT path + non-nil
		// valid_to roundtrip).
		e1.Weight = 2.5
		vto := 500.0
		e1.ValidTo = &vto
		if err := s.PutEdge(e1); err != nil {
			t.Fatal(err)
		}
		edges, err = s.Neighbors(a.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 {
			t.Errorf("Neighbors(a) after invalidating e1 = %d edges, want 1", len(edges))
		}

		counts, err := s.NeighborCounts()
		if err != nil {
			t.Fatal(err)
		}
		// Only e2 is still valid: touches a and c once each.
		if counts[a.ID] != 1 || counts[c.ID] != 1 || counts[b.ID] != 0 {
			t.Errorf("NeighborCounts = %v", counts)
		}

		// InvalidateEdge marks the edge no longer current.
		if err := s.InvalidateEdge(e2.ID, 600); err != nil {
			t.Fatal(err)
		}
		edges, err = s.Neighbors(a.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 0 {
			t.Errorf("Neighbors(a) after InvalidateEdge = %d edges, want 0", len(edges))
		}
	})
}

func TestSuiteCodeSymbols(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
		suitePutMemory(t, s, m, nil)
		m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
		suitePutMemory(t, s, m2, nil)

		sym := &schema.CodeSymbol{
			MemoryID: m.ID, FilePath: "pkg/a.go", SymbolKind: "function",
			QualifiedName: "pkg.F", Signature: "func F()", Docstring: "doc",
			LineStart: 10, LineEnd: 20, Language: "go",
		}
		if err := s.PutCodeSymbol(sym); err != nil {
			t.Fatal(err)
		}
		// Upsert the same memory_id with a new line range.
		sym.LineStart = 12
		if err := s.PutCodeSymbol(sym); err != nil {
			t.Fatal(err)
		}
		sym2 := &schema.CodeSymbol{
			MemoryID: m2.ID, FilePath: "pkg/a.go", SymbolKind: "method",
			QualifiedName: "pkg.G", LineStart: 1, LineEnd: 5, Language: "go",
		}
		if err := s.PutCodeSymbol(sym2); err != nil {
			t.Fatal(err)
		}

		got, err := s.SymbolsForFile("pkg/a.go")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("SymbolsForFile = %d symbols, want 2", len(got))
		}
		// Ordered by line_start: G (1) before F (12).
		if got[0].QualifiedName != "pkg.G" || got[1].QualifiedName != "pkg.F" {
			t.Errorf("order = %q, %q", got[0].QualifiedName, got[1].QualifiedName)
		}
		if got[1].LineStart != 12 || got[1].Signature != "func F()" || got[1].Docstring != "doc" {
			t.Errorf("roundtrip mismatch: %+v", got[1])
		}
		got, err = s.SymbolsForFile("nope.go")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("SymbolsForFile(nope.go) = %v, want empty", got)
		}

		n, err := s.CountCodeSymbols()
		if err != nil {
			t.Fatal(err)
		}
		if n != 2 {
			t.Errorf("CountCodeSymbols = %d, want 2", n)
		}
	})
}

func TestSuiteCodeRefs(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		refs := []*schema.CodeRef{
			{SrcSymbol: "pkg.F", DstSymbol: "pkg.G", RefKind: "calls"},
			{SrcSymbol: "pkg.H", DstSymbol: "pkg.F", RefKind: "imports"},
		}
		if err := s.PutCodeRefs(refs); err != nil {
			t.Fatal(err)
		}
		// Empty batch is a no-op.
		if err := s.PutCodeRefs(nil); err != nil {
			t.Fatal(err)
		}

		out, err := s.RefsForSymbol("pkg.F", "out")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].DstSymbol != "pkg.G" {
			t.Errorf("out refs = %v", out)
		}
		in, err := s.RefsForSymbol("pkg.F", "in")
		if err != nil {
			t.Fatal(err)
		}
		if len(in) != 1 || in[0].SrcSymbol != "pkg.H" {
			t.Errorf("in refs = %v", in)
		}
		both, err := s.RefsForSymbol("pkg.F", "both")
		if err != nil {
			t.Fatal(err)
		}
		if len(both) != 2 {
			t.Errorf("both refs = %v, want 2", both)
		}
		none, err := s.RefsForSymbol("pkg.F", "sideways")
		if err != nil {
			t.Fatal(err)
		}
		if len(none) != 0 {
			t.Errorf("unknown direction = %v, want empty", none)
		}
	})
}

// TestSuiteDeleteMemories covers the two bulk deletes the code indexer
// re-index write path needs.
func TestSuiteDeleteMemories(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		target := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
		target.Source = "codeindex"
		target.Workspace = "ws"
		suitePutMemory(t, s, target, nil)
		otherWs := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
		otherWs.Source = "codeindex"
		otherWs.Workspace = "other"
		suitePutMemory(t, s, otherWs, nil)
		if err := s.PutCodeSymbol(&schema.CodeSymbol{
			MemoryID: target.ID, FilePath: "f.go", SymbolKind: "function",
			QualifiedName: "pkg.F", LineStart: 1, LineEnd: 2, Language: "go",
		}); err != nil {
			t.Fatal(err)
		}

		// DeleteMemoriesByTypeSource is scoped to type+source+workspace.
		if err := s.DeleteMemoriesByTypeSource(string(schema.TypeCodeSymbol), "codeindex", "other"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetMemory(otherWs.ID); got != nil {
			t.Error("DeleteMemoriesByTypeSource should remove the other-workspace row")
		}
		if got, _ := s.GetMemory(target.ID); got == nil {
			t.Error("DeleteMemoriesByTypeSource must not touch other workspaces")
		}

		// DeleteSymbolMemories removes via the code_symbols projection.
		if err := s.DeleteSymbolMemories("pkg.F", "ws"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetMemory(target.ID); got != nil {
			t.Error("DeleteSymbolMemories should remove the projected memory")
		}
	})
}

func TestSuiteIndexState(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		h, err := s.GetIndexedHash("a.go")
		if err != nil {
			t.Fatal(err)
		}
		if h != "" {
			t.Errorf("GetIndexedHash(a.go) = %q, want \"\"", h)
		}

		if err := s.SetIndexed("a.go", "hash1", 100); err != nil {
			t.Fatal(err)
		}
		h, err = s.GetIndexedHash("a.go")
		if err != nil {
			t.Fatal(err)
		}
		if h != "hash1" {
			t.Errorf("GetIndexedHash = %q, want hash1", h)
		}

		// Upsert; now==0 uses schema.Now().
		if err := s.SetIndexed("a.go", "hash2", 0); err != nil {
			t.Fatal(err)
		}
		h, err = s.GetIndexedHash("a.go")
		if err != nil {
			t.Fatal(err)
		}
		if h != "hash2" {
			t.Errorf("GetIndexedHash after upsert = %q, want hash2", h)
		}
	})
}

func TestSuiteWorkspacesAndMeta(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		ws, err := s.Workspaces()
		if err != nil {
			t.Fatal(err)
		}
		if len(ws) != 0 {
			t.Errorf("Workspaces on empty store = %v", ws)
		}
		m1 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m1.Workspace = "beta"
		m2 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m2.Workspace = "alpha"
		suitePutMemory(t, s, m1, nil)
		suitePutMemory(t, s, m2, nil)
		ws, err = s.Workspaces()
		if err != nil {
			t.Fatal(err)
		}
		if len(ws) != 2 || ws[0] != "alpha" || ws[1] != "beta" {
			t.Errorf("Workspaces = %v, want [alpha beta]", ws)
		}

		v, err := s.GetMeta("missing")
		if err != nil {
			t.Fatal(err)
		}
		if v != "" {
			t.Errorf("GetMeta(missing) = %q, want \"\"", v)
		}
		if err := s.SetMeta("k", "v1"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMeta("k", "v2"); err != nil {
			t.Fatal(err)
		}
		v, err = s.GetMeta("k")
		if err != nil {
			t.Fatal(err)
		}
		if v != "v2" {
			t.Errorf("GetMeta(k) = %q, want v2", v)
		}
	})
}

// TestSuiteUsersCRUD pins the users-table contract (HTTP Basic auth accounts)
// to identical behaviour on both backends.
func TestSuiteUsersCRUD(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		// Missing user → nil, nil.
		got, err := s.GetUser("nope")
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("GetUser(nope) = %v, want nil", got)
		}

		// Empty store lists no users.
		users, err := s.ListUsers()
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 0 {
			t.Errorf("ListUsers on empty store = %v", users)
		}

		admin := &schema.User{Username: "root", PasswordHash: "$2a$10$bcrypt-hash", Workspace: "", Admin: true, CreatedAt: 100.5}
		alice := &schema.User{Username: "alice", PasswordHash: "", Workspace: "acme", Admin: false, CreatedAt: 200}
		bob := &schema.User{Username: "bob", PasswordHash: "hash-b", Workspace: "globex", Admin: false, CreatedAt: 300}
		for _, u := range []*schema.User{bob, admin, alice} {
			if err := s.PutUser(u); err != nil {
				t.Fatal(err)
			}
		}

		got, err = s.GetUser("alice")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("GetUser(alice) = nil")
		}
		if got.Username != "alice" || got.PasswordHash != "" || got.Workspace != "acme" ||
			got.Admin || got.CreatedAt != 200 {
			t.Errorf("roundtrip mismatch: %+v", got)
		}

		// ListUsers is sorted by username.
		users, err = s.ListUsers()
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 3 || users[0].Username != "alice" || users[1].Username != "bob" || users[2].Username != "root" {
			names := []string{}
			for _, u := range users {
				names = append(names, u.Username)
			}
			t.Errorf("ListUsers order = %v, want [alice bob root]", names)
		}

		// Upsert: same username updates in place (passwd flow).
		alice.PasswordHash = "new-hash"
		alice.Admin = true
		if err := s.PutUser(alice); err != nil {
			t.Fatal(err)
		}
		got, err = s.GetUser("alice")
		if err != nil {
			t.Fatal(err)
		}
		if got.PasswordHash != "new-hash" || !got.Admin {
			t.Errorf("after upsert: %+v", got)
		}

		if err := s.DeleteUser("bob"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.GetUser("bob"); got != nil {
			t.Errorf("GetUser(bob) after delete = %v, want nil", got)
		}
		// Deleting a missing user is a no-op.
		if err := s.DeleteUser("bob"); err != nil {
			t.Fatal(err)
		}
	})
}

// embeddingColumnPresent reports whether the memory row still carries a
// non-NULL embedding, checked on the raw column of whichever backend s is.
func embeddingColumnPresent(t *testing.T, s Store, id string) bool {
	t.Helper()
	switch st := s.(type) {
	case *SQLiteStore:
		var n int
		if err := st.db.QueryRow(
			"SELECT COUNT(*) FROM memories WHERE id = ? AND embedding IS NOT NULL", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	case *PostgresStore:
		var ok bool
		if err := st.pool.QueryRow(context.Background(),
			"SELECT embedding IS NOT NULL FROM memories WHERE id = $1", id).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	default:
		t.Fatalf("unknown store type %T", s)
		return false
	}
}

// TestSuiteUpdateMemoryContent pins the targeted memory-update contract used
// by the console CRUD API: fields and updated_at always change; a nil vector
// leaves the embedding column (and content_hash) untouched, a non-nil vector
// rewrites both. This is the guard against the PutMemory(mem, nil) trap,
// which NULLs the embedding column on upsert.
func TestSuiteUpdateMemoryContent(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		v1 := []float32{1, 0, 0, 0, 0, 0, 0, 0}
		v2 := []float32{0, 1, 0, 0, 0, 0, 0, 0}

		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.Content = "original content"
		m.Summary = "original summary"
		m.Tags = []string{"a"}
		m.Workspace = "w1"
		m.ContentHash = schema.ContentHash(m.Content)
		suitePutMemory(t, s, m, v1)

		// nil vector: fields update, embedding column and content_hash stay.
		if err := s.UpdateMemoryContent(m.ID, "rewritten content", "rewritten summary",
			[]string{"b", "c"}, nil, 555.0); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != "rewritten content" || got.Summary != "rewritten summary" ||
			got.UpdatedAt != 555.0 {
			t.Errorf("after nil-vector update: %+v", got)
		}
		if len(got.Tags) != 2 || got.Tags[0] != "b" || got.Tags[1] != "c" {
			t.Errorf("tags after update = %v", got.Tags)
		}
		if got.ContentHash != schema.ContentHash("original content") {
			t.Errorf("content_hash changed on nil-vector update: %q", got.ContentHash)
		}
		if !embeddingColumnPresent(t, s, m.ID) {
			t.Error("nil-vector update NULLed the embedding column (PutMemory trap)")
		}
		// The old vector still scores the memory (index untouched).
		hits := s.VectorSearch(v1, 5)
		if len(hits) == 0 || hits[0].ID != m.ID {
			t.Errorf("VectorSearch(v1) after nil-vector update = %v, want top hit %s", hits, m.ID)
		}

		// Non-nil vector: embedding + content_hash move with the content.
		if err := s.UpdateMemoryContent(m.ID, "rewritten again", "summary2",
			nil, v2, 556.0); err != nil {
			t.Fatal(err)
		}
		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Content != "rewritten again" || got.Summary != "summary2" || got.UpdatedAt != 556.0 {
			t.Errorf("after vector update: %+v", got)
		}
		if len(got.Tags) != 0 {
			t.Errorf("tags after nil-tags update = %v, want empty", got.Tags)
		}
		if got.ContentHash != schema.ContentHash("rewritten again") {
			t.Errorf("content_hash after vector update = %q, want hash of new content", got.ContentHash)
		}
		hits = s.VectorSearch(v2, 5)
		if len(hits) == 0 || hits[0].ID != m.ID {
			t.Errorf("VectorSearch(v2) after vector update = %v, want top hit %s", hits, m.ID)
		}
		hits = s.VectorSearch(v1, 5)
		if len(hits) != 0 && hits[0].ID == m.ID && hits[0].Similarity > 0.9 {
			t.Errorf("stale vector v1 still scores %s at %.3f after vector update", m.ID, hits[0].Similarity)
		}

		// now==0 falls back to the wall clock.
		before := schema.Now()
		if err := s.UpdateMemoryContent(m.ID, "c", "s", nil, nil, 0); err != nil {
			t.Fatal(err)
		}
		got, err = s.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.UpdatedAt < before {
			t.Errorf("updated_at with now=0 = %v, want >= %v (wall clock)", got.UpdatedAt, before)
		}

		// Updating a missing id is a no-op (the api layer 404s beforehand).
		if err := s.UpdateMemoryContent("missing", "c", "s", nil, v2, 1); err != nil {
			t.Fatal(err)
		}
	})
}

// TestSuiteIndexLockConflict pins the cross-process index-lock contract: a
// second acquire fails fast with ErrIndexLockHeld, and the lock can be taken
// again after release. On the PG backend this exercises the advisory lock.
func TestSuiteIndexLockConflict(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		release, err := s.TryAcquireIndexLock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.TryAcquireIndexLock(); !errors.Is(err, ErrIndexLockHeld) {
			t.Errorf("second acquire err = %v, want ErrIndexLockHeld", err)
		}
		release()
		release2, err := s.TryAcquireIndexLock()
		if err != nil {
			t.Fatalf("acquire after release: %v", err)
		}
		release2()
	})
}
