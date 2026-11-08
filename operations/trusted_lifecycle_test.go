//go:build !enterprise

package operations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type trustedEmbedding struct{}

type trustedFixtureStore struct {
	storage.Store
	path string
}

func (trustedEmbedding) Embed(string) ([]float32, error) { return []float32{1, 0}, nil }
func (e trustedEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i], _ = e.Embed(texts[i])
	}
	return out, nil
}
func (trustedEmbedding) Dim() int                    { return 2 }
func (trustedEmbedding) HealthCheck() (bool, string) { return true, "" }

func trustedStore(t *testing.T) (storage.Store, trustedEmbedding) {
	t.Helper()
	path := ".trusted-" + schema.NewID() + ".db"
	s, err := storage.NewStore(path, 2, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
	})
	return trustedFixtureStore{Store: s, path: path}, trustedEmbedding{}
}

func trustedMemory(t *testing.T, s storage.Store, layer schema.Layer, typ schema.MemoryType, content, ws string, meta map[string]any, configure ...func(*schema.Memory)) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(layer, typ)
	m.Content, m.Workspace = content, ws
	m.ContentHash = schema.ContentHash(content)
	if meta != nil {
		m.Metadata = meta
	}
	for _, f := range configure {
		f(m)
	}
	if err := s.PutMemory(m, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	return m
}

func trustedEdge(t *testing.T, s storage.Store, src, rel, dst string) {
	t.Helper()
	if err := s.PutEdge(schema.NewEdge(src, rel, dst)); err != nil {
		t.Fatal(err)
	}
}

func TestConsolidateTypedOffline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     schema.MemoryType
		content string
		meta    map[string]any
		want    Action
	}{
		{"resource", schema.TypeResource, "candidate", nil, ActionAdd},
		{"code", schema.TypeCodeFile, "candidate", nil, ActionAdd},
		{"similar", schema.TypeFact, "different", nil, ActionAdd},
		{"exact", schema.TypeFact, "candidate", nil, ActionNoop},
		{"retired", schema.TypeFact, "candidate", map[string]any{"superseded": true}, ActionAdd},
		{"stale", schema.TypeNote, "candidate", map[string]any{"knowledge_status": "stale"}, ActionAdd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, emb := trustedStore(t)
			old := trustedMemory(t, s, schema.LayerSemantic, tc.typ, tc.content, "test", tc.meta)
			trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
			report, err := Consolidate(s, emb, config.Default(), "test", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if report.Actions[string(tc.want)] != 1 {
				t.Errorf("want %s: %+v", tc.want, report)
			}
			got, _ := s.GetMemory(old.ID)
			if IsRetired(got) != IsRetired(old) {
				t.Error("offline consolidation retired evidence")
			}
		})
	}
}

