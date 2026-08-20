//go:build !enterprise

package engine

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// Fix 1: Engine.Link must pass weight/metadata/valid_from/valid_to through to
// the associative layer (Python link(src, dst, relation, **kw)).
func TestLinkOptions(t *testing.T) {
	eng := newTestEngine(t)
	mk := func(content string) string {
		m, err := eng.Remember(content, schema.LayerSemantic, schema.TypeFact, nil, nil, "test", "")
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	a, b, c := mk("node a"), mk("node b"), mk("node c")

	// no opts → default weight 1.0
	e0, err := eng.Link(a, b, "related_to")
	if err != nil {
		t.Fatal(err)
	}
	if e0.Weight != 1.0 {
		t.Errorf("default weight = %v, want 1.0", e0.Weight)
	}

	// explicit zero weight must survive to the DB (not be coerced to 1.0)
	e1, err := eng.Link(a, b, "related_to", WithWeight(0))
	if err != nil {
		t.Fatal(err)
	}
	if e1.Weight != 0 {
		t.Errorf("explicit WithWeight(0) returned weight %v, want 0", e1.Weight)
	}
	edges, err := eng.Associative.Neighbors(a, "related_to")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ed := range edges {
		if ed.ID == e1.ID {
			found = true
			if ed.Weight != 0 {
				t.Errorf("persisted weight = %v, want 0", ed.Weight)
			}
		}
	}
	if !found {
		t.Fatal("zero-weight edge not found via Neighbors")
	}

	// metadata / valid_from / valid_to passthrough
	e2, err := eng.Link(a, c, "supports",
		WithWeight(2.5),
		WithMetadata(map[string]any{"k": "v"}),
		WithValidFrom(100),
		WithValidTo(200),
	)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Weight != 2.5 {
		t.Errorf("weight = %v, want 2.5", e2.Weight)
	}
	if e2.Metadata["k"] != "v" {
		t.Errorf("metadata = %v, want k=v", e2.Metadata)
	}
	if e2.ValidFrom != 100 {
		t.Errorf("valid_from = %v, want 100", e2.ValidFrom)
	}
	if e2.ValidTo == nil || *e2.ValidTo != 200 {
		t.Errorf("valid_to = %v, want 200", e2.ValidTo)
	}
}

// Fix 2: a failing LLM classifier must abort Consolidate with an error
// (Python: the exception propagates out of consolidate).
func TestConsolidateClassifierErrorPropagates(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.RecordEvent("claude", "did a thing", "obs", "success", nil, nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("classifier boom")
	err := eng.AttachLLMClassifier(func(candidate string, similar []string) (operations.Action, string, error) {
		return "", "", boom
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Consolidate("", 0); !errors.Is(err, boom) {
		t.Fatalf("Consolidate err = %v, want wrapped %v", err, boom)
	}
}
