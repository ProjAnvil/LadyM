package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

type freshnessCountingStore struct {
	Store
	reads, neighborReads map[string]int
	readLimit, total     int
	failID               string
	failNeighbors        bool
}

func (s *freshnessCountingStore) GetMemory(id string) (*schema.Memory, error) {
	s.reads[id]++
	s.total++
	if s.total > s.readLimit {
		return nil, fmt.Errorf("source read budget exceeded: %d > %d", s.total, s.readLimit)
	}
	if id == s.failID && !s.failNeighbors {
		return nil, fmt.Errorf("source read canceled: %w", context.Canceled)
	}
	return s.Store.GetMemory(id)
}

func (s *freshnessCountingStore) Neighbors(id, relation string) ([]*schema.Edge, error) {
	s.neighborReads[id]++
	if id == s.failID && s.failNeighbors {
		return nil, fmt.Errorf("dependency read canceled: %w", context.Canceled)
	}
	return s.Store.Neighbors(id, relation)
}

func countFreshness(s Store, nodes int) *freshnessCountingStore {
	return &freshnessCountingStore{
		Store: s, reads: map[string]int{}, neighborReads: map[string]int{}, readLimit: 2 * nodes,
	}
}

func putFreshnessNode(t *testing.T, s Store, id string, sources ...*schema.Memory) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.ID, m.Content, m.Workspace = id, id, "test"
	ids, revisions := []string{}, map[string]int64{}
	for _, source := range sources {
		ids = append(ids, source.ID)
		revisions[source.ID] = schema.ManagedRevision(source)
	}
	m.Metadata["source_episodes"], m.Metadata["source_revisions"] = ids, revisions
	if len(ids) > 0 {
		m.Metadata["source_episode"] = ids[0]
	}
	suitePutMemory(t, s, m, nil)
	for _, source := range sources {
		if err := s.PutEdge(schema.NewEdge(m.ID, "produced_from", source.ID)); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestEvidenceFreshnessLinearSourceReads(t *testing.T) {
	for _, shape := range []string{"20 hop dual provenance", "shared diamonds", "cycle"} {
		t.Run(shape, func(t *testing.T) {
			runStoreBackends(t, func(t *testing.T, s Store) {
				root := putFreshnessNode(t, s, "leaf")
				nodes, wantFresh := 1, true
				switch shape {
				case "20 hop dual provenance":
					for i := range 20 {
						root = putFreshnessNode(t, s, fmt.Sprintf("hop-%02d", i), root)
						nodes++
					}
				case "shared diamonds":
					for i := range 20 {
						left := putFreshnessNode(t, s, fmt.Sprintf("left-%02d", i), root)
						right := putFreshnessNode(t, s, fmt.Sprintf("right-%02d", i), root)
						root = putFreshnessNode(t, s, fmt.Sprintf("merge-%02d", i), left, right)
						nodes += 3
					}
				case "cycle":
					middle := putFreshnessNode(t, s, "middle", root)
					top := putFreshnessNode(t, s, "top", middle)
					if err := s.PutEdge(schema.NewEdge(root.ID, "produced_from", top.ID)); err != nil {
						t.Fatal(err)
					}
					nodes, wantFresh = 3, false
				}
				counted := countFreshness(s, nodes)
				fresh, err := IsFresh(counted, root, schema.Now())
				if err != nil || fresh != wantFresh {
					t.Fatalf("fresh=%v want=%v err=%v; source reads=%d nodes=%d", fresh, wantFresh, err, counted.total, nodes)
				}
				if counted.total > nodes-1 {
					t.Errorf("source reads=%d exceeds unique sources=%d", counted.total, nodes-1)
				}
				for id, reads := range counted.reads {
					if reads > 1 {
						t.Errorf("source %s read %d times", id, reads)
					}
				}
				for id, reads := range counted.neighborReads {
					if reads > 1 {
						t.Errorf("node %s evaluated %d times", id, reads)
					}
				}
				t.Logf("nodes=%d source_reads=%d evaluated_nodes=%d fresh=%v", nodes, counted.total, len(counted.neighborReads), fresh)
			})
		})
	}
}

func TestEvidenceFreshnessMemoPreservesSourceGuards(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		shared := schema.NewMemory(schema.LayerSemantic, schema.TypeTopicView)
		shared.Workspace = "test"
		shared.Metadata["topic_revision"] = 1
		suitePutMemory(t, s, shared, nil)
		left := putFreshnessNode(t, s, "left", shared)
		right := putFreshnessNode(t, s, "right", shared)
		right.Metadata["source_revisions"] = map[string]int64{shared.ID: 2}
		suitePutMemory(t, s, right, nil)
		root := putFreshnessNode(t, s, "root", left, right)
		counted := countFreshness(s, 4)
		if fresh, err := IsFresh(counted, root, schema.Now()); err != nil || fresh {
			t.Fatalf("cached source bypassed parent's revision guard: fresh=%v err=%v", fresh, err)
		}
		if counted.reads[shared.ID] != 1 {
			t.Fatalf("shared source read %d times, want 1", counted.reads[shared.ID])
		}
	})
}

