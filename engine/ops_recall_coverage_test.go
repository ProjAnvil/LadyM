//go:build !enterprise

package engine

// Coverage-gap tests for ops_recall.go (Task 22): resolveResourceForRead's
// alias-hit, wrong-type vector filter, and store/embedder error branches, plus
// memoryAliases' JSON round-trip tolerance. Store errors are injected with a
// stub wrapping the real store (same pattern as store_error_branches_test.go).

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// recallStubStore delegates everything to the wrapped store except the
// methods a test has overridden with a hook.
type recallStubStore struct {
	storage.Store
	iterMemories func(workspace, layer, typ string) ([]*schema.Memory, error)
	getMemory    func(id string) (*schema.Memory, error)
}

func (s *recallStubStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	if s.iterMemories != nil {
		return s.iterMemories(workspace, layer, typ)
	}
	return s.Store.IterMemories(workspace, layer, typ)
}

func (s *recallStubStore) GetMemory(id string) (*schema.Memory, error) {
	if s.getMemory != nil {
		return s.getMemory(id)
	}
	return s.Store.GetMemory(id)
}

// scopeErrEmbedder fails every Embed call; the rest of the provider contract
// is delegated.
type scopeErrEmbedder struct {
	storage.EmbeddingProvider
}

func (e *scopeErrEmbedder) Embed(string) ([]float32, error) {
	return nil, errors.New("embed boom")
}

// putScopedResource persists a resource memory with content and aliases in
// the scope's workspace and returns it.
func putScopedResource(t *testing.T, sc *Scope, content string, aliases []string) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	m.Workspace = sc.workspace
	m.Content = content
	m.Summary = content
	m.Metadata = map[string]any{"aliases": aliases}
	vec, err := sc.eng.Provider.Embed(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.eng.Store.PutMemory(m, vec); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolveResourceForReadAliasHit(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	res := putScopedResource(t, sc, "customer database", []string{"custdb"})

	// "custdb" matches neither the content nor a case variant of it: only the
	// read-side alias walk can resolve it.
	got, err := sc.resolveResourceForRead("custdb")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != res.ID {
		t.Fatalf("resolveResourceForRead(custdb) = %+v, want %s", got, res.ID)
	}
	// Case-insensitive alias matching.
	got, err = sc.resolveResourceForRead("CUSTDB")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != res.ID {
		t.Fatalf("resolveResourceForRead(CUSTDB) = %+v, want %s", got, res.ID)
	}
	// The read path must not grow the alias list (write-path behaviour).
	if aliases := memoryAliases(got); len(aliases) != 1 {
		t.Errorf("aliases = %v, want unchanged [custdb] (read path never grows aliases)", aliases)
	}
}

func TestResolveResourceForReadVectorHitOnNonResourceSkips(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	// An episodic memory whose content the query equals: the vector search
	// hits it with cos 1.0, but the type filter must skip it (and no resource
	// exists, so resolution degrades to a miss).
	ep := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	ep.Workspace = sc.workspace
	ep.Content = "alpha beta gamma"
	vec, err := eng.Provider.Embed(ep.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Store.PutMemory(ep, vec); err != nil {
		t.Fatal(err)
	}

	got, err := sc.resolveResourceForRead("alpha beta gamma")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("resolveResourceForRead resolved non-resource %q, want nil", got.Content)
	}
}

func TestResolveResourceForReadIterMemoriesErrorPropagates(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	boom := errors.New("iter boom")
	eng.Store = &recallStubStore{Store: eng.Store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
		return nil, boom
	}}

	if _, err := sc.resolveResourceForRead("ladym.db"); !errors.Is(err, boom) {
		t.Fatalf("resolveResourceForRead err = %v, want %v", err, boom)
	}
}

func TestResolveResourceForReadEmbedErrorPropagates(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	eng.Provider = &scopeErrEmbedder{EmbeddingProvider: eng.Provider}

	if _, err := sc.resolveResourceForRead("ladym.db"); err == nil {
		t.Fatal("resolveResourceForRead with failing embedder: want error, got nil")
	}
}

func TestResolveResourceForReadGetMemoryErrorPropagates(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	res := putScopedResource(t, sc, "postgres primary db", nil)
	// Lower the read threshold below the real cosine so the vector branch
	// hits, then fail the GetMemory lookup (same threshold trick as the
	// existing alias-resolution test).
	va, _ := eng.Provider.Embed("postgres primary db")
	vb, _ := eng.Provider.Embed("postgres primary database")
	cos := storage.CosineSimilarity(va, vb)
	if cos <= 0.01 {
		t.Fatalf("cosine between test names too low: %f", cos)
	}
	eng.Config.Resource.ReadThreshold = cos - 0.01
	boom := errors.New("get boom")
	eng.Store = &recallStubStore{Store: eng.Store, getMemory: func(string) (*schema.Memory, error) {
		return nil, boom
	}}

	if _, err := sc.resolveResourceForRead("postgres primary database"); !errors.Is(err, boom) {
		t.Fatalf("resolveResourceForRead err = %v, want %v", err, boom)
	}
	_ = res
}

func TestMemoryAliasesToleratesJSONRoundTrip(t *testing.T) {
	// Fresh []string form passes through unchanged.
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	m.Metadata = map[string]any{"aliases": []string{"a", "b"}}
	if got := memoryAliases(m); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("memoryAliases([]string) = %v, want [a b]", got)
	}
	// JSON round-trip form: non-string members are dropped.
	m.Metadata["aliases"] = []any{"x", 42, "y"}
	if got := memoryAliases(m); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("memoryAliases([]any) = %v, want [x y]", got)
	}
	// Missing or malformed alias metadata yields nil, never a panic.
	delete(m.Metadata, "aliases")
	if got := memoryAliases(m); got != nil {
		t.Errorf("memoryAliases(no aliases) = %v, want nil", got)
	}
	m.Metadata["aliases"] = "bogus"
	if got := memoryAliases(m); got != nil {
		t.Errorf("memoryAliases(string) = %v, want nil", got)
	}
}
