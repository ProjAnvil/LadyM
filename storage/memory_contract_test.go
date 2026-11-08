package storage

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func contractVector() []float32 { return []float32{1, 0, 0, 0, 0, 0, 0, 0} }

func contractMemory(typ schema.MemoryType) *schema.Memory {
	m := schema.NewMemory(schema.LayerSemantic, typ)
	m.Workspace = "contract"
	m.LastAccessAt = 1
	return m
}

func TestSearchMemoriesScopeBeforeTopK(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		target := contractMemory(schema.TypeFact)
		suitePutMemory(t, s, target, []float32{1, 1, 0, 0, 0, 0, 0, 0})
		for i := range 4 {
			m := contractMemory(schema.TypeNote)
			if i < 2 {
				m.Type = schema.TypeFact
				m.Workspace = "other"
			}
			suitePutMemory(t, s, m, contractVector())
		}
		for _, meta := range []map[string]any{{"superseded": true}, {"knowledge_status": "stale"}, {"evidence_missing": true}} {
			m := contractMemory(schema.TypeFact)
			m.Metadata = meta
			suitePutMemory(t, s, m, contractVector())
		}
		hits, err := s.SearchMemories(contractVector(), 1, MemoryFilter{
			Workspace: "contract", Layers: []schema.Layer{schema.LayerSemantic},
			Types: []schema.MemoryType{schema.TypeFact}, ExcludeRetired: true, ExcludeUnusable: true,
		})
		if err != nil || len(hits) != 1 || hits[0].ID != target.ID {
			t.Fatalf("filtered topK = %v, %v; want target %s", hits, err, target.ID)
		}
	})
}

func TestSearchMemoriesInvalidAndClosed(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := contractMemory(schema.TypeFact)
		suitePutMemory(t, s, m, contractVector())
		for _, vec := range [][]float32{{1}, {float32(math.NaN()), 0, 0, 0, 0, 0, 0, 0}, {float32(math.Inf(1)), 0, 0, 0, 0, 0, 0, 0}} {
			if _, err := s.SearchMemories(vec, 1, MemoryFilter{}); err == nil {
				t.Fatal("invalid query accepted")
			}
			changed := m.Clone()
			changed.Content = "must not persist"
			if err := s.PutMemory(changed, vec); err == nil {
				t.Fatal("invalid write accepted")
			}
			got, err := s.GetMemory(m.ID)
			if err != nil || got.Content != "" {
				t.Fatalf("invalid vector mutated storage: %v %v", got, err)
			}
		}
		if _, err := s.SearchMemories(contractVector(), 0, MemoryFilter{}); err == nil {
			t.Fatal("zero topK accepted")
		}
		if _, err := s.SearchMemories(contractVector(), 1, MemoryFilter{}); err != nil {
			t.Fatal(err)
		}
		s.Close()
		if _, err := s.SearchMemories(contractVector(), 1, MemoryFilter{}); err == nil {
			t.Fatal("closed DB returned success")
		}
	})
}

func TestSearchMemoriesPathExclusionBeforeTopK(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		for i := range 50 {
			m := contractMemory(schema.TypeResource)
			m.ID = fmt.Sprintf("a-path-%03d", i)
			m.Metadata["resource_identity"] = "path"
			suitePutMemory(t, s, m, contractVector())
		}
		target := contractMemory(schema.TypeResource)
		target.ID = "z-ordinary"
		suitePutMemory(t, s, target, contractVector())
		filter := MemoryFilter{Workspace: "contract", Types: []schema.MemoryType{schema.TypeResource}, ExcludeUnusable: true}
		hits, err := s.SearchMemories(contractVector(), 50, filter)
		if err != nil || len(hits) != 50 || hits[0].ID != "a-path-000" || slices.ContainsFunc(hits, func(h SearchHit) bool { return h.ID == target.ID }) {
			t.Fatalf("fixture does not starve ordinary resource: %v %v", hits, err)
		}
		filter.ExcludePathResources = true
		hits, err = s.SearchMemories(contractVector(), 1, filter)
		if err != nil || len(hits) != 1 || hits[0].ID != target.ID {
			t.Fatalf("path exclusion must precede truncation: %v %v", hits, err)
		}
	})
}