func TestEvidenceFreshnessMemoCallIsolationAndErrors(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		leaf := schema.NewMemory(schema.LayerSemantic, schema.TypeEvidence)
		leaf.Workspace = "test"
		leaf.Metadata["valid_until"] = 100
		suitePutMemory(t, s, leaf, nil)
		root := putFreshnessNode(t, s, "root", leaf)
		counted := countFreshness(s, 20)
		for _, tc := range []struct {
			now  float64
			want bool
		}{{99, true}, {100, false}, {99, true}} {
			if fresh, err := IsFresh(counted, root, tc.now); err != nil || fresh != tc.want {
				t.Fatalf("per-call TTL at %g: fresh=%v want=%v err=%v", tc.now, fresh, tc.want, err)
			}
		}
		foreign := root.Clone()
		foreign.Workspace = "foreign"
		if fresh, err := IsFresh(counted, foreign, 99); err != nil || fresh {
			t.Fatalf("cached source bypassed workspace: fresh=%v err=%v", fresh, err)
		}
		for _, failNeighbors := range []bool{false, true} {
			failing := countFreshness(s, 2)
			failing.failID, failing.failNeighbors = leaf.ID, failNeighbors
			if fresh, err := IsFresh(failing, root, 99); fresh || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled provenance read swallowed: fresh=%v err=%v", fresh, err)
			}
			failing.failID = ""
			if fresh, err := IsFresh(failing, root, 99); err != nil || !fresh {
				t.Fatalf("failed call poisoned later evaluation: fresh=%v err=%v", fresh, err)
			}
		}
	})
}

func TestEvidenceFreshnessDeduplicatedRefsStillValidateEveryRef(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		source := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvidence)
		source.Content, source.Workspace = "attested observation", "test"
		source.Metadata["verification_kind"] = "observation"
		suitePutMemory(t, s, source, nil)
		ref := schema.EvidenceRef{MemoryID: source.ID, Kind: "observation", Fingerprint: schema.KnowledgeFingerprint(source)}
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeClaim)
		m.Content, m.Workspace = "observed claim", "test"
		m.Metadata["source_episodes"] = []string{source.ID}
		m.Metadata["source_revisions"] = map[string]int64{source.ID: 0}
		claim := &schema.Claim{
			Memory: m, Revision: 1, Status: "inferred", Evidence: []schema.EvidenceRef{ref},
			Verification: &schema.Verification{Method: "observation", Evidence: []schema.EvidenceRef{ref}},
		}
		schema.SetClaimData(claim)
		suitePutMemory(t, s, m, nil)
		if err := s.PutEdge(schema.NewEdge(m.ID, "produced_from", source.ID)); err != nil {
			t.Fatal(err)
		}
		for _, valid := range []bool{true, false} {
			counted := countFreshness(s, 2)
			if !valid {
				claim.Verification.Evidence[0].Fingerprint = "mismatched"
				schema.SetClaimData(claim)
			}
			if fresh, err := IsFresh(counted, m, schema.Now()); err != nil || fresh != valid {
				t.Fatalf("duplicate verification ref: fresh=%v want=%v err=%v", fresh, valid, err)
			}
			if counted.reads[source.ID] != 1 || counted.neighborReads[source.ID] > 1 {
				t.Fatalf("duplicate evidence read/evaluation: reads=%v nodes=%v", counted.reads, counted.neighborReads)
			}
		}
	})
}

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
