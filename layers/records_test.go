//go:build !enterprise

package layers

import (
	"errors"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// failingEmbedder has a fixed dim but fails every Embed call.
type failingEmbedder struct {
	dim int
	err error
}

func (f *failingEmbedder) Dim() int { return f.dim }

func (f *failingEmbedder) Embed(string) ([]float32, error) { return nil, f.err }

func (f *failingEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for range texts {
		out = append(out, nil)
	}
	return nil, f.err
}

func (f *failingEmbedder) HealthCheck() (bool, string) { return false, f.err.Error() }

func newTestEmbedder() storage.EmbeddingProvider { return storage.NewHashingEmbedding(16) }

// ---- constructors: empty workspace defaults to "default" ----

func TestConstructorsDefaultWorkspace(t *testing.T) {
	store := newTestStore(t)
	emb := newTestEmbedder()

	if got := NewEpisodicMemory(store, emb, "").Workspace; got != "default" {
		t.Errorf("episodic workspace = %q, want default", got)
	}
	if got := NewSemanticMemory(store, emb, "").Workspace; got != "default" {
		t.Errorf("semantic workspace = %q, want default", got)
	}
	if got := NewProceduralMemory(store, emb, "").Workspace; got != "default" {
		t.Errorf("procedural workspace = %q, want default", got)
	}
	w := NewWorkingMemory(0, "")
	if got := w.Push("x", nil, nil, "s").Workspace; got != "default" {
		t.Errorf("working workspace = %q, want default", got)
	}
	// capacity <= 0 defaults to 64: push 70 items, expect only the last 64 kept.
	for i := 0; i < 70; i++ {
		w.Push(string(rune('a'+i%26)), nil, nil, "s")
	}
	if got := w.Len(); got != 64 {
		t.Errorf("default-capacity Len = %d, want 64", got)
	}
}

// ---- working memory ----

func TestWorkingMemoryBuffer(t *testing.T) {
	w := NewWorkingMemory(2, "ws")
	w.Push("first", nil, nil, "src")
	w.Push("second", nil, nil, "src")
	w.Push("third", nil, nil, "src") // overflows: "first" dropped

	if got := w.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 after overflow", got)
	}
	items := w.Items()
	if len(items) != 2 || items[0].Content != "second" || items[1].Content != "third" {
		t.Fatalf("Items contents = %v, want [second third]", items)
	}

	// Items returns a snapshot: mutating the returned slice must not affect the buffer.
	items[0] = nil
	if got := w.Items()[0].Content; got != "second" {
		t.Errorf("Items snapshot mutation leaked into buffer, got %q", got)
	}

	drained := w.Drain()
	if len(drained) != 2 {
		t.Errorf("Drain returned %d items, want 2", len(drained))
	}
	if got := w.Len(); got != 0 {
		t.Errorf("Len after Drain = %d, want 0", got)
	}

	w.Push("x", nil, nil, "src")
	w.Clear()
	if got := w.Len(); got != 0 {
		t.Errorf("Len after Clear = %d, want 0", got)
	}
}

// truncate must cut by runes, not bytes.
func TestWorkingPushTruncatesLongSummary(t *testing.T) {
	w := NewWorkingMemory(10, "ws")
	long := strings.Repeat("界", 100)
	m := w.Push(long, nil, nil, "src")
	if got := []rune(m.Summary); len(got) != 80 {
		t.Errorf("Summary length = %d runes, want 80", len(got))
	}
	if m.Content != long {
		t.Error("Content should not be truncated")
	}
	short := w.Push("short", nil, nil, "src")
	if short.Summary != "short" {
		t.Errorf("Summary = %q, want short", short.Summary)
	}
}

// ---- episodic memory ----

