package storage

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func failPGDescendantWrite(t *testing.T, s *PostgresStore, id string) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), fmt.Sprintf(`CREATE FUNCTION fail_invalidation() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF OLD.id='%s' THEN RAISE EXCEPTION 'invalidation failure'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fail_invalidation BEFORE UPDATE ON memories FOR EACH ROW EXECUTE FUNCTION fail_invalidation()`, id)); err != nil {
		t.Fatal(err)
	}
}

func TestRecursiveInvalidationRollback(t *testing.T) {
	for _, mutation := range []string{"put", "patch", "delete", "batch", "file"} {
		t.Run(mutation, func(t *testing.T) {
			runStoreBackends(t, func(t *testing.T, s Store) {
				chain := invalidationChain(t, s)
				root := chain[0]
				root.Source = "file.go"
				suitePutMemory(t, s, root, nil)
				if err := s.PutCodeSymbol(&schema.CodeSymbol{MemoryID: root.ID, QualifiedName: "pkg.F", FilePath: "file.go"}); err != nil {
					t.Fatal(err)
				}
				failDescendantWrite(t, s, chain[3].ID)
				var err error
				switch mutation {
				case "put":
					changed := root.Clone()
					changed.Content = "changed"
					err = s.PutMemory(changed, nil)
				case "patch":
					err = s.UpdateMemoryContent(root.ID, "changed", "", nil, nil, 0)
				case "delete":
					err = s.DeleteMemory(root.ID)
				case "batch":
					changed := root.Clone()
					changed.Metadata["superseded"] = true
					err = s.ApplyMemoryBatch(MemoryBatch{Writes: []MemoryWrite{{Memory: changed}}, CloseEdgesFor: []string{root.ID}})
				case "file":
					err = s.DeleteCodeArtifactsByFile(root.Workspace, root.Source)
				}
				if err == nil {
					t.Fatal("injected invalidation failure ignored")
				}
				for _, old := range chain {
					got, err := s.GetMemory(old.ID)
					if err != nil || got == nil || got.Content != "original" || !schema.IsUsable(got) {
						t.Fatalf("failed mutation was not rolled back: %+v %v", got, err)
					}
					if schema.ManagedRevisionKey(got) != "" && schema.ManagedRevision(got) != 1 {
						t.Fatal("failed mutation advanced revision")
					}
				}
				symbols, err := s.SymbolsForFile("file.go")
				if err != nil || len(symbols) != 1 {
					t.Fatal("failed deletion removed projection")
				}
			})
		})
	}
}

func invalidationChain(t *testing.T, s Store) []*schema.Memory {
	t.Helper()
	var chain []*schema.Memory
	for i, typ := range []schema.MemoryType{schema.TypeCodeSymbol, schema.TypeClaim, schema.TypeMentalModel, schema.TypeTopicView, schema.TypeFact} {
		m := schema.NewMemory(schema.LayerSemantic, typ)
		m.Content, m.Workspace = "original", "chain"
		if typ == schema.TypeClaim {
			m.Metadata["claim_revision"] = int64(1)
		}
		if typ == schema.TypeTopicView {
			m.Metadata["topic_revision"] = int64(1)
		}
		if i > 0 {
			m.Metadata["source_episodes"] = []string{chain[i-1].ID}
		}
		suitePutMemory(t, s, m, nil)
		chain = append(chain, m)
	}
	return chain
}