func TestMemoryBatchRollbackAndConflict(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		source.Layer = schema.LayerEpisodic
		target := contractMemory(schema.TypeFact)
		suitePutMemory(t, s, source, contractVector())
		suitePutMemory(t, s, target, contractVector())
		stamp := source.Clone()
		stamp.Metadata["consolidated_at"] = 10
		retire := target.Clone()
		retire.Metadata["superseded"] = true
		add := contractMemory(schema.TypeFact)
		batch := MemoryBatch{
			Guards: []MemoryGuard{{Expected: source, PendingEpisode: true}, {Expected: target}},
			Writes: []MemoryWrite{{Memory: stamp, PreserveEmbedding: true}, {Memory: retire, PreserveEmbedding: true}, {Memory: add, Vector: contractVector()}},
			Edges:  []*schema.Edge{schema.NewEdge(add.ID, "produced_from", "missing")},
		}
		if err := s.ApplyMemoryBatch(batch); err == nil {
			t.Fatal("missing endpoint accepted")
		}
		for _, m := range []*schema.Memory{source, target} {
			got, err := s.GetMemory(m.ID)
			if err != nil || got.MetaBool("consolidated_at") || schema.IsRetired(got) {
				t.Fatalf("partial batch persisted: %v %v", got, err)
			}
		}
		if got, _ := s.GetMemory(add.ID); got != nil {
			t.Fatal("insert survived rollback")
		}
		batch.Edges = nil
		for _, original := range []*schema.Memory{source, target} {
			modified := original.Clone()
			modified.Content = "concurrent edit"
			suitePutMemory(t, s, modified, contractVector())
			if err := s.ApplyMemoryBatch(batch); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("stale snapshot = %v; want conflict", err)
			}
			suitePutMemory(t, s, original, contractVector())
		}
		if err := s.ApplyMemoryBatch(batch); err != nil {
			t.Fatal(err)
		}
		if err := s.ApplyMemoryBatch(batch); !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("double consumption = %v", err)
		}
	})
}

func TestMemoryBatchRoundtripAndBookkeeping(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := contractMemory(schema.TypeFact)
		m.Metadata = map[string]any{"source_episodes": []string{}, "n": int64(9007199254740993)}
		suitePutMemory(t, s, m, contractVector())
		if err := s.TouchMemories([]string{m.ID}, 50); err != nil {
			t.Fatal(err)
		}

		patch := m.Clone()
		patch.Content = "new"
		edge := schema.NewEdge(m.ID, "related_to", m.ID)
		if err := s.PutEdge(edge); err != nil {
			t.Fatal(err)
		}
		replacement := schema.NewEdge(m.ID, "related_to", m.ID)
		if err := s.ApplyMemoryBatch(MemoryBatch{Guards: []MemoryGuard{{Expected: m}},
			Writes:        []MemoryWrite{{Memory: patch, PreserveEmbedding: true}},
			CloseEdgesFor: []string{m.ID}, Edges: []*schema.Edge{replacement}}); err != nil {
			t.Fatalf("roundtrip/Touch caused conflict: %v", err)
		}
		hits, err := s.SearchMemories(contractVector(), 1, MemoryFilter{})
		if err != nil || len(hits) != 1 || hits[0].ID != m.ID {
			t.Fatalf("preserved embedding = %v %v", hits, err)
		}
		edges, err := s.Neighbors(m.ID, "")
		if err != nil || len(edges) != 1 || edges[0].ID != replacement.ID {
			t.Fatalf("edge replacement = %v %v", edges, err)
		}
		got, _ := s.GetMemory(m.ID)
		if got.LastAccessAt != 50 || got.AccessCount != 1 {
			t.Fatalf("batch overwrote Touch: %+v", got)
		}
		if err := s.ApplyMemoryBatch(MemoryBatch{Writes: []MemoryWrite{{Memory: got}}}); err != nil {
			t.Fatal(err)
		}
		hits, err = s.SearchMemories(contractVector(), 1, MemoryFilter{})
		if err != nil || len(hits) != 0 {
			t.Fatalf("nil vector did not clear embedding: %v %v", hits, err)
		}
	})
}