func TestEpisodicRecordContentAndMeta(t *testing.T) {
	e := newTestEpisodic(t)
	m, err := e.Record("agent", "act", "obs", "out", []string{"t1"}, map[string]any{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	want := "agent=agent | action=act | observation=obs | outcome=out"
	if m.Content != want {
		t.Errorf("Content = %q, want %q", m.Content, want)
	}
	if m.Summary != "agent: act" {
		t.Errorf("Summary = %q, want %q", m.Summary, "agent: act")
	}
	if got := m.MetaString("observation"); got != "obs" {
		t.Errorf("meta observation = %q, want obs", got)
	}
	if got := m.MetaString("outcome"); got != "out" {
		t.Errorf("meta outcome = %q, want out", got)
	}
	if m.Metadata["k"] != "v" {
		t.Errorf("meta k = %v, want v", m.Metadata["k"])
	}
	if m.Source != "agent" || m.Workspace != "test" {
		t.Errorf("Source/Workspace = %q/%q, want agent/test", m.Source, m.Workspace)
	}
	if len(m.Tags) != 1 || m.Tags[0] != "t1" {
		t.Errorf("Tags = %v, want [t1]", m.Tags)
	}

	// No observation/outcome: parts omitted, tags normalised to [].
	m2, err := e.Record("agent", "act", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Content != "agent=agent | action=act" {
		t.Errorf("Content = %q, want parts without observation/outcome", m2.Content)
	}
	if m2.Tags == nil {
		t.Error("Tags should be [] after Record with nil tags, got nil")
	}
}

func TestEpisodicRecordEmbedderFailure(t *testing.T) {
	boom := errors.New("embed boom")
	e := NewEpisodicMemory(newTestStore(t), &failingEmbedder{dim: 16, err: boom}, "test")
	if _, err := e.Record("agent", "act", "", "", nil, nil); !errors.Is(err, boom) {
		t.Errorf("Record err = %v, want embed boom", err)
	}
}

func TestEpisodicRecordStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	e := NewEpisodicMemory(store, newTestEmbedder(), "test")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := e.Record("agent", "act", "", "", nil, nil); err == nil {
		t.Error("Record should fail when the memories table is gone")
	}
}

func TestEpisodicRecentStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	e := NewEpisodicMemory(store, newTestEmbedder(), "test")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := e.Recent(5); err == nil {
		t.Error("Recent should fail when the memories table is gone")
	}
}

// ---- semantic memory ----

func TestSemanticPutFact(t *testing.T) {
	s := NewSemanticMemory(newTestStore(t), newTestEmbedder(), "ws")

	// Explicit summary wins; long content without summary is truncated to 80 runes.
	m, err := s.PutFact("content", "sum", []string{"a"}, map[string]any{"k": "v"}, "src")
	if err != nil {
		t.Fatal(err)
	}
	if m.Summary != "sum" || m.Source != "src" || m.Workspace != "ws" {
		t.Errorf("Summary/Source/Workspace = %q/%q/%q", m.Summary, m.Source, m.Workspace)
	}
	if m.ContentHash != schema.ContentHash("content") {
		t.Error("ContentHash mismatch")
	}

	long := strings.Repeat("x", 100)
	m2, err := s.PutFact(long, "", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(m2.Summary)) != 80 {
		t.Errorf("default Summary length = %d runes, want 80", len([]rune(m2.Summary)))
	}
	if m2.Tags == nil || m2.Metadata == nil {
		t.Error("nil tags/metadata should be normalised to empty")
	}

	// FindByHash round-trips the fact; unknown hash returns nil, nil.
	got, err := s.FindByHash(m2.ContentHash)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != m2.ID {
		t.Errorf("FindByHash returned %v, want the stored fact", got)
	}
	miss, err := s.FindByHash("no-such-hash")
	if err != nil || miss != nil {
		t.Errorf("FindByHash(unknown) = %v, %v; want nil, nil", miss, err)
	}
}

func TestSemanticPutFactStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	s := NewSemanticMemory(store, newTestEmbedder(), "ws")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := s.PutFact("content", "", nil, nil, ""); err == nil {
		t.Error("PutFact should fail when the memories table is gone")
	}
}

func TestSemanticPutFactEmbedderFailure(t *testing.T) {
	boom := errors.New("embed boom")
	s := NewSemanticMemory(newTestStore(t), &failingEmbedder{dim: 16, err: boom}, "ws")
	if _, err := s.PutFact("content", "", nil, nil, ""); !errors.Is(err, boom) {
		t.Errorf("PutFact err = %v, want embed boom", err)
	}
}

