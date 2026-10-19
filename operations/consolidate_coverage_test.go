//go:build !enterprise

package operations

// Coverage-gap tests for consolidate.go's projectProvenance (Task 22): the
// retired-edge skip, reversed-orientation tolerance, and the three store
// error branches. Store errors are injected with opsStubStore
// (maintain_coverage_test.go).

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// provenanceFixture persists a fact and an episode wired to two resources:
// r1 via a valid operates_on edge and r3 via a reversed edge
// (r3 -> operates_on -> episode).
func provenanceFixture(t *testing.T) (storage.Store, *storage.HashingEmbedding, *schema.Memory, *schema.Memory, *schema.Memory, *schema.Memory) {
	t.Helper()
	store, emb, _ := maintainTestStore(t)
	var fact, ep, r1, r3 *schema.Memory
	fact = putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "derived fact", nil)
	ep = putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=run", map[string]any{"action": "run"})
	r1 = putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeResource, "resource one", nil)
	r3 = putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeResource, "resource three", nil)

	valid := schema.NewEdge(ep.ID, "operates_on", r1.ID)
	valid.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", r1.ID)
	if err := store.PutEdge(valid); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(schema.NewEdge(r3.ID, "operates_on", ep.ID)); err != nil {
		t.Fatal(err)
	}
	return store, emb, fact, ep, r1, r3
}

func TestProjectProvenanceSkipsRetiredEdgesAndToleratesReversedOrientation(t *testing.T) {
	store, emb, fact, ep, r1, r3 := provenanceFixture(t)
	r2 := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeResource, "resource two", nil)
	// Neighbors only returns still-valid rows, so a retired edge cannot be
	// planted through the real store: inject one via the stub. A backend
	// that does surface retired rows must not get a projection from it.
	now := schema.Now()
	retired := schema.NewEdge(ep.ID, "operates_on", r2.ID)
	retired.ValidTo = &now
	inner := store
	stub := &opsStubStore{Store: store, neighbors: func(memID, relation string) ([]*schema.Edge, error) {
		edges, err := inner.Neighbors(memID, relation)
		if err != nil {
			return nil, err
		}
		if memID == ep.ID && relation == "operates_on" {
			edges = append(edges, retired)
		}
		return edges, nil
	}}

	if err := projectProvenance(stub, fact.ID, ep.ID); err != nil {
		t.Fatal(err)
	}

	// produced_from: exactly one deterministic edge fact -> episode.
	pf, err := store.Neighbors(fact.ID, "produced_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(pf) != 1 || pf[0].SrcID != fact.ID || pf[0].DstID != ep.ID ||
		pf[0].ID != schema.ProjectedEdgeID(fact.ID, "produced_from", ep.ID) {
		t.Fatalf("produced_from edges = %v, want one projected fact->episode edge", pf)
	}

	// operates_on: projected onto r1 (valid) and r3 (reversed orientation),
	// but never onto r2 whose only edge is retired.
	edges, err := store.Neighbors(fact.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range edges {
		if e.ValidTo != nil || e.SrcID != fact.ID {
			continue
		}
		got[e.DstID] = true
	}
	if !got[r1.ID] {
		t.Errorf("no valid operates_on projection onto r1 (%s): %v", r1.ID, got)
	}
	if !got[r3.ID] {
		t.Errorf("reversed edge r3->ep was not tolerated into a projection onto r3 (%s): %v", r3.ID, got)
	}
	if got[r2.ID] {
		t.Errorf("retired edge to r2 (%s) was projected anyway", r2.ID)
	}
}

func TestProjectProvenancePutEdgeErrorPropagates(t *testing.T) {
	store, _, fact, ep, _, _ := provenanceFixture(t)
	boom := errors.New("put edge boom")
	stub := &opsStubStore{Store: store, putEdge: func(*schema.Edge) error { return boom }}

	if err := projectProvenance(stub, fact.ID, ep.ID); !errors.Is(err, boom) {
		t.Fatalf("projectProvenance err = %v, want %v", err, boom)
	}
}

func TestProjectProvenanceNeighborsErrorPropagates(t *testing.T) {
	store, _, fact, ep, _, _ := provenanceFixture(t)
	boom := errors.New("neighbors boom")
	stub := &opsStubStore{Store: store, neighbors: func(string, string) ([]*schema.Edge, error) {
		return nil, boom
	}}

	if err := projectProvenance(stub, fact.ID, ep.ID); !errors.Is(err, boom) {
		t.Fatalf("projectProvenance err = %v, want %v", err, boom)
	}
}

func TestProjectProvenanceProjectedEdgePutErrorPropagates(t *testing.T) {
	store, _, fact, ep, _, _ := provenanceFixture(t)
	// The produced_from edge (dst = the episode) is written for real; every
	// subsequent projected operates_on edge fails.
	boom := errors.New("projected put boom")
	inner := store
	stub := &opsStubStore{Store: store, putEdge: func(e *schema.Edge) error {
		if e.DstID == ep.ID {
			return inner.PutEdge(e)
		}
		return boom
	}}

	if err := projectProvenance(stub, fact.ID, ep.ID); !errors.Is(err, boom) {
		t.Fatalf("projectProvenance err = %v, want %v", err, boom)
	}
}