func TestMemoryBatchConcurrentConsumption(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		source.Layer = schema.LayerEpisodic
		suitePutMemory(t, s, source, nil)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			derived := contractMemory(schema.TypeFact)
			stamp := source.Clone()
			stamp.Metadata["consolidated_at"] = 10
			batch := MemoryBatch{Guards: []MemoryGuard{{Expected: source, PendingEpisode: true}},
				Writes: []MemoryWrite{{Memory: stamp}, {Memory: derived}},
				Edges:  []*schema.Edge{schema.NewEdge(derived.ID, "produced_from", source.ID)}}
			wg.Go(func() {
				<-start
				results <- s.ApplyMemoryBatch(batch)
			})
		}
		close(start)
		wg.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrMemoryConflict):
				conflicts++
			default:
				t.Fatal(err)
			}
		}
		facts, err := s.IterMemories("contract", "", string(schema.TypeFact))
		if err != nil || successes != 1 || conflicts != 1 || len(facts) != 1 {
			t.Fatalf("concurrent consumption: success=%d conflict=%d facts=%v err=%v", successes, conflicts, facts, err)
		}
	})
}

func TestSafeDeleteEvidenceSnapshotParity(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		source.Metadata["source_episode"] = source.ID
		suitePutMemory(t, s, source, nil)
		foreign := contractMemory(schema.TypeFact)
		foreign.Workspace = "foreign"
		// Simulate legacy cross-workspace metadata; new writes reject it.
		switch db := s.(type) {
		case *PostgresStore:
			suitePutMemory(t, s, foreign, nil)
			if _, err := db.pool.Exec(t.Context(), "UPDATE memories SET metadata=jsonb_build_object('source_episode',$1::text) WHERE id=$2", source.ID, foreign.ID); err != nil {
				t.Fatal(err)
			}

		default:
			putLegacyForeignReference(t, s, foreign, source.ID)
		}
		refs, err := s.EvidenceReferences("")
		if err != nil || refs[source.ID] {
			t.Fatalf("self/cross-workspace reference counted: %v %v", refs, err)
		}
		if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || !deleted || protected {
			t.Fatalf("snapshot disagrees with deletion: %v %v %v", deleted, protected, err)
		}
	})
}

