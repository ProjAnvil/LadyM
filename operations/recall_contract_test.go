//go:build !enterprise

package operations

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func TestRecallHonestTier2AndTokens(t *testing.T) {
	s, emb := trustedStore(t)
	cfg := config.Default()
	cfg.Recall.ReflectionMinHits = 1
	cfg.Recall.ReflectionMinCoverage = 1
	m := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "concatenate", "test", nil)
	hits := []*schema.RecallResult{{Memory: m}}
	if v := reflect("cat", hits, cfg.Recall); v.sufficient || v.coverage != 0 {
		t.Errorf("substring counted as token coverage: %+v", v)
	}
	resp, err := Recall(s, emb, "absent", cfg, "test", 10, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 2 || resp.ReflectedSufficient {
		t.Fatalf("tier2 cannot manufacture coverage: %+v", resp)
	}
	cfg.Recall.ReflectionMinHits = 2
	resp, err = Recall(s, emb, "concatenate", cfg, "test", 10, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ReflectedSufficient {
		t.Error("tier2 ignored the returned hit count")
	}
}

func TestRecallExpansionBudgetAndScope(t *testing.T) {
	s, _ := trustedStore(t)
	cfg := config.Default()
	cfg.Recall.GraphHops = 3
	anchor := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "anchor", "test", nil)
	for i := range 160 {
		m := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, fmt.Sprintf("neighbor-%03d", i), "test", nil)
		trustedEdge(t, s, anchor.ID, "related", m.ID)
	}

	for _, status := range []string{"stale", "rejected", "retired", "foreign"} {
		meta := map[string]any{"knowledge_status": status}
		ws := "test"
		if status == "retired" {
			meta["superseded"] = true
		}
		if status == "foreign" {
			ws = "other"
		}
		bridge := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, status, ws, meta)
		leaf := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "hidden-"+status, "test", nil)
		trustedEdge(t, s, anchor.ID, "related", bridge.ID)
		trustedEdge(t, s, bridge.ID, "related", leaf.ID)
	}
	run := func() []string {
		t.Helper()
		items, err := tier2Expand(s, []*schema.RecallResult{{Memory: anchor, Score: 1}}, cfg, "test")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 128 {
			t.Errorf("expanded %d nodes, budget 128", len(items))
		}
		ids := make([]string, 0, len(items))
		for _, item := range items {
			if !schema.IsUsable(item.Memory) || item.Memory.Workspace != "test" ||
				!strings.HasPrefix(item.Memory.Content, "neighbor") {
				t.Errorf("ineligible node or bridge: %+v", item.Memory)
			}
			ids = append(ids, item.Memory.ID)
		}
		return ids
	}
	if a, b := run(), run(); !slices.Equal(a, b) {
		t.Fatalf("unstable expansion: %v != %v", a, b)
	}
}

type failingSearchStore struct{ storage.Store }

func (s failingSearchStore) SearchMemories([]float32, int, storage.MemoryFilter) ([]storage.SearchHit, error) {
	return nil, fmt.Errorf("search unavailable")
}

func TestRecallSearchErrors(t *testing.T) {
	s, emb := trustedStore(t)
	if _, err := Recall(failingSearchStore{s}, emb, "cat", config.Default(), "test", 10, nil, nil, 0); err == nil {
		t.Fatal("search error swallowed")
	}
}

type expansionCountingStore struct {
	storage.Store
	memoryReads int
	fileScans   int
}

func (s *expansionCountingStore) GetMemory(id string) (*schema.Memory, error) {
	s.memoryReads++
	return s.Store.GetMemory(id)
}

func (s *expansionCountingStore) IterMemories(ws, layer, typ string) ([]*schema.Memory, error) {
	if typ == string(schema.TypeCodeFile) {
		s.fileScans++
	}
	return s.Store.IterMemories(ws, layer, typ)
}

func TestRecallExpansionConfiguredBudgetAndFileMap(t *testing.T) {
	s, _ := trustedStore(t)
	cfg := config.Default()
	symbols := []*schema.RecallResult{}
	for i := range 2 {
		m := trustedMemory(t, s, schema.LayerSemantic, schema.TypeCodeSymbol, fmt.Sprintf("symbol%d", i), "test", map[string]any{"file_path": fmt.Sprintf("file%d.go", i)})
		symbols = append(symbols, &schema.RecallResult{Memory: m, Score: 1})
		trustedMemory(t, s, schema.LayerSemantic, schema.TypeCodeFile, fmt.Sprintf("file%d", i), "test", map[string]any{"file_path": fmt.Sprintf("file%d.go", i)})
	}
	counted := &expansionCountingStore{Store: s}
	items, err := tier2Expand(counted, symbols, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || counted.fileScans != 1 || counted.memoryReads != 0 {
		t.Errorf("backtracking: items=%d scans=%d reads=%d", len(items), counted.fileScans, counted.memoryReads)
	}
	cfg.Recall.MaxExpandedNodes = 1
	items, err = tier2Expand(s, symbols, cfg, "test")
	if err != nil || len(items) != 1 {
		t.Errorf("configured budget: items=%d err=%v", len(items), err)
	}
	cfg.Recall.MaxExpandedNodes = 0
	items, err = tier2Expand(s, symbols, cfg, "test")
	if err != nil || len(items) != 2 {
		t.Errorf("programmatic zero default: items=%d err=%v", len(items), err)
	}
	cfg.Recall.MaxExpandedNodes = -1
	if _, err := Recall(s, trustedEmbedding{}, "symbol", cfg, "test", 10, nil, nil, 0); err == nil {
		t.Error("negative node budget accepted")
	}
}

func TestRecallExpansionFiltersPreventBridging(t *testing.T) {
	s, _ := trustedStore(t)
	cfg := config.Default()
	anchor := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "anchor", "test", nil)
	for _, kind := range []string{"retired", "stale", "foreign", "wrong type", "wrong layer", "closed edge"} {
		ws, layer, typ := "test", schema.LayerSemantic, schema.TypeFact
		meta := map[string]any{}
		switch kind {
		case "retired":
			meta["superseded"] = true
		case "stale":
			meta["knowledge_status"] = "stale"
		case "foreign":
			ws = "foreign"
		case "wrong type":
			typ = schema.TypeNote
		case "wrong layer":
			layer = schema.LayerEpisodic
		}
		bridge := trustedMemory(t, s, layer, typ, kind, ws, meta)
		leaf := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "hidden "+kind, "test", nil)
		e := schema.NewEdge(anchor.ID, "related", bridge.ID)
		if kind == "closed edge" {
			e.ValidTo = new(schema.Now())
		}
		if err := s.PutEdge(e); err != nil {
			t.Fatal(err)
		}
		trustedEdge(t, s, bridge.ID, "related", leaf.ID)
	}
	allowed := trustedMemory(t, s, schema.LayerSemantic, schema.TypeFact, "allowed", "test", nil)
	trustedEdge(t, s, anchor.ID, "related", allowed.ID)
	filter := storage.MemoryFilter{Workspace: "test", Layers: []schema.Layer{schema.LayerSemantic}, Types: []schema.MemoryType{schema.TypeFact}, ExcludeUnusable: true}
	items, err := tier2Expand(s, []*schema.RecallResult{{Memory: anchor, Score: 1}}, cfg, "test", filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Memory.ID != allowed.ID {
		t.Errorf("crossed an ineligible bridge: %+v", items)
	}
}