func TestConsolidateExactIndependentOfVectorRank(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    schema.MemoryType
		vector []float32
		decoys int
	}{
		{"crowded fact", schema.TypeFact, []float32{0.8, 0.6}, 8},
		{"crowded note", schema.TypeNote, []float32{0.8, 0.6}, 9},
		{"unembedded fact", schema.TypeFact, nil, 0},
		{"unembedded note", schema.TypeNote, nil, 0},
		{"below similarity cutoff", schema.TypeFact, []float32{-1, 0}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, emb := trustedStore(t)
			cfg := config.Default()
			for i := range tc.decoys {
				trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, fmt.Sprintf("decoy %d", i), "test", nil)
			}
			exact := trustedMemory(t, s, schema.LayerSemantic, tc.typ, "candidate", "test",
				map[string]any{"knowledge_status": "verified", "marker": "exact"})
			if err := s.PutMemory(exact, tc.vector); err != nil {
				t.Fatal(err)
			}
			ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test",
				map[string]any{"knowledge_status": "verified", "marker": "episode"})
			raw, err := s.SearchMemories([]float32{1, 0}, cfg.Consolidate.MinEpisodesToTrigger+5, storage.MemoryFilter{
				Workspace: "test", Layers: []schema.Layer{schema.LayerSemantic},
				Types: []schema.MemoryType{schema.TypeFact, schema.TypeNote}, ExcludeUnusable: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range raw {
				if h.ID == exact.ID && h.Similarity >= 0.1 {
					t.Fatal("fixture must hide the exact target from vector candidates")
				}
			}
			wrapped := &beforeBatchStore{Store: s, before: func() {}, inspect: func(b storage.MemoryBatch) {
				sourceGuard, targetGuard := false, false
				for _, g := range b.Guards {
					sourceGuard = sourceGuard || (g.Expected.ID == ep.ID && g.PendingEpisode)
					targetGuard = targetGuard || g.Expected.ID == exact.ID
				}
				if !sourceGuard || !targetGuard || len(b.Writes) != 1 || b.Writes[0].Memory.ID != ep.ID ||
					!b.Writes[0].PreserveEmbedding || len(b.Edges) != 0 || len(b.CloseEdgesFor) != 0 {
					t.Error("exact NOOP must atomically guard source and exact target, stamping only the source")
				}
			}}
			report, err := Consolidate(wrapped, emb, cfg, "test", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if report.Actions[string(ActionNoop)] != 1 || report.Actions[string(ActionAdd)] != 0 || report.PromotedToSemantic != 0 {
				t.Fatalf("exact live match must NOOP independently of vector rank: %+v", report)
			}
			memories, err := s.IterMemories("test", string(schema.LayerSemantic), "")
			if err != nil {
				t.Fatal(err)
			}
			if len(memories) != tc.decoys+1 {
				t.Fatalf("NOOP published a duplicate: %d semantic memories", len(memories))
			}
			got, err := s.GetMemory(exact.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Content != "candidate" || IsRetired(got) || got.MetaString("knowledge_status") != "verified" ||
				got.MetaString("marker") != "exact" || got.UpdatedAt != exact.UpdatedAt {
				t.Error("NOOP changed exact evidence or its metadata")
			}
			stamped, err := s.GetMemory(ep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !IsConsolidated(stamped) || stamped.MetaString("knowledge_status") != "verified" || stamped.MetaString("marker") != "episode" {
				t.Error("NOOP did not preserve and stamp the source episode")
			}
		})
	}
}

func TestConsolidateExactLookupExcludesIneligible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		layer schema.Layer
		typ   schema.MemoryType
		ws    string
		meta  map[string]any
	}{
		{"retired", schema.LayerSemantic, schema.TypeFact, "test", map[string]any{"superseded": true}},
		{"replaced", schema.LayerSemantic, schema.TypeFact, "test", map[string]any{"superseded_by": "replacement"}},
		{"stale", schema.LayerSemantic, schema.TypeNote, "test", map[string]any{"knowledge_status": "stale"}},
		{"rejected", schema.LayerSemantic, schema.TypeFact, "test", map[string]any{"knowledge_status": "rejected"}},
		{"missing evidence", schema.LayerSemantic, schema.TypeFact, "test", map[string]any{"evidence_missing": true}},
		{"code", schema.LayerSemantic, schema.TypeCodeFile, "test", nil},
		{"resource", schema.LayerSemantic, schema.TypeResource, "test", nil},
		{"other workspace", schema.LayerSemantic, schema.TypeFact, "other", nil},
		{"other layer", schema.LayerProcedural, schema.TypeNote, "test", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, emb := trustedStore(t)
			old := trustedMemory(t, s, tc.layer, tc.typ, "candidate", tc.ws, tc.meta)
			if err := s.PutMemory(old, nil); err != nil {
				t.Fatal(err)
			}
			ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
			report, err := Consolidate(s, emb, config.Default(), "test", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if report.Actions[string(ActionAdd)] != 1 || report.Actions[string(ActionNoop)] != 0 || report.PromotedToSemantic != 1 {
				t.Fatalf("ineligible exact match must not suppress ADD: %+v", report)
			}
			got, err := s.GetMemory(old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Content != old.Content || got.UpdatedAt != old.UpdatedAt || IsRetired(got) != IsRetired(old) ||
				got.MetaString("knowledge_status") != old.MetaString("knowledge_status") || got.Meta("evidence_missing") != old.Meta("evidence_missing") {
				t.Error("ADD changed ineligible evidence")
			}
			stamped, err := s.GetMemory(ep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !IsConsolidated(stamped) {
				t.Error("ADD did not stamp the source episode")
			}
		})
	}
}

func TestConsolidateExactNoopAtomicConflict(t *testing.T) {
	for _, fault := range []string{"source edited", "target edited", "target retired", "target deleted"} {
		t.Run(fault, func(t *testing.T) {
			s, emb := trustedStore(t)
			exact := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "candidate", "test", nil)
			if err := s.PutMemory(exact, nil); err != nil {
				t.Fatal(err)
			}
			trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "decoy", "test", nil)
			ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
			wrapped := &beforeBatchStore{Store: s, before: func() {
				var err error
				switch fault {
				case "source edited":
					err = s.UpdateMemoryContent(ep.ID, "edited", "", nil, nil, 0)
				case "target edited":
					err = s.UpdateMemoryContent(exact.ID, "edited", "", nil, nil, 0)
				case "target retired":
					retired := exact.Clone()
					retired.Metadata["superseded"] = true
					err = s.PutMemory(retired, nil)
				case "target deleted":
					err = s.DeleteMemory(exact.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			_, err := Consolidate(wrapped, emb, config.Default(), "test", nil, 0)
			if !errors.Is(err, storage.ErrMemoryConflict) {
				t.Errorf("exact NOOP must conflict after %s: %v", fault, err)
			}
			got, err := s.GetMemory(ep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if IsConsolidated(got) {
				t.Error("failed exact NOOP stamped the source episode")
			}
			facts, err := s.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
			if err != nil {
				t.Fatal(err)
			}
			for _, fact := range facts {
				if fact.ID != exact.ID && fact.Content == "candidate" {
					t.Error("failed exact NOOP published a duplicate")
				}
			}
		})
	}
}

func TestConsolidateInvalidVerdictPending(t *testing.T) {
	for _, action := range []Action{"BOGUS", ActionUpdate, ActionDelete, ActionNoop} {
		t.Run(string(action), func(t *testing.T) {
			s, emb := trustedStore(t)
			ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
			_, err := Consolidate(s, emb, config.Default(), "test", func(string, []string) (Action, string, error) {
				return action, "", nil
			}, 0)
			if err == nil {
				t.Errorf("invalid verdict %q silently accepted", action)
			}
			got, _ := s.GetMemory(ep.ID)
			if IsConsolidated(got) {
				t.Error("invalid verdict stamped episode")
			}
		})
	}
}

func TestConsolidateAtomicSourceConflict(t *testing.T) {
	t.Run("two classifiers", func(t *testing.T) {
		s, emb := trustedStore(t)
		trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		classify := func(string, []string) (Action, string, error) {
			arrived <- struct{}{}
			<-release
			return ActionAdd, "", nil
		}
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				_, err := Consolidate(s, emb, config.Default(), "test", classify, 0)
				errs <- err
			})
		}
		<-arrived
		<-arrived
		close(release)
		wg.Wait()
		a, b := <-errs, <-errs
		if !((a == nil && errors.Is(b, storage.ErrMemoryConflict)) || (b == nil && errors.Is(a, storage.ErrMemoryConflict))) {
			t.Errorf("one winner required: %v / %v", a, b)
		}
		facts, _ := s.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
		if len(facts) != 1 {
			t.Errorf("published %d facts", len(facts))
		}
	})
	t.Run("source edited", func(t *testing.T) {
		s, emb := trustedStore(t)
		ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
		_, err := Consolidate(s, emb, config.Default(), "test", func(string, []string) (Action, string, error) {
			return ActionAdd, "", s.UpdateMemoryContent(ep.ID, "edited", "", nil, []float32{1, 0}, 0)
		}, 0)
		if !errors.Is(err, storage.ErrMemoryConflict) {
			t.Errorf("source edit should conflict: %v", err)
		}
		facts, _ := s.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
		if len(facts) != 0 {
			t.Error("unsupported fact published")
		}
	})
	t.Run("SQL edge fault", func(t *testing.T) {
		s, emb := trustedStore(t)
		ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "candidate", "test", nil)
		trustedSQLFault(t, s)
		_, err := Consolidate(s, emb, config.Default(), "test", nil, 0)
		if err == nil {
			t.Fatal("edge fault not propagated")
		}
		facts, _ := s.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
		got, _ := s.GetMemory(ep.ID)
		if len(facts) != 0 || IsConsolidated(got) {
			t.Fatal("edge fault left partial publication")
		}
	})
}