func TestSafeDeleteGraphOnlyProvenanceRetained(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeFact)
		other := contractMemory(schema.TypeFact)
		another := contractMemory(schema.TypeFact)
		suitePutMemory(t, s, source, contractVector())
		suitePutMemory(t, s, other, contractVector())
		suitePutMemory(t, s, another, contractVector())
		for _, relation := range []string{"abstracts", "produced_from"} {
			dependant := contractMemory(schema.TypeMentalModel)
			dependant.Layer = schema.LayerL5Mental
			dependant.Metadata["marker"] = "original"
			if relation == "produced_from" {
				dependant.Metadata["source_episode"] = other.ID
				dependant.Metadata["source_episodes"] = []string{another.ID}
			}
			suitePutMemory(t, s, dependant, contractVector())
			if err := s.PutEdge(schema.NewEdge(dependant.ID, relation, source.ID)); err != nil {
				t.Fatal(err)
			}
		}
		if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || deleted || !protected {
			t.Fatalf("graph-only evidence not protected: %v %v %v", deleted, protected, err)
		}
		if err := s.DeleteMemory(source.ID); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetMemory(source.ID); err != nil || got != nil {
			t.Fatalf("source survived explicit deletion: %+v %v", got, err)
		}
		dependants, err := s.IterMemories("contract", string(schema.LayerL5Mental), "")
		if err != nil || len(dependants) != 2 {
			t.Fatalf("dependants: %v %v", dependants, err)
		}
		for _, m := range dependants {
			if schema.IsUsable(m) || m.MetaString("knowledge_status") != "stale" || m.Meta("evidence_missing") != true || m.MetaString("marker") != "original" {
				t.Errorf("lost invalidation or original metadata: %+v", m)
			}
			ids := legacySources(m)
			if !slices.Contains(ids, source.ID) {
				t.Errorf("cascade erased graph-only lost source ID: %v", m.Metadata)
			}
			if m.MetaString("source_episode") == other.ID &&
				(!slices.Contains(ids, other.ID) || !slices.Contains(ids, another.ID)) {
				t.Errorf("deletion replaced legacy provenance: %v", m.Metadata)
			}
			edges, err := s.Neighbors(m.ID, "")
			if err != nil || slices.ContainsFunc(edges, func(e *schema.Edge) bool { return e.DstID == source.ID }) {
				t.Errorf("source edge did not cascade: %+v %v", edges, err)
			}
		}
	})
}
func TestMemoryBatchDatabaseFailureRollback(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		source.Layer = schema.LayerEpisodic
		target := contractMemory(schema.TypeFact)
		suitePutMemory(t, s, source, contractVector())
		suitePutMemory(t, s, target, contractVector())
		edge := schema.NewEdge(target.ID, "related_to", source.ID)
		if err := s.PutEdge(edge); err != nil {
			t.Fatal(err)
		}
		installContractFault(t, s)
		stamp, retired := source.Clone(), target.Clone()
		stamp.Metadata["consolidated_at"] = 10
		retired.Metadata["superseded"] = true
		add := contractMemory(schema.TypeFact)
		batch := MemoryBatch{Guards: []MemoryGuard{{Expected: source, PendingEpisode: true}, {Expected: target}},
			Writes:        []MemoryWrite{{Memory: stamp, PreserveEmbedding: true}, {Memory: retired, PreserveEmbedding: true}, {Memory: add}},
			CloseEdgesFor: []string{target.ID}, Edges: []*schema.Edge{schema.NewEdge(add.ID, "fail_contract", source.ID)}}
		if err := s.ApplyMemoryBatch(batch); err == nil {
			t.Fatal("injected database fault did not fail")
		}
		gotSource, _ := s.GetMemory(source.ID)
		gotTarget, _ := s.GetMemory(target.ID)
		gotAdd, _ := s.GetMemory(add.ID)
		edges, err := s.Neighbors(target.ID, "")
		if err != nil || gotSource.MetaBool("consolidated_at") || schema.IsRetired(gotTarget) || gotAdd != nil || len(edges) != 1 || edges[0].ID != edge.ID {
			t.Fatalf("fault leaked partial mutation: source=%v target=%v add=%v edges=%v err=%v", gotSource, gotTarget, gotAdd, edges, err)
		}
		// Deletion fails after dependants have been marked stale; those updates
		// must roll back together with the failed source deletion.
		if err := s.PutEdge(schema.NewEdge(target.ID, "produced_from", source.ID)); err != nil {
			t.Fatal(err)
		}
		if deleted, _, err := s.DeleteMemorySafely(source.ID, 0); err == nil || deleted {
			t.Fatalf("injected delete fault = %v %v", deleted, err)
		}
		gotSource, _ = s.GetMemory(source.ID)
		gotTarget, _ = s.GetMemory(target.ID)
		if gotSource == nil || !schema.IsUsable(gotTarget) || slices.Contains(legacySources(gotTarget), source.ID) {
			t.Fatalf("failed delete partially invalidated evidence: %v %v", gotSource, gotTarget)
		}
	})
}

