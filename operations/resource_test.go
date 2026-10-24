//go:build !enterprise

package operations

import (
	"math"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func newResourceEnv(t *testing.T) (storage.Store, *storage.HashingEmbedding, *config.Config) {
	t.Helper()
	return newTestStore(t), storage.NewHashingEmbedding(16), config.Default()
}

// aliasesOf extracts the resource alias list from metadata, tolerating both
// the fresh []string form and the JSON round-trip []any form.
func aliasesOf(t *testing.T, m *schema.Memory) []string {
	t.Helper()
	switch v := m.Metadata["aliases"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				t.Fatalf("non-string alias %v", x)
			}
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("aliases metadata has unexpected type %T", v)
		return nil
	}
}

func TestResolveResourceCreatesNew(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	m, created, err := ResolveResource(store, emb, cfg, "t", "ladym.db")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false, want true on first resolve")
	}
	if m.Layer != schema.LayerSemantic {
		t.Errorf("layer = %q, want L2_semantic", m.Layer)
	}
	if m.Type != schema.TypeResource {
		t.Errorf("type = %q, want resource", m.Type)
	}
	if m.Workspace != "t" {
		t.Errorf("workspace = %q, want t", m.Workspace)
	}
	if m.Content != "ladym.db" {
		t.Errorf("content = %q, want ladym.db", m.Content)
	}
	if !slices.Contains(aliasesOf(t, m), "ladym.db") {
		t.Errorf("aliases = %v, want to contain ladym.db", m.Metadata["aliases"])
	}
	// Persisted: GetMemory round-trips.
	got, err := store.GetMemory(m.ID)
	if err != nil || got == nil {
		t.Fatalf("GetMemory after create: %v, %v", got, err)
	}
}

func TestResolveResourceExactAliasHit(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	m, _, err := ResolveResource(store, emb, cfg, "t", "ladym.db")
	if err != nil {
		t.Fatal(err)
	}
	// Same name, different case: exact alias match is case-insensitive.
	m2, created, err := ResolveResource(store, emb, cfg, "t", "LADYM.DB")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("created = true on alias hit, want false")
	}
	if m2.ID != m.ID {
		t.Errorf("alias hit id = %s, want %s", m2.ID, m.ID)
	}
}

func TestResolveResourceUnrelatedNameCreatesNew(t *testing.T) {
	store, emb, cfg := newResourceEnv(t)
	m, _, err := ResolveResource(store, emb, cfg, "t", "ladym.db")
	if err != nil {
		t.Fatal(err)
	}
	// Default 0.85 threshold: an unrelated name must not vector-match the
	// existing resource, so a new entity is created.
	m3, created, err := ResolveResource(store, emb, cfg, "t", "totally-unrelated-thing")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("created = false for unrelated name, want true")
	}
	if m3.ID == m.ID {
		t.Error("unrelated name resolved to existing resource id")
	}
}

func TestResolveResourceVectorBranch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite")
	store, err := storage.NewStore(path, 16, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	emb := storage.NewHashingEmbedding(16)
	cfg := config.Default()

	m, _, err := ResolveResource(store, emb, cfg, "t", "postgres primary db")
	if err != nil {
		t.Fatal(err)
	}

	// Hashing embeddings do not reach the default 0.85 threshold for these
	// near-neighbors, so lower the threshold to just under the real cosine
	// to exercise the vector branch itself (fallback sanctioned by the plan).
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

	m2, created, err := ResolveResource(store, emb, cfg, "t", "postgres primary database")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("created = true on vector hit, want false")
	}
	if m2.ID != m.ID {
		t.Errorf("vector hit id = %s, want %s", m2.ID, m.ID)
	}
	// Lazy alias growth: the queried name is appended.
	if !slices.Contains(aliasesOf(t, m2), "postgres primary database") {
		t.Errorf("aliases = %v, want to contain queried name", m2.Metadata["aliases"])
	}

	// Regression for the nil-vector hazard: the alias-append rewrite must
	// re-embed, so after reopening the store the resource is still
	// vector-searchable (a NULL embedding column would drop it).
	store.Close()
	store2, err := storage.NewStore(path, 16, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store2.Close() })
	found := false
	for _, h := range store2.VectorSearch(va, 10) {
		if h.ID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("resource not vector-searchable after reopen; embedding column was nulled by the alias rewrite")
	}
}
