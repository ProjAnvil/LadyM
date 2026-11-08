//go:build !enterprise

package engine

import (
	"errors"
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
	for _, name := range []string{"a/main.go", "b/main.go", "A/main.go", "main.go", " main.go", "main.go "} {
		ep, err := sc.RecordOperation(OperationEvent{Agent: "codex", Action: "read",
			Resources: []string{name}, Metadata: map[string]any{"resource_identity": "path"}})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = ep.ID
	}
	for name, id := range ids {
		t.Run(name, func(t *testing.T) {
			got, err := sc.RecallOperations(OperationsQuery{Resource: name})
			if err != nil || got.Fallback || got.Resource == nil || got.Resource.Content != name ||
				len(got.Items) != 1 || got.Items[0].Memory.ID != id {
				t.Fatalf("exact path recall %q: %+v %v", name, got, err)
			}
			res, err := sc.resolveResourceForRead(name)
			if err != nil || res == nil || res.Content != name {
				t.Fatalf("exact structural lookup %q: %+v %v", name, res, err)
			}
		})
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
	for _, name := range []string{" main.go ", "main.go  ", "  main.go"} {
		t.Run(name, func(t *testing.T) {
			if got, err := sc.resolveResourceForRead(name); err != nil || got != nil {
				t.Fatalf("unknown spaced path %q must not resolve to another path: %+v %v", name, got, err)
			}
			got, err := sc.RecallOperations(OperationsQuery{Resource: name})
			if err != nil || got.Resource != nil || !got.Fallback {
				t.Fatalf("unknown spaced path %q must use marked fallback: %+v %v", name, got, err)
			}
		})
	}
}

func TestCanonicalRecallPreservesLegacyWhitespace(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("legacy-whitespace")
	ep, err := sc.RecordOperation(OperationEvent{Agent: "codex", Action: "read", Resources: []string{"database"}})
	if err != nil {
		t.Fatal(err)
	}
	eng.Provider = &scopeErrEmbedder{EmbeddingProvider: eng.Provider}
	got, err := sc.RecallOperations(OperationsQuery{Resource: " \tDATABASE \n"})
	if err != nil || got.Fallback || got.Resource == nil || got.Resource.Content != "database" ||
		len(got.Items) != 1 || got.Items[0].Memory.ID != ep.ID {
		t.Fatalf("ordinary recall must still trim queries: %+v %v", got, err)
	}
	res, err := sc.resolveResourceForRead(" \tDATABASE \n")
	if err != nil || res == nil || res.Content != "database" {
		t.Fatalf("ordinary resolution must trim after path lookup: %+v %v", res, err)
	}
	for _, name := range []string{"", " ", "\t\n"} {
		if _, err := sc.RecallOperations(OperationsQuery{Resource: name}); !errors.Is(err, ErrResourceRequired) {
			t.Errorf("blank resource %q: %v, want ErrResourceRequired", name, err)
		}
	}
}