func trustedSQLFault(t *testing.T, s storage.Store) {
	t.Helper()
	// The fixture store owns this path; a second connection installs a real
	// SQLite constraint fault after writes, before provenance publication.
	db, err := sql.Open("sqlite", s.(trustedFixtureStore).path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TRIGGER trusted_edge_fault BEFORE INSERT ON edges BEGIN SELECT RAISE(ABORT, 'FOREIGN KEY constraint failed'); END"); err != nil {
		t.Fatal(err)
	}
}

func TestConsolidateUpdateKeepsAllSources(t *testing.T) {
	s, emb := trustedStore(t)
	oldSource := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "old evidence", "test", map[string]any{"consolidated_at": 1.0})
	target := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "old fact", "test", map[string]any{"source_episode": oldSource.ID})
	trustedEdge(t, s, target.ID, "produced_from", oldSource.ID)
	newSource := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "new evidence", "test", nil)
	_, err := Consolidate(s, emb, config.Default(), "test", func(string, []string) (Action, string, error) {
		return ActionUpdate, "updated fact", nil
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := s.GetMemory(target.ID)
	updated, _ := s.GetMemory(old.MetaString("superseded_by"))
	if updated == nil {
		t.Fatal("updated fact missing")
	}
	edges, _ := s.Neighbors(updated.ID, "produced_from")
	var ids []string
	for _, edge := range edges {
		if edge.SrcID == updated.ID && edge.ValidTo == nil {
			ids = append(ids, edge.DstID)
		}
	}
	if len(ids) != 2 || !slices.Contains(ids, oldSource.ID) || !slices.Contains(ids, newSource.ID) {
		t.Errorf("lost sources: %v", ids)
	}
	encoded := fmt.Sprint(updated.Metadata["source_episodes"])
	if !strings.Contains(encoded, oldSource.ID) || !strings.Contains(encoded, newSource.ID) {
		t.Errorf("lost source metadata: %v", updated.Metadata)
	}
}

func TestDecayProtectedLegacyAndDeletion(t *testing.T) {
	s, _ := trustedStore(t)
	ep := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "evidence", "test", nil)
	old := ep.Clone()
	old.LastAccessAt = 1
	if err := s.PutMemory(old, []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	fact := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "dependent", "test", map[string]any{"source_episode": ep.ID})
	unreferenced := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "unreferenced", "test", nil, func(m *schema.Memory) { m.LastAccessAt = 1 })
	dry, err := Decay(s, "test", nil, 1, 1, schema.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := Decay(s, "test", nil, 1, 1, schema.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Forgotten != 1 || actual.Forgotten != 1 || dry.Protected != 1 || actual.Protected != 1 ||
		!slices.Equal(dry.ForgottenIDs, actual.ForgottenIDs) || !slices.Contains(actual.ForgottenIDs, unreferenced.ID) {
		t.Fatalf("protected legacy source forgotten: dry=%+v actual=%+v", dry, actual)
	}
	if err := s.DeleteMemory(ep.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMemory(fact.ID)
	if schema.IsUsable(got) {
		t.Fatal("explicit deletion left dependent usable")
	}
}

type decayInterveningStore struct {
	storage.Store
	before func()
}

func (s decayInterveningStore) DeleteMemorySafely(id string, cutoff float64) (bool, bool, error) {
	s.before()
	return s.Store.DeleteMemorySafely(id, cutoff)
}

func TestDecayProtectedRechecksAccessAndReferences(t *testing.T) {
	for _, kind := range []string{"access", "reference"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := trustedStore(t)
			m := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "old event", "test", nil, func(m *schema.Memory) { m.LastAccessAt = 1 })
			wrapped := decayInterveningStore{Store: s, before: func() {
				if kind == "access" {
					if err := s.TouchMemories([]string{m.ID}, 9500); err != nil {
						t.Error(err)
					}
				} else {
					trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "new dependent", "test", map[string]any{"source_episode": m.ID})
				}
			}}
			weights := config.Default().Activation
			weights.RecencyHalfLifeS = 1000
			report, err := Decay(wrapped, "test", &weights, 1, 0.2, 10000, false)
			if err != nil {
				t.Fatal(err)
			}
			if report.Forgotten != 0 {
				t.Errorf("race outcome inflated: %+v", report)
			}
			if kind == "reference" && report.Protected != 1 {
				t.Errorf("new reference not protected: %+v", report)
			}
			if got, _ := s.GetMemory(m.ID); got == nil {
				t.Fatal("concurrently protected event deleted")
			}
		})
	}
}

