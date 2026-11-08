package storage

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestTopicSystemExclusionsBeforeTopK(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		target := contractMemory(schema.TypeCodeSymbol)
		suitePutMemory(t, s, target, []float32{1, 1, 0, 0, 0, 0, 0, 0})
		for i := range 200 {
			typ := []schema.MemoryType{schema.TypeMaintenanceJob, schema.TypeTaskState, schema.TypeEvidence, schema.TypeForwardIntent}[i%4]
			m := contractMemory(typ)
			suitePutMemory(t, s, m, contractVector())
		}
		for _, filter := range []MemoryFilter{
			{Workspace: "contract", ExcludeSystem: true},
			{Workspace: "contract", ExcludeTypes: []schema.MemoryType{schema.TypeMaintenanceJob, schema.TypeTaskState, schema.TypeEvidence, schema.TypeForwardIntent}},
		} {
			hits, err := s.SearchMemories(contractVector(), 1, filter)
			if err != nil || len(hits) != 1 || hits[0].ID != target.ID {
				t.Fatalf("system topK: %v %v", hits, err)
			}
		}
	})
}

func TestTopicAtomicFreshness(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeFact)
		source.Metadata["valid_until"] = schema.Now() - 1
		suitePutMemory(t, s, source, nil)
		fingerprint, err := schema.SourceFingerprint(source)
		if err != nil {
			t.Fatal(err)
		}
		topic := contractMemory(schema.TypeTopicView)
		topic.Content = "must never publish stale body"
		topic.Metadata = map[string]any{"topic_revision": 1, "source_episodes": []string{source.ID}, "source_fingerprints": map[string]string{source.ID: fingerprint}, "source_revisions": map[string]int64{source.ID: 0}}
		err = s.ApplyMemoryBatch(MemoryBatch{AbsentIDs: []string{topic.ID}, Guards: []MemoryGuard{{Expected: source}}, Writes: []MemoryWrite{{Memory: topic}}})
		if !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("stale atomic topic accepted: %v", err)
		}
		got, err := s.GetMemory(topic.ID)
		if err != nil || got != nil {
			t.Fatalf("failed topic persisted: %+v %v", got, err)
		}
	})
}
