//go:build !enterprise

package operations

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type canonicalEmbedding struct{ storage.EmbeddingProvider }

func (e canonicalEmbedding) Embed(string) ([]float32, error) {
	v := make([]float32, e.Dim())
	v[0] = 1
	return v, nil
}

func (e canonicalEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i], _ = e.Embed(texts[i])
	}
	return out, nil
}

func TestCanonicalResourceNoFuzzyMerge(t *testing.T) {
	store, provider, cfg := newResourceEnv(t)
	emb := canonicalEmbedding{provider}
	a, err := ResolvePathResource(store, emb, "t", "a/main.go")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResolvePathResource(store, emb, "t", "b/main.go")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("path:%x", sha256.Sum256([]byte("t\x00a/main.go")))
	if a.ID != want || a.ID == b.ID || a.MetaString("resource_identity") != "path" {
		t.Fatalf("path identities: %+v %+v", a, b)
	}
	// Inject equal vectors to prove the exclusions even for imported path rows.
	for _, m := range []*schema.Memory{a, b} {
		vec, err := emb.Embed(m.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
	}
	again, err := ResolvePathResource(store, emb, "t", "a/main.go")
	if err != nil || again.ID != a.ID {
		t.Fatalf("repeat exact identity: %+v %v", again, err)
	}
	upper, err := ResolvePathResource(store, emb, "t", "A/main.go")
	if err != nil || upper.ID == a.ID {
		t.Fatalf("case-sensitive identity: %+v %v", upper, err)
	}
	foreign, err := ResolvePathResource(store, emb, "other", "a/main.go")
	if err != nil || foreign.ID == a.ID {
		t.Fatalf("workspace identity: %+v %v", foreign, err)
	}
	for _, name := range []string{"a/main.go", "MAIN.GO"} {
		ordinary, created, err := ResolveResource(store, emb, cfg, "t", name)
		if err != nil || !created || ordinary.ID == a.ID || ordinary.ID == b.ID {
			t.Fatalf("ordinary resource must not alias paths: %+v %v %v", ordinary, created, err)
		}
		// Remove ordinary entities so both exact and vector exclusions are exercised.
		if err := store.DeleteMemory(ordinary.ID); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	yes := &providers.FakeLLMProvider{StructuredFn: func([]providers.Message, providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"same_entity": true}, nil
	}}
	report, err := MaintainL4(store, emb, cfg, "t", &MaintainAgents{AliasMerge: yes})
	if err != nil || report.Merged != 0 || calls != 0 {
		t.Fatalf("path worker merge: %+v calls=%d err=%v", report, calls, err)
	}
	for _, id := range []string{a.ID, b.ID, upper.ID} {
		got, err := store.GetMemory(id)
		if err != nil || !schema.IsUsable(got) {
			t.Fatalf("path lost: %+v %v", got, err)
		}
	}
}