func installContractFault(t *testing.T, s Store) {
	t.Helper()
	if db, ok := s.(*PostgresStore); ok {
		for _, query := range []string{
			`CREATE FUNCTION fail_contract_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN RAISE EXCEPTION 'injected contract fault'; END; $$`,
			`CREATE TRIGGER fail_contract_edge BEFORE INSERT ON edges FOR EACH ROW
				WHEN (NEW.relation='fail_contract') EXECUTE FUNCTION fail_contract_mutation()`,
			`CREATE TRIGGER fail_contract_delete BEFORE DELETE ON memories FOR EACH ROW
				EXECUTE FUNCTION fail_contract_mutation()`,
		} {
			if _, err := db.pool.Exec(t.Context(), query); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	installSQLiteContractFault(t, s)
}

func TestSafeDeleteIgnoresMalformedLegacyList(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		suitePutMemory(t, s, source, nil)
		dependant := contractMemory(schema.TypeFact)
		dependant.Metadata["source_episodes"] = source.ID
		suitePutMemory(t, s, dependant, nil)
		refs, err := s.EvidenceReferences("contract")
		if err != nil || refs[source.ID] {
			t.Fatalf("scalar source_episodes treated as list: %v %v", refs, err)
		}
		if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || !deleted || protected {
			t.Fatalf("malformed list broke safe deletion: %v %v %v", deleted, protected, err)
		}
	})
}

func TestSafeDeleteInvalidCutoff(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		m := contractMemory(schema.TypeEvent)
		suitePutMemory(t, s, m, nil)
		for _, cutoff := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			if deleted, protected, err := s.DeleteMemorySafely(m.ID, cutoff); err == nil || deleted || protected {
				t.Fatalf("invalid cutoff %v = %v %v %v", cutoff, deleted, protected, err)
			}
		}
		if got, err := s.GetMemory(m.ID); err != nil || got == nil {
			t.Fatalf("invalid cutoff deleted memory: %v %v", got, err)
		}
		if deleted, protected, err := s.DeleteMemorySafely(m.ID, 0.5); err != nil || deleted || protected {
			t.Fatalf("last access eligibility = %v %v %v", deleted, protected, err)
		}
	})
}

func TestSafeDeleteDependencies(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := contractMemory(schema.TypeEvent)
		source.Layer = schema.LayerEpisodic
		suitePutMemory(t, s, source, contractVector())
		for i, typ := range []schema.MemoryType{schema.TypeFact, schema.TypeMentalModel, schema.TypePlaybook} {
			dependant := contractMemory(typ)
			if i == 0 {
				dependant.Metadata["source_episode"] = source.ID
			} else if i == 1 {
				dependant.Metadata["source_episodes"] = []string{source.ID}
			}
			suitePutMemory(t, s, dependant, nil)
			if i == 2 {
				if err := s.PutEdge(schema.NewEdge(dependant.ID, "abstracts", source.ID)); err != nil {
					t.Fatal(err)
				}
			}
		}
		refs, err := s.EvidenceReferences("contract")
		if err != nil || !refs[source.ID] {
			t.Fatalf("references = %v %v", refs, err)
		}
		if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || deleted || !protected {
			t.Fatalf("auto delete referenced source = %v %v %v", deleted, protected, err)
		}
		if err := s.DeleteMemory(source.ID); err != nil {
			t.Fatal(err)
		}
		memories, _ := s.IterMemories("contract", "", "")
		for _, m := range memories {
			if m.MetaString("knowledge_status") != "stale" || !m.MetaBool("evidence_missing") {
				t.Fatalf("dependant not invalidated: %+v", m)
			}
		}
		if deleted, protected, err := s.DeleteMemorySafely("missing", 0); err != nil || deleted || protected {
			t.Fatalf("missing delete = %v %v %v", deleted, protected, err)
		}
		source = contractMemory(schema.TypeEvent)
		suitePutMemory(t, s, source, nil)
		retired := contractMemory(schema.TypeFact)
		retired.Metadata["source_episode"] = source.ID
		retired.Metadata["superseded"] = true
		suitePutMemory(t, s, retired, nil)
		if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || !deleted || protected {
			t.Fatalf("retired-only dependency = %v %v %v", deleted, protected, err)
		}
	})
}

