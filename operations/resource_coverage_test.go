//go:build !enterprise

package operations

// Coverage-gap tests for resource.go (Task 22): the direct alias-hit branch,
// resourceAliases' malformed-metadata tolerance, appendAlias's dedup, and
// ResolveResource's store/embedder error branches. Uses the opsStubStore and
// selectEmbedder from maintain_coverage_test.go.

import (
	"errors"
	"math"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// nearVectorEnv lowers ResolveThreshold to just under the real cosine between
// two names so the vector branch of ResolveResource is exercised (same
// fallback as TestResolveResourceVectorBranch).
func nearVectorEnv(t *testing.T) (storage.Store, *storage.HashingEmbedding, *config.Config) {
	t.Helper()
	store, emb, _ := maintainTestStore(t)
	cfg := config.Default()
	va, _ := emb.Embed("postgres primary db")
	vb, _ := emb.Embed("postgres primary database")
	var dot, na, nb float64
	for i := range va {
		dot += float64(va[i]) * float64(vb[i])
		na += float64(va[i]) * float64(va[i])
		nb += float64(vb[i]) * float64(vb[i])
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if sim <= 0.01 {
		t.Fatalf("cosine between test names too low: %f", sim)
	}
	cfg.Resource.ResolveThreshold = sim - 0.01
	return store, emb, cfg
}

func TestResolveResourceAliasListHit(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	res := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeResource,
		"customer database", map[string]any{"aliases": []string{"custdb"}})

	// "custdb" matches neither the content nor a case variant of it: only the
	// alias walk can resolve it.
	m, created, err := ResolveResource(store, emb, cfg, "test", "custdb")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("created = true on alias hit, want false")
	}
	if m.ID != res.ID {
		t.Errorf("alias hit id = %s, want %s", m.ID, res.ID)
	}
	// Alias matching is case-insensitive like content matching.
	m2, _, err := ResolveResource(store, emb, cfg, "test", "CUSTDB")
	if err != nil {
		t.Fatal(err)
	}
	if m2.ID != res.ID {
		t.Errorf("case-insensitive alias hit id = %s, want %s", m2.ID, res.ID)
	}
}

func TestResourceAliasesToleratesMalformedMetadata(t *testing.T) {
	// Non-list metadata (a corrupted/legacy value) yields nil, never a panic.
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	m.Metadata = map[string]any{"aliases": 42}
	if got := resourceAliases(m); got != nil {
		t.Errorf("resourceAliases(int) = %v, want nil", got)
	}
	// Missing metadata entirely.
	if got := resourceAliases(schema.NewMemory(schema.LayerSemantic, schema.TypeResource)); got != nil {
		t.Errorf("resourceAliases(no metadata) = %v, want nil", got)
	}
	// JSON round-trip form with non-string members keeps only the strings.
	m.Metadata["aliases"] = []any{"a", 1.5, "b"}
	got := resourceAliases(m)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("resourceAliases([]any) = %v, want [a b]", got)
	}
}

func TestAppendAliasDedupCaseInsensitive(t *testing.T) {
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	m.Metadata = map[string]any{"aliases": []string{"custdb"}}
	if appendAlias(m, "CUSTDB") {
		t.Error("appendAlias(different case) = true, want false (dedup)")
	}
	if appendAlias(m, "customers db") != true {
		t.Error("appendAlias(new name) = false, want true")
	}
	got := resourceAliases(m)
	if len(got) != 2 || got[1] != "customers db" {
		t.Errorf("aliases after append = %v, want [custdb customers db]", got)
	}
}

func TestResolveResourceIterMemoriesErrorPropagates(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	boom := errors.New("iter boom")
	stub := &opsStubStore{Store: store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
		return nil, boom
	}}

	if _, _, err := ResolveResource(stub, emb, cfg, "t", "ladym.db"); !errors.Is(err, boom) {
		t.Fatalf("ResolveResource err = %v, want %v", err, boom)
	}
}

func TestResolveResourceEmbedErrorPropagates(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	failEmb := &selectEmbedder{EmbeddingProvider: emb, failContent: "ladym.db"}

	if _, _, err := ResolveResource(store, failEmb, cfg, "t", "ladym.db"); err == nil {
		t.Fatal("ResolveResource with failing embedder: want error, got nil")
	}
}

func TestResolveResourceGetMemoryErrorPropagates(t *testing.T) {
	store, emb, cfg := nearVectorEnv(t)
	if _, _, err := ResolveResource(store, emb, cfg, "test", "postgres primary db"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("get boom")
	stub := &opsStubStore{Store: store, getMemory: func(string) (*schema.Memory, error) {
		return nil, boom
	}}

	if _, _, err := ResolveResource(stub, emb, cfg, "test", "postgres primary database"); !errors.Is(err, boom) {
		t.Fatalf("ResolveResource err = %v, want %v", err, boom)
	}
}

func TestResolveResourceVectorHitOnForeignWorkspaceCreatesNew(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	// A resource in another workspace: stage 1 is workspace-scoped so it
	// misses, but the global vector search hits it — the type/workspace
	// filter must skip it and a fresh entity be created here instead.
	foreign := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	foreign.Workspace = "other"
	foreign.Content = "alpha beta gamma"
	foreign.Summary = "alpha beta gamma"
	vec, err := emb.Embed(foreign.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(foreign, vec); err != nil {
		t.Fatal(err)
	}

	m, created, err := ResolveResource(store, emb, cfg, "test", "alpha beta gamma")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false, want true (foreign-workspace hit must not leak)")
	}
	if m.ID == foreign.ID || m.Workspace != "test" {
		t.Errorf("resolved %+v, want a fresh entity in workspace test", m)
	}
	if got, _ := store.GetMemory(foreign.ID); got == nil {
		t.Error("the foreign resource was clobbered")
	}
}

func TestResolveResourceAliasAppendRewriteEmbedErrorPropagates(t *testing.T) {
	store, emb, cfg := nearVectorEnv(t)
	res, _, err := ResolveResource(store, emb, cfg, "test", "postgres primary db")
	if err != nil {
		t.Fatal(err)
	}
	// The query embeds fine; the alias-append rewrite of the winner content fails.
	failEmb := &selectEmbedder{EmbeddingProvider: emb, failContent: res.Content}

	if _, _, err := ResolveResource(store, failEmb, cfg, "test", "postgres primary database"); err == nil {
		t.Fatal("ResolveResource alias-append rewrite with failing embedder: want error, got nil")
	}
}

func TestResolveResourceAliasAppendRewritePutErrorPropagates(t *testing.T) {
	store, emb, cfg := nearVectorEnv(t)
	if _, _, err := ResolveResource(store, emb, cfg, "test", "postgres primary db"); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("put boom")
	stub := &opsStubStore{Store: store, putMemory: func(*schema.Memory, []float32) error {
		return boom
	}}

	if _, _, err := ResolveResource(stub, emb, cfg, "test", "postgres primary database"); !errors.Is(err, boom) {
		t.Fatalf("ResolveResource err = %v, want %v", err, boom)
	}
}

func TestResolveResourceCreatePutErrorPropagates(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	boom := errors.New("create boom")
	stub := &opsStubStore{Store: store, putMemory: func(*schema.Memory, []float32) error {
		return boom
	}}

	if _, _, err := ResolveResource(stub, emb, cfg, "t", "brand-new-thing"); !errors.Is(err, boom) {
		t.Fatalf("ResolveResource err = %v, want %v", err, boom)
	}
}