func TestPlaybookProvenanceAndHookVerification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		meta   map[string]any
		hook   bool
		count  int
		status string
	}{
		{"legacy", map[string]any{"outcome": "success", "action": "deploy"}, false, 1, "inferred"},
		{"tool only", map[string]any{"outcome": "success", "action": "deploy", "knowledge_status": "observed"}, true, 0, ""},
		{"hook metadata only", map[string]any{"outcome": "success", "action": "deploy", "hook_event_name": "PostToolUse", "knowledge_status": "observed"}, false, 0, ""},
		{"verified task", map[string]any{"outcome": "success", "action": "deploy", "task_outcome": "success", "knowledge_status": "verified"}, true, 1, "verified"},
		{"SDK task only", map[string]any{"action": "deploy", "task_outcome": "success", "knowledge_status": "verified"}, false, 1, "verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, emb := trustedStore(t)
			var sources []string
			for range 3 {
				m := trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, "deploy", "test", tc.meta)
				if tc.hook {
					m.Tags = []string{"hook"}
					if err := s.PutMemory(m, []float32{1, 0}); err != nil {
						t.Fatal(err)
					}
				}
				sources = append(sources, m.ID)
			}
			_, err := Proceduralize(s, emb, config.Default(), "test", 3, 0.5)
			if err != nil {
				t.Fatal(err)
			}
			playbooks, _ := s.IterMemories("test", string(schema.LayerProcedural), string(schema.TypePlaybook))
			if len(playbooks) != tc.count {
				t.Fatalf("playbooks=%d, want %d", len(playbooks), tc.count)
			}
			if tc.count == 0 {
				return
			}
			p := playbooks[0]
			if p.MetaString("knowledge_status") != tc.status {
				t.Errorf("status=%q, want %q", p.MetaString("knowledge_status"), tc.status)
			}
			edges, _ := s.Neighbors(p.ID, "produced_from")
			if len(edges) != len(sources) {
				t.Errorf("provenance=%d, want all %d", len(edges), len(sources))
			}
		})
	}
}

