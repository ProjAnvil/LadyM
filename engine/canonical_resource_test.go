//go:build !enterprise

package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
)

type canonicalEmbedding struct{ storage.EmbeddingProvider }

func (e canonicalEmbedding) Embed(string) ([]float32, error) {
	v := make([]float32, e.Dim())
	v[0] = 1
	return v, nil
}

func TestCanonicalResourceNoFuzzyMergeRecall(t *testing.T) {
	eng := newTestEngine(t)
	eng.Provider = canonicalEmbedding{eng.Provider}
	sc := eng.Scope("canonical")
	ids := map[string]string{}
	for _, name := range []string{"a/main.go", "b/main.go", "A/main.go"} {
		ep, err := sc.RecordOperation(OperationEvent{Agent: "codex", Action: "read",
			Resources: []string{name}, Metadata: map[string]any{"resource_identity": "path"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = ep.ID
	}
	for name, id := range ids {
		got, err := sc.RecallOperations(OperationsQuery{Resource: name})
		if err != nil || got.Fallback || len(got.Items) != 1 || got.Items[0].Memory.ID != id {
			t.Fatalf("exact path recall %q: %+v %v", name, got, err)
		}
	}
	// Even poisoned aliases and identical vectors must not resolve an unknown path.
	res, err := sc.resolveResourceForRead("a/main.go")
	if err != nil {
		t.Fatal(err)
	}
	res.Metadata["aliases"] = []string{"unknown/main.go"}
	vec, _ := eng.Provider.Embed(res.Content)
	if err := eng.Store.PutMemory(res, vec); err != nil {
		t.Fatal(err)
	}
	if got, err := sc.resolveResourceForRead("unknown/main.go"); err != nil || got != nil {
		t.Fatalf("unknown path must not fuzzy/alias resolve: %+v %v", got, err)
	}
}