func TestSemanticPutCodeFile(t *testing.T) {
	s := NewSemanticMemory(newTestStore(t), newTestEmbedder(), "ws")

	m, err := s.PutCodeFile("pkg/foo.go", "does foo things", "go")
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "pkg/foo.go: does foo things" {
		t.Errorf("Content = %q", m.Content)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "code" || m.Tags[1] != "go" {
		t.Errorf("Tags = %v, want [code go]", m.Tags)
	}
	if got := m.MetaString("file_path"); got != "pkg/foo.go" {
		t.Errorf("meta file_path = %q", got)
	}
	if m.Source != "pkg/foo.go" {
		t.Errorf("Source = %q, want file path", m.Source)
	}

	// No language: only the "code" tag.
	m2, err := s.PutCodeFile("readme.md", "docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Tags) != 1 || m2.Tags[0] != "code" {
		t.Errorf("Tags = %v, want [code]", m2.Tags)
	}

	// Long summaries are truncated to 120 runes.
	m3, err := s.PutCodeFile("big.go", strings.Repeat("y", 200), "go")
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(m3.Summary)) != 120 {
		t.Errorf("Summary length = %d runes, want 120", len([]rune(m3.Summary)))
	}
}

func TestSemanticPutCodeFileStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	s := NewSemanticMemory(store, newTestEmbedder(), "ws")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := s.PutCodeFile("f.go", "s", "go"); err == nil {
		t.Error("PutCodeFile should fail when the memories table is gone")
	}
}

func TestSemanticPutCodeFileEmbedderFailure(t *testing.T) {
	boom := errors.New("embed boom")
	s := NewSemanticMemory(newTestStore(t), &failingEmbedder{dim: 16, err: boom}, "ws")
	if _, err := s.PutCodeFile("f.go", "s", "go"); !errors.Is(err, boom) {
		t.Errorf("PutCodeFile err = %v, want embed boom", err)
	}
}

// ---- procedural memory ----