type trustedLLM struct {
	run     func() (map[string]any, error)
	observe func([]providers.Message)
}

func (l trustedLLM) Name() string                                 { return "trusted-test" }
func (l trustedLLM) Complete([]providers.Message) (string, error) { return "", nil }
func (l trustedLLM) CompleteStructured(messages []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
	if l.observe != nil {
		l.observe(messages)
	}
	return l.run()
}
func (l trustedLLM) Close() error { return nil }

type beforeBatchStore struct {
	storage.Store
	before  func()
	inspect func(storage.MemoryBatch)
	once    sync.Once
}

func (s *beforeBatchStore) ApplyMemoryBatch(b storage.MemoryBatch) error {
	if s.inspect != nil {
		s.inspect(b)
	}
	s.once.Do(s.before)
	return s.Store.ApplyMemoryBatch(b)
}

func TestDerivedModelAndPlaybookAtomic(t *testing.T) {
	for _, kind := range []string{"extract", "merge", "playbook", "playbook update"} {
		for _, fault := range []string{"deleted source", "edited source", "SQL edge fault"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				s, emb := trustedStore(t)
				cfg := config.Default()
				cfg.System2.L5MinClusterSize = 2
				cfg.System2.L5MergeEveryNCycles = 1
				var sources []*schema.Memory
				layer, typ := schema.LayerSemantic, schema.TypeFact
				if strings.HasPrefix(kind, "playbook") {
					layer, typ = schema.LayerEpisodic, schema.TypeEvent
				}
				for range 3 {
					sources = append(sources, trustedMemory(t, s, layer, typ, "deploy", "test", map[string]any{"outcome": "success", "action": "deploy"}))
				}
				var olds []*schema.Memory
				if kind == "merge" {
					for range 2 {
						m := trustedMemory(t, s, schema.LayerL5Mental, schema.TypeMentalModel, "old model", "test", nil)
						for _, source := range sources {
							trustedEdge(t, s, m.ID, "abstracts", source.ID)
						}
						olds = append(olds, m)
					}
				}
				if kind == "playbook update" {
					olds = append(olds, trustedMemory(t, s, schema.LayerProcedural, schema.TypePlaybook, "old playbook", "test", nil))
				}
				if fault == "SQL edge fault" {
					trustedSQLFault(t, s)
				}
				remove := func() {
					if fault == "deleted source" {
						if err := s.DeleteMemory(sources[0].ID); err != nil {
							t.Error(err)
						}
					}
					if fault == "edited source" {
						if err := s.UpdateMemoryContent(sources[0].ID, "edited", "", nil, []float32{1, 0}, 0); err != nil {
							t.Error(err)
						}
					}
				}
				wrapped := &beforeBatchStore{Store: s, before: remove, inspect: func(batch storage.MemoryBatch) {
					guards := map[string]bool{}
					for _, guard := range batch.Guards {
						guards[guard.Expected.ID] = true
					}
					for _, source := range sources {
						if !guards[source.ID] {
							t.Errorf("missing source guard %s", source.ID)
						}
					}
					for _, old := range olds {
						if !guards[old.ID] {
							t.Errorf("missing retirement guard %s", old.ID)
						}
					}
					if len(batch.Writes) != len(olds)+1 {
						t.Errorf("writes=%d, want derived item and every retirement", len(batch.Writes))
					}
					if len(batch.Edges) < len(sources) {
						t.Errorf("missing staged provenance: %d", len(batch.Edges))
					}
				}}
				llm := trustedLLM{run: func() (map[string]any, error) {
					if fault == "deleted source" || fault == "edited source" {
						remove()
					}
					return map[string]any{"title": "new model", "model": "deploy safely"}, nil
				}}
				var err error
				if strings.HasPrefix(kind, "playbook") {
					_, err = Proceduralize(wrapped, emb, cfg, "test", 3, 0.5)
				} else {
					_, err = ExtractL5(wrapped, emb, cfg, "test", llm, "")
				}
				if err == nil {
					t.Error("publication fault swallowed")
				}
				for _, old := range olds {
					got, _ := s.GetMemory(old.ID)
					if IsRetired(got) {
						t.Error("premature retirement")
					}
				}
				resultLayer := schema.LayerL5Mental
				if strings.HasPrefix(kind, "playbook") {
					resultLayer = schema.LayerProcedural
				}
				results, _ := s.IterMemories("test", string(resultLayer), "")
				oldIDs := map[string]bool{}
				for _, old := range olds {
					oldIDs[old.ID] = true
				}
				for _, result := range results {
					if !oldIDs[result.ID] {
						t.Errorf("partial derived publication: %s", result.ID)
					}
				}
			})
		}
	}
}