func TestSafeDeleteConcurrentReferenceCommit(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		for _, referenceFirst := range []bool{true, false} {
			source := contractMemory(schema.TypeEvent)
			source.Layer = schema.LayerEpisodic
			suitePutMemory(t, s, source, nil)
			derived := contractMemory(schema.TypeFact)
			derived.Metadata["source_episode"] = source.ID
			edge := schema.NewEdge(derived.ID, "produced_from", source.ID)
			batch := MemoryBatch{Guards: []MemoryGuard{{Expected: source, PendingEpisode: true}},
				Writes: []MemoryWrite{{Memory: derived}}, Edges: []*schema.Edge{edge}}
			tx := beginContractTransaction(t, s, batch)
			started := make(chan struct{})
			if referenceFirst {
				if err := tx.put(batch.Writes[0], true); err != nil {
					tx.rollback()
					t.Fatal(err)
				}
				if err := tx.putEdge(edge); err != nil {
					tx.rollback()
					t.Fatal(err)
				}
				result := make(chan struct {
					deleted, protected bool
					err                error
				}, 1)
				go func() {
					close(started)
					deleted, protected, err := s.DeleteMemorySafely(source.ID, 2)
					result <- struct {
						deleted, protected bool
						err                error
					}{deleted, protected, err}
				}()
				<-started
				if err := tx.commit(); err != nil {
					t.Fatal(err)
				}
				got := <-result
				if got.err != nil || got.deleted || !got.protected {
					t.Fatalf("in-flight reference lost: %+v", got)
				}
			} else {
				if err := tx.remove(source.ID); err != nil {
					tx.rollback()
					t.Fatal(err)
				}
				result := make(chan error, 1)
				go func() {
					close(started)
					result <- s.ApplyMemoryBatch(batch)
				}()
				<-started
				if err := tx.commit(); err != nil {
					t.Fatal(err)
				}
				if err := <-result; !errors.Is(err, ErrMemoryConflict) {
					t.Fatalf("in-flight delete did not conflict: %v", err)
				}
			}
		}
	})
}

func beginContractTransaction(t *testing.T, s Store, batch MemoryBatch) memoryTransaction {
	t.Helper()
	if db, ok := s.(*PostgresStore); ok {
		tx, err := db.beginMemoryTransaction(batch)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	return beginSQLiteContractTransaction(t, s)
}

func TestSafeDeleteReferenceRace(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		for _, referenceFirst := range []bool{true, false} {
			source := contractMemory(schema.TypeEvent)
			source.Layer = schema.LayerEpisodic
			suitePutMemory(t, s, source, nil)
			derived := contractMemory(schema.TypeFact)
			derived.Metadata["source_episode"] = source.ID
			batch := MemoryBatch{Guards: []MemoryGuard{{Expected: source, PendingEpisode: true}},
				Writes: []MemoryWrite{{Memory: derived}}, Edges: []*schema.Edge{schema.NewEdge(derived.ID, "produced_from", source.ID)}}
			if referenceFirst {
				if err := s.ApplyMemoryBatch(batch); err != nil {
					t.Fatal(err)
				}
				if deleted, protected, err := s.DeleteMemorySafely(source.ID, 2); err != nil || deleted || !protected {
					t.Fatalf("committed reference lost: %v %v %v", deleted, protected, err)
				}
			} else {
				if deleted, _, err := s.DeleteMemorySafely(source.ID, 2); err != nil || !deleted {
					t.Fatalf("delete first = %v %v", deleted, err)
				}
				if err := s.ApplyMemoryBatch(batch); !errors.Is(err, ErrMemoryConflict) {
					t.Fatalf("deleted source guard = %v", err)
				}
				if err := s.PutMemory(derived, nil); !errors.Is(err, ErrMemoryConflict) {
					t.Fatalf("unguarded dangling legacy reference accepted: %v", err)
				}
			}
		}
	})
}
