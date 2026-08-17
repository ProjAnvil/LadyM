package storage

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func putTestMemory(t *testing.T, s *SQLiteStore, mem *schema.Memory, vector []float32) {
	t.Helper()
	if err := s.PutMemory(mem, vector); err != nil {
		t.Fatal(err)
	}
}

func TestPutAndGetMemory(t *testing.T) {
	s := openTestStore(t)

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
	vec := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	putTestMemory(t, s, m, vec)

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
	// Vector index updated too.
	if s.VectorIndex().Len() != 1 {
		t.Errorf("index len = %d, want 1", s.VectorIndex().Len())
	}

	// Upsert same id (ON CONFLICT path) without vector keeps working.
	m.Content = "updated content"
	m.Tags = nil // nil tags marshal to "null" → scanMemory normalises to []
	m.Metadata = nil
	putTestMemory(t, s, m, nil)
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
	// No new vector inserted when vector == nil.
	if s.VectorIndex().Len() != 1 {
		t.Errorf("index len after vector-less upsert = %d, want 1", s.VectorIndex().Len())
	}

	// Vector dim mismatch propagates from the index.
	if err := s.PutMemory(m, []float32{1, 2}); err == nil {
		t.Error("expected dim-mismatch error from PutMemory")
	}
}

func TestDeleteAndTouchMemory(t *testing.T) {
	s := openTestStore(t)
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m.Content = "event"
	putTestMemory(t, s, m, []float32{1, 0, 0, 0, 0, 0, 0, 0})

	if err := s.TouchMemory(m.ID, 123.5); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMemory(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessCount != 1 || got.LastAccessAt != 123.5 {
		t.Errorf("after touch: access_count=%d last_access_at=%v", got.AccessCount, got.LastAccessAt)
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
	if s.VectorIndex().Len() != 0 {
		t.Errorf("index len after delete = %d, want 0", s.VectorIndex().Len())
	}
	// Deleting a missing id is a no-op.
	if err := s.DeleteMemory("missing"); err != nil {
		t.Fatal(err)
	}
}

func TestIterMemoriesFilters(t *testing.T) {
	s := openTestStore(t)
	m1 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m1.Workspace = "w1"
	m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m2.Workspace = "w1"
	m3 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m3.Workspace = "w2"
	for _, m := range []*schema.Memory{m1, m2, m3} {
		putTestMemory(t, s, m, nil)
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
}

func TestFindByHash(t *testing.T) {
	s := openTestStore(t)
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.ContentHash = "h1"
	m.Workspace = "w1"
	putTestMemory(t, s, m, nil)

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
}

func TestEpisodicContentsSince(t *testing.T) {
	s := openTestStore(t)
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
		putTestMemory(t, s, m, nil)
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
}

func TestCount(t *testing.T) {
	s := openTestStore(t)
	m1 := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m1.Workspace = "w1"
	m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m2.Workspace = "w1"
	m3 := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m3.Workspace = "w2"
	for _, m := range []*schema.Memory{m1, m2, m3} {
		putTestMemory(t, s, m, nil)
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
}

func TestEdgeCRUD(t *testing.T) {
	s := openTestStore(t)
	a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	c := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	for _, m := range []*schema.Memory{a, b, c} {
		putTestMemory(t, s, m, nil)
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

	// Upsert: same id updates in place (cover the ON CONFLICT path and
	// a non-nil valid_to roundtrip).
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
}

func TestCodeSymbols(t *testing.T) {
	s := openTestStore(t)
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	putTestMemory(t, s, m, nil)
	m2 := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	putTestMemory(t, s, m2, nil)

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
}

func TestRefsForSymbol(t *testing.T) {
	s := openTestStore(t)
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
}

func TestIndexState(t *testing.T) {
	s := openTestStore(t)

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
}

func TestWorkspacesAndMeta(t *testing.T) {
	s := openTestStore(t)

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
	putTestMemory(t, s, m1, nil)
	putTestMemory(t, s, m2, nil)
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
}

func TestStoreMisc(t *testing.T) {
	s := openTestStore(t)
	if s.UsingSQLiteVec() {
		t.Error("UsingSQLiteVec should always be false")
	}
	if s.DB() == nil {
		t.Error("DB() returned nil")
	}

	// RebuildVectorIndex resets the index at a new dim.
	putTestMemory(t, s, schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent),
		[]float32{1, 0, 0, 0, 0, 0, 0, 0})
	s.RebuildVectorIndex(4)
	if s.Dim != 4 {
		t.Errorf("Dim after rebuild = %d, want 4", s.Dim)
	}
	if s.VectorIndex().Len() != 0 {
		t.Errorf("index len after rebuild = %d, want 0", s.VectorIndex().Len())
	}
}

func TestWarmIndexFromBlobs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "warm.db")

	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	good := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	putTestMemory(t, s, good, []float32{1, 0, 0, 0, 0, 0, 0, 0})
	noVec := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	putTestMemory(t, s, noVec, nil)
	// A blob of the wrong length must be skipped on warm.
	bad := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if err := s.PutMemory(bad, []float32{1, 2}); err == nil {
		t.Fatal("expected dim-mismatch error")
	}
	// PutMemory failed before index upsert? It actually fails at index upsert,
	// after the SQL write — so the row exists with a 2-dim blob.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// Only the good 8-dim vector is warmed; the 2-dim blob is skipped.
	if s2.VectorIndex().Len() != 1 {
		t.Errorf("warmed index len = %d, want 1", s2.VectorIndex().Len())
	}
	hits := s2.VectorIndex().Search([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 1)
	if len(hits) != 1 || hits[0].ID != good.ID {
		t.Errorf("warm search hits = %v, want [%s]", hits, good.ID)
	}
}