func TestProceduralPutPlaybook(t *testing.T) {
	p := NewProceduralMemory(newTestStore(t), newTestEmbedder(), "ws")
	m, err := p.PutPlaybook("deploy", []string{"build", "ship"},
		[]string{"clean tree"}, "app live", []string{"ops"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "deploy\n1. build\n2. ship" {
		t.Errorf("Content = %q", m.Content)
	}
	if m.Summary != "deploy" || m.Source != "proceduralize" || m.Workspace != "ws" {
		t.Errorf("Summary/Source/Workspace = %q/%q/%q", m.Summary, m.Source, m.Workspace)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "ops" || m.Tags[1] != "playbook" {
		t.Errorf("Tags = %v, want [ops playbook]", m.Tags)
	}
	if m.Metadata["expected_outcome"] != "app live" {
		t.Errorf("meta expected_outcome = %v", m.Metadata["expected_outcome"])
	}
	if m.ContentHash != schema.ContentHash(m.Content) {
		t.Error("ContentHash mismatch")
	}

	// nil preconditions/tags are normalised to empty slices.
	m2, err := p.PutPlaybook("pb", nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Tags == nil || len(m2.Tags) != 1 || m2.Tags[0] != "playbook" {
		t.Errorf("Tags = %v, want [playbook]", m2.Tags)
	}
	pre, ok := m2.Metadata["preconditions"].([]string)
	if !ok || pre == nil || len(pre) != 0 {
		t.Errorf("meta preconditions = %v, want empty []string", m2.Metadata["preconditions"])
	}
}

func TestProceduralPutPlaybookStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	p := NewProceduralMemory(store, newTestEmbedder(), "ws")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := p.PutPlaybook("pb", nil, nil, "", nil); err == nil {
		t.Error("PutPlaybook should fail when the memories table is gone")
	}
}

func TestProceduralPutPlaybookEmbedderFailure(t *testing.T) {
	boom := errors.New("embed boom")
	p := NewProceduralMemory(newTestStore(t), &failingEmbedder{dim: 16, err: boom}, "ws")
	if _, err := p.PutPlaybook("pb", nil, nil, "", nil); !errors.Is(err, boom) {
		t.Errorf("PutPlaybook err = %v, want embed boom", err)
	}
}

func TestProceduralPutSnippet(t *testing.T) {
	p := NewProceduralMemory(newTestStore(t), newTestEmbedder(), "ws")
	m, err := p.PutSnippet("hello", "print('hi')", "", []string{"demo"})
	if err != nil {
		t.Fatal(err)
	}
	want := "hello\n```python\nprint('hi')\n```"
	if m.Content != want {
		t.Errorf("Content = %q, want %q", m.Content, want)
	}
	// default language python: tags = [demo, snippet, python]
	if len(m.Tags) != 3 || m.Tags[1] != "snippet" || m.Tags[2] != "python" {
		t.Errorf("Tags = %v, want [demo snippet python]", m.Tags)
	}
	if got := m.MetaString("language"); got != "python" {
		t.Errorf("meta language = %q, want python", got)
	}

	m2, err := p.PutSnippet("hi", "echo hi", "bash", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.Tags) != 2 || m2.Tags[0] != "snippet" || m2.Tags[1] != "bash" {
		t.Errorf("Tags = %v, want [snippet bash]", m2.Tags)
	}
}

func TestProceduralPutSnippetStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	p := NewProceduralMemory(store, newTestEmbedder(), "ws")
	backdoorExec(t, dbPath, "DROP TABLE memories")
	if _, err := p.PutSnippet("t", "c", "", nil); err == nil {
		t.Error("PutSnippet should fail when the memories table is gone")
	}
}

func TestProceduralPutSnippetEmbedderFailure(t *testing.T) {
	boom := errors.New("embed boom")
	p := NewProceduralMemory(newTestStore(t), &failingEmbedder{dim: 16, err: boom}, "ws")
	if _, err := p.PutSnippet("t", "c", "", nil); !errors.Is(err, boom) {
		t.Errorf("PutSnippet err = %v, want embed boom", err)
	}
}

// ---- associative memory ----

func TestAssociativeGraph(t *testing.T) {
	store := newTestStore(t)
	a := NewAssociativeMemory(store)
	putMem := func(id string) {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID = id
		m.Content = "mem " + id
		if err := store.PutMemory(m, nil); err != nil {
			t.Fatal(err)
		}
	}
	putMem("s")
	putMem("d")
	putMem("x")

	// Empty relation defaults to related_to; validFrom defaults to now.
	e1, err := a.Link("s", "d", "", nil, map[string]any{"k": "v"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Relation != "related_to" {
		t.Errorf("Relation = %q, want related_to", e1.Relation)
	}
	if e1.ValidFrom == 0 {
		t.Error("ValidFrom should default to now")
	}

	if _, err := a.Link("s", "x", "supports", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Neighbors without relation filter returns both edges of "s".
	nb, err := a.Neighbors("s", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 2 {
		t.Fatalf("Neighbors(s) = %d edges, want 2", len(nb))
	}
	// With a relation filter only the matching edge comes back.
	nb, err = a.Neighbors("s", "supports")
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 1 || nb[0].DstID != "x" {
		t.Errorf("Neighbors(s, supports) = %v, want the s->x edge", nb)
	}

	counts, err := a.NeighborCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["s"] != 2 || counts["d"] != 1 || counts["x"] != 1 {
		t.Errorf("NeighborCounts = %v, want s:2 d:1 x:1", counts)
	}

	// Retire with an explicit timestamp hides the edge from Neighbors/Counts.
	when := schema.Now()
	if err := a.Retire(e1.ID, &when); err != nil {
		t.Fatal(err)
	}
	nb, err = a.Neighbors("s", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 1 {
		t.Errorf("Neighbors(s) after Retire = %d edges, want 1", len(nb))
	}
	counts, err = a.NeighborCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["s"] != 1 {
		t.Errorf("NeighborCounts[s] after Retire = %d, want 1", counts["s"])
	}

	// Retire with nil timestamp uses now.
	e2, err := a.Link("d", "x", "", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Retire(e2.ID, nil); err != nil {
		t.Fatal(err)
	}
	nb, err = a.Neighbors("d", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nb) != 0 {
		t.Errorf("Neighbors(d) after retiring all edges = %d, want 0", len(nb))
	}
}

func TestAssociativeLinkStoreFailure(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	a := NewAssociativeMemory(store)
	backdoorExec(t, dbPath, "DROP TABLE edges")
	if _, err := a.Link("s", "d", "", nil, nil, nil, nil); err == nil {
		t.Error("Link should fail when the edges table is gone")
	}
	if err := a.Retire("edge-id", nil); err == nil {
		t.Error("Retire should fail when the edges table is gone")
	}
}