func TestSystem2WorkspaceCursor(t *testing.T) {
	s, emb := trustedStore(t)
	cfg := config.Default()
	cfg.System2.L6MaxEpisodes = 2
	cfg.System2.L5MergeEveryNCycles = 100
	for _, ws := range []string{"A", "B"} {
		for i := range 5 {
			trustedMemory(t, s, schema.LayerEpisodic, schema.TypeEvent, fmt.Sprintf("%s-%d", ws, i), ws, nil, func(m *schema.Memory) { m.CreatedAt = 123.123456789 })
		}
	}
	if err := s.SetMeta(l6Watermark, "9999999999"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("l5_merge_cycle_count", "88"); err != nil {
		t.Fatal(err)
	}
	var consumed []string
	llm := trustedLLM{
		run: func() (map[string]any, error) { return map[string]any{"intents": []any{}}, nil },
		observe: func(messages []providers.Message) {
			for _, message := range messages {
				if message.Role == "user" {
					for line := range strings.SplitSeq(message.Content, "\n") {
						if strings.HasPrefix(line, "- ") {
							consumed = append(consumed, strings.TrimPrefix(line, "- "))
						}
					}
				}
			}
		},
	}
	for _, ws := range []string{"A", "B"} {
		consumed = nil
		episodes, _ := s.IterMemories(ws, string(schema.LayerEpisodic), "")
		slices.SortFunc(episodes, func(a, b *schema.Memory) int { return strings.Compare(a.ID, b.ID) })
		var expected []string
		for _, ep := range episodes {
			expected = append(expected, ep.Content)
		}
		var counts []int
		for i := range 4 {
			report, err := PredictL6(s, emb, cfg, ws, llm, "")
			if err != nil {
				t.Fatal(err)
			}
			counts = append(counts, report.EpisodesSeen)
			if i == 0 {
				fixture := s.(trustedFixtureStore)
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := storage.NewStore(fixture.path, 2, false, false)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { reopened.Close() })
				s = trustedFixtureStore{Store: reopened, path: fixture.path}
			}
		}
		if !slices.Equal(counts, []int{2, 2, 1, 0}) {
			t.Errorf("%s cursor counts=%v", ws, counts)
		}
		if !slices.Equal(consumed, expected) {
			t.Errorf("%s episodes consumed out of order or more than once: %v != %v", ws, consumed, expected)
		}
		raw, _ := s.GetMeta(workspaceMetaKey("l6_episode_cursor", ws))
		var cursor episodeCursor
		if err := json.Unmarshal([]byte(raw), &cursor); err != nil || cursor.Timestamp != 123.123456789 || cursor.ID != episodes[len(episodes)-1].ID {
			t.Errorf("lossy cursor %q (%v)", raw, err)
		}
	}
	modelLLM := trustedLLM{run: func() (map[string]any, error) { return map[string]any{"title": "model", "model": "body"}, nil }}
	for _, ws := range []string{"A", "B"} {
		if _, err := ExtractL5(s, emb, cfg, ws, modelLLM, ""); err != nil {
			t.Fatal(err)
		}
	}
	keys := []string{}
	// The key is an operation prefix plus stable workspace hash.
	for _, ws := range []string{"A", "B"} {
		keys = append(keys, "l5_merge_cycle_count:"+schema.ContentHash(ws))
	}
	for _, key := range keys {
		value, err := s.GetMeta(key)
		if err != nil || value != "1" {
			t.Errorf("independent counter %s=%q (%v)", key, value, err)
		}
		for key, want := range map[string]string{l6Watermark: "9999999999", "l5_merge_cycle_count": "88"} {
			if got, _ := s.GetMeta(key); got != want {
				t.Errorf("legacy global key %s changed: %q", key, got)
			}
		}
	}
	fail := errors.New("provider unavailable")
	trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "one", "C", nil)
	trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "two", "C", nil)
	cfg.System2.L5MinClusterSize = 2
	if _, err := ExtractL5(s, emb, cfg, "C", trustedLLM{run: func() (map[string]any, error) { return nil, fail }}, ""); !errors.Is(err, fail) {
		t.Errorf("L5 provider error swallowed: %v", err)
	}
}