func TestRecursiveInvalidation(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		chain := invalidationChain(t, s)
		// A stale intermediary and a cycle must not stop propagation.
		if err := s.PutEdge(schema.NewEdge(chain[1].ID, "abstracts", chain[4].ID)); err != nil {
			t.Fatal(err)
		}
		m := chain[2].Clone()
		m.Metadata["knowledge_status"] = "stale"
		tx, err := contractTransaction(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.put(MemoryWrite{Memory: m, PreserveEmbedding: true}, true); err != nil {
			tx.rollback()
			t.Fatal(err)
		}
		if err := tx.commit(); err != nil {
			t.Fatal(err)
		}
		foreign := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		foreign.Workspace, foreign.Content = "foreign", "untouched"
		suitePutMemory(t, s, foreign, nil)
		// Legacy foreign edges are ignored, not followed across workspaces.
		tx, err = contractTransaction(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.putEdge(schema.NewEdge(foreign.ID, "produced_from", chain[0].ID)); err != nil {
			tx.rollback()
			t.Fatal(err)
		}
		if err := tx.commit(); err != nil {
			t.Fatal(err)
		}
		rejected := schema.NewMemory(schema.LayerSemantic, schema.TypeClaim)
		rejected.Workspace = "chain"
		rejected.Metadata = map[string]any{"source_episode": chain[0].ID, "knowledge_status": "rejected", "claim_revision": 1}
		suitePutMemory(t, s, rejected, nil)
		root := chain[0].Clone()
		root.Content = "changed"
		suitePutMemory(t, s, root, nil)
		for i, old := range chain[1:] {
			got, err := s.GetMemory(old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.MetaString("knowledge_status") != "stale" || !got.MetaBool("evidence_missing") {
				t.Fatalf("descendant %d escaped invalidation: %+v", i+1, got)
			}
			if key := schema.ManagedRevisionKey(got); key != "" {
				if rev, _ := got.MetaFloat(key); rev != 2 {
					t.Fatalf("managed revision = %v, want 2", rev)
				}
			}
		}
		got, _ := s.GetMemory(foreign.ID)
		if !schema.IsUsable(got) {
			t.Fatal("foreign workspace invalidated")
		}
		got, _ = s.GetMemory(rejected.ID)
		if got.MetaString("knowledge_status") != "rejected" {
			t.Fatal("rejection lost")
		}
	})
}

func TestBulkIndexInvalidation(t *testing.T) {
	for _, mutation := range []string{"patch", "delete", "type_source", "symbol", "file", "retire", "explicit"} {
		t.Run(mutation, func(t *testing.T) {
			runStoreBackends(t, func(t *testing.T, s Store) {
				chain := invalidationChain(t, s)
				root := chain[0]
				root.Source = "file.go"
				suitePutMemory(t, s, root, nil)
				sym := &schema.CodeSymbol{MemoryID: root.ID, QualifiedName: "pkg.F", FilePath: "file.go"}
				if err := s.PutCodeSymbol(sym); err != nil {
					t.Fatal(err)
				}
				var err error
				switch mutation {
				case "patch":
					err = s.UpdateMemoryContent(root.ID, "edited", "", nil, nil, 0)
				case "delete":
					err = s.DeleteMemory(root.ID)
				case "type_source":
					err = s.DeleteMemoriesByTypeSource(string(root.Type), root.Source, root.Workspace)
				case "symbol":
					err = s.DeleteSymbolMemories(sym.QualifiedName, root.Workspace)
				case "file":
					err = s.DeleteCodeArtifactsByFile(root.Workspace, root.Source)
				case "retire":
					retired := root.Clone()
					retired.Metadata["superseded"] = true
					err = s.ApplyMemoryBatch(MemoryBatch{Writes: []MemoryWrite{{Memory: retired}}, CloseEdgesFor: []string{root.ID}})
				case "explicit":
					_, err = s.InvalidateDependants(root.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				got, _ := s.GetMemory(chain[4].ID)
				if schema.IsUsable(got) {
					t.Fatal("transitive descendant still usable")
				}
			})
		})
	}
}

func TestManagedWorkspaceAndAbsentGuards(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		chain := invalidationChain(t, s)
		root := chain[0].Clone()
		root.Workspace = "foreign"
		if err := s.PutMemory(root, nil); !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("workspace move: %v", err)
		}

		root = chain[0].Clone()
		root.Content = "forged"
		if err := s.ApplyMemoryBatch(MemoryBatch{AbsentIDs: []string{root.ID}, Writes: []MemoryWrite{{Memory: root}}}); !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("absence guard: %v", err)
		}
		got, _ := s.GetMemory(root.ID)
		if got.Content != "original" {
			t.Fatal("guard failure wrote content")
		}
	})
}

func TestManagedAbsentCreationRace(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		id := schema.NewID()
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for _, content := range []string{"first", "second"} {
			wg.Go(func() {
				m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
				m.ID, m.Content = id, content
				errs <- s.ApplyMemoryBatch(MemoryBatch{AbsentIDs: []string{id}, Writes: []MemoryWrite{{Memory: m}}})
			})
		}
		wg.Wait()
		close(errs)
		wins, conflicts := 0, 0
		for err := range errs {
			if err == nil {
				wins++
			} else if errors.Is(err, ErrMemoryConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("creation wins=%d conflicts=%d", wins, conflicts)
		}
	})
}

func TestEvidenceFreshnessGraphAndRevisions(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := schema.NewMemory(schema.LayerSemantic, schema.TypeEvidence)
		source.Metadata["valid_until"] = 100.0
		suitePutMemory(t, s, source, nil)
		model := schema.NewMemory(schema.LayerL5Mental, schema.TypeMentalModel)
		suitePutMemory(t, s, model, nil)
		entry := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		suitePutMemory(t, s, entry, nil)
		for _, e := range []*schema.Edge{schema.NewEdge(model.ID, "abstracts", source.ID), schema.NewEdge(entry.ID, "produced_from", model.ID)} {
			if err := s.PutEdge(e); err != nil {
				t.Fatal(err)
			}

		}
		if fresh, err := IsFresh(s, entry, 99); err != nil || !fresh {
			t.Fatalf("live graph: %v %v", fresh, err)
		}
		if fresh, err := IsFresh(s, entry, 101); err != nil || fresh {
			t.Fatalf("transitive edge TTL ignored: %v %v", fresh, err)
		}
	})
}

func TestRecursiveAutomaticDeleteStaleIntermediary(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		chain := invalidationChain(t, s)
		tx, err := contractTransaction(s)
		if err != nil {
			t.Fatal(err)
		}
		stale := chain[1].Clone()
		stale.Metadata["knowledge_status"] = "stale"
		if err := tx.put(MemoryWrite{Memory: stale}, true); err != nil {
			tx.rollback()
			t.Fatal(err)
		}
		if err := tx.commit(); err != nil {
			t.Fatal(err)
		}
		deleted, protected, err := s.DeleteMemorySafely(chain[0].ID, schema.Now()+1)
		if err != nil || !deleted || protected {
			t.Fatalf("automatic deletion: %v %v %v", deleted, protected, err)
		}
		got, _ := s.GetMemory(chain[4].ID)
		if schema.IsUsable(got) {
			t.Fatal("automatic deletion left live transitive child")
		}
	})
}
