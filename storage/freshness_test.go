package storage

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestEvidenceFreshnessManagedSourceRevision(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := schema.NewMemory(schema.LayerSemantic, schema.TypeTopicView)
		source.Metadata["topic_revision"] = 1
		suitePutMemory(t, s, source, nil)
		child := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		child.Metadata["source_episodes"] = []string{source.ID}
		child.Metadata["source_revisions"] = map[string]int64{source.ID: 1}
		suitePutMemory(t, s, child, nil)
		if err := s.TouchMemories([]string{source.ID}, schema.Now()); err != nil {
			t.Fatal(err)
		}
		if fresh, err := IsFresh(s, child, schema.Now()); err != nil || !fresh {
			t.Fatalf("Touch changed source revision: %v %v", fresh, err)
		}
		tx, err := contractTransaction(s)
		if err != nil {
			t.Fatal(err)
		}
		source.Metadata["topic_revision"] = 2
		if err := tx.put(MemoryWrite{Memory: source, PreserveEmbedding: true}, true); err != nil {
			tx.rollback()
			t.Fatal(err)
		}
		if err := tx.commit(); err != nil {
			t.Fatal(err)
		}
		if fresh, err := IsFresh(s, child, schema.Now()); err != nil || fresh {
			t.Fatalf("changed managed source revision trusted: %v %v", fresh, err)
		}
	})
}
