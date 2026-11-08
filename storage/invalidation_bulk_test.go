package storage

import (
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestBulkIndexPreservesAllDeletedSources(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		child := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		suitePutMemory(t, s, child, nil)
		var ids []string
		for range 2 {
			source := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
			source.Source = "shared.go"
			suitePutMemory(t, s, source, nil)
			ids = append(ids, source.ID)
			if err := s.PutEdge(schema.NewEdge(child.ID, "produced_from", source.ID)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteMemoriesByTypeSource(string(schema.TypeCodeSymbol), "shared.go", child.Workspace); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetMemory(child.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if !slices.Contains(legacySources(got), id) {
				t.Fatal("bulk deletion lost one of the shared source IDs")
			}
		}
	})
}
