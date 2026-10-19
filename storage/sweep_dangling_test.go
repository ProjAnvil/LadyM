//go:build !enterprise

package storage

// SweepDanglingEdges removes valid edges whose endpoint memories no longer
// exist (legacy pre-FK databases; see neighbor_counts_join_test.go for why
// the public API cannot produce them). Runs against both backends.

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestSweepDanglingEdges(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		c := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		for _, m := range []*schema.Memory{a, b, c} {
			suitePutMemory(t, s, m, nil)
		}
		ab := schema.NewEdge(a.ID, "related_to", b.ID)
		ac := schema.NewEdge(a.ID, "supports", c.ID)
		if err := s.PutEdge(ab); err != nil {
			t.Fatal(err)
		}
		if err := s.PutEdge(ac); err != nil {
			t.Fatal(err)
		}

		// Dropping C without the cascade leaves the A-C edge dangling.
		deleteMemoryNoCascade(t, s, c.ID)

		n, err := s.SweepDanglingEdges()
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("SweepDanglingEdges = %d, want 1", n)
		}

		// A keeps only the A-B edge; it is still valid.
		edges, err := s.Neighbors(a.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 1 || edges[0].ID != ab.ID || edges[0].DstID != b.ID || edges[0].ValidTo != nil {
			t.Errorf("Neighbors(a) after sweep = %v, want only the valid A-B edge", edges)
		}

		// A second sweep is a no-op.
		n, err = s.SweepDanglingEdges()
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("second SweepDanglingEdges = %d, want 0", n)
		}
	})
}

// TestDeleteMemoryCascadesEdges is the regression lock for the dangling-edge
// defence: on a schema-complete database the FK cascade already removes the
// edges of a deleted memory through the public API, so dangling edges only
// arise in legacy pre-FK databases or from external writes (spec §9).
func TestDeleteMemoryCascadesEdges(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		suitePutMemory(t, s, a, nil)
		suitePutMemory(t, s, b, nil)
		if err := s.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
			t.Fatal(err)
		}

		if err := s.DeleteMemory(a.ID); err != nil {
			t.Fatal(err)
		}

		edges, err := s.Neighbors(b.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(edges) != 0 {
			t.Errorf("Neighbors(b) after DeleteMemory(a) = %v, want empty (FK cascade)", edges)
		}
		counts, err := s.NeighborCounts()
		if err != nil {
			t.Fatal(err)
		}
		if len(counts) != 0 {
			t.Errorf("NeighborCounts after DeleteMemory(a) = %v, want empty (FK cascade)", counts)
		}
	})
}
