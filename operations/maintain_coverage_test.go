//go:build !enterprise

package operations

// Coverage-gap tests for maintain.go (Task 22): the JSON-tolerance helpers,
// mergeWinner's tie branches, stampActionClass's nil-metadata and embed-error
// paths, MaintainL4's disabled/gate/error returns, and mergeResourcePair's
// malformed/edge-skip/error branches. Store errors are injected with a stub
// wrapping the real store (same pattern as engine/store_error_branches_test.go).

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	_ "modernc.org/sqlite"
)

// opsStubStore delegates every Store method to the wrapped store except the
// ones a test has overridden with a hook.
type opsStubStore struct {
	storage.Store
	sweepDanglingEdges func() (int, error)
	iterMemories       func(workspace, layer, typ string) ([]*schema.Memory, error)
	putMemory          func(mem *schema.Memory, vector []float32) error
	neighbors          func(memID, relation string) ([]*schema.Edge, error)
	putEdge            func(e *schema.Edge) error
	getMemory          func(id string) (*schema.Memory, error)
}

func (s *opsStubStore) SweepDanglingEdges() (int, error) {
	if s.sweepDanglingEdges != nil {
		return s.sweepDanglingEdges()
	}
	return s.Store.SweepDanglingEdges()
}

func (s *opsStubStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	if s.iterMemories != nil {
		return s.iterMemories(workspace, layer, typ)
	}
	return s.Store.IterMemories(workspace, layer, typ)
}

func (s *opsStubStore) PutMemory(mem *schema.Memory, vector []float32) error {
	if s.putMemory != nil {
		return s.putMemory(mem, vector)
	}
	return s.Store.PutMemory(mem, vector)
}

func (s *opsStubStore) Neighbors(memID, relation string) ([]*schema.Edge, error) {
	if s.neighbors != nil {
		return s.neighbors(memID, relation)
	}
	return s.Store.Neighbors(memID, relation)
}

func (s *opsStubStore) PutEdge(e *schema.Edge) error {
	if s.putEdge != nil {
		return s.putEdge(e)
	}
	return s.Store.PutEdge(e)
}

func (s *opsStubStore) GetMemory(id string) (*schema.Memory, error) {
	if s.getMemory != nil {
		return s.getMemory(id)
	}
	return s.Store.GetMemory(id)
}

// selectEmbedder fails Embed for one exact content, everything else delegates.
type selectEmbedder struct {
	storage.EmbeddingProvider
	failContent string
}

func (e *selectEmbedder) Embed(text string) ([]float32, error) {
	if text == e.failContent {
		return nil, errors.New("embed boom")
	}
	return e.EmbeddingProvider.Embed(text)
}

// --- pure helpers ---------------------------------------------------------------

func TestMergeWinnerOrderingRules(t *testing.T) {
	older := &schema.Memory{ID: "zz", CreatedAt: 1}
	newer := &schema.Memory{ID: "aa", CreatedAt: 2}

	if w, _ := mergeWinner(older, newer); w != older {
		t.Errorf("mergeWinner(older, newer) winner = %v, want older", w.ID)
	}
	if _, l := mergeWinner(older, newer); l != newer {
		t.Errorf("mergeWinner(older, newer) loser = %v, want newer", l.ID)
	}
	if w, _ := mergeWinner(newer, older); w != older {
		t.Errorf("mergeWinner(newer, older) winner = %v, want older", w.ID)
	}

	// CreatedAt tie: smaller ID wins regardless of argument order.
	ta := &schema.Memory{ID: "aa", CreatedAt: 5}
	tb := &schema.Memory{ID: "bb", CreatedAt: 5}
	if w, _ := mergeWinner(ta, tb); w != ta {
		t.Errorf("tie winner = %v, want aa", w.ID)
	}
	if w, _ := mergeWinner(tb, ta); w != ta {
		t.Errorf("tie winner (reversed args) = %v, want aa", w.ID)
	}
}

func TestAsBoolDefaultToleratesJSONShapes(t *testing.T) {
	for _, c := range []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"true", true},
		{"yes", true},
		{"1", true},
		{"false", false},
		{"no", false},
		{"0", false},
		{2.5, true}, // float64 != 0
		{float64(0), false},
		{"maybe", false}, // unrecognized string -> default
		{nil, false},
		{42, false}, // ints are NOT coerced (encoding/json yields float64)
	} {
		if got := asBoolDefault(c.in, false); got != c.want {
			t.Errorf("asBoolDefault(%#v, false) = %v, want %v", c.in, got, c.want)
		}
	}
	// The default kicks in for every unrecognized shape.
	if asBoolDefault("maybe", true) != true {
		t.Error(`asBoolDefault("maybe", true) = false, want default true`)
	}
}

func TestAsFloatOkToleratesJSONShapes(t *testing.T) {
	for _, c := range []struct {
		in    any
		want  float64
		wantO bool
	}{
		{1.5, 1.5, true},
		{int(2), 2, true},
		{int64(3), 3, true},
		{"1.5", 1.5, true},
		{"", 0, false},
		// ok reports "a value was present", not "it parsed": an unparseable
		// non-empty string yields zero weight but ok=true.
		{"not-a-number", 0, true},
		{nil, 0, false},
		{true, 0, false}, // unhandled type -> default
	} {
		got, ok := asFloatOk(c.in)
		if ok != c.wantO || got != c.want {
			t.Errorf("asFloatOk(%#v) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.wantO)
		}
	}
}

// --- stampActionClass ------------------------------------------------------------

func TestStampActionClassNilMetadata(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	ep := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	ep.Workspace = "test"
	ep.Content = "agent=bot did things"
	// scanMemory never yields nil Metadata, so the nil branch is only
	// reachable with a hand-built memory — exercise the defensive path.
	ep.Metadata = nil
	vec, err := emb.Embed(ep.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(ep, vec); err != nil {
		t.Fatal(err)
	}

	if err := stampActionClass(store, emb, ep, "execute"); err != nil {
		t.Fatal(err)
	}
	if ep.MetaString("action_class") != "execute" {
		t.Errorf("in-memory action_class = %q, want execute", ep.MetaString("action_class"))
	}
	got, err := store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetaString("action_class") != "execute" {
		t.Errorf("persisted action_class = %q, want execute", got.MetaString("action_class"))
	}
	// The rewrite re-embeds: the episode must stay vector-searchable.
	found := false
	for _, hit := range store.VectorSearch(vec, 5) {
		if hit.ID == ep.ID {
			found = true
		}
	}
	if !found {
		t.Error("episode dropped from vector search after stampActionClass rewrite")
	}
}

func TestStampActionClassEmbedErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	ep := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	ep.Workspace = "test"
	ep.Content = "agent=bot did things"
	vec, err := emb.Embed(ep.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(ep, vec); err != nil {
		t.Fatal(err)
	}

	failEmb := &selectEmbedder{EmbeddingProvider: emb, failContent: ep.Content}
	if err := stampActionClass(store, failEmb, ep, "execute"); err == nil {
		t.Fatal("stampActionClass with failing embedder: want error, got nil")
	}
	// Nothing was persisted: the store copy still has no action_class.
	got, err := store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetaString("action_class") != "" {
		t.Errorf("persisted action_class = %q, want empty on embed failure", got.MetaString("action_class"))
	}
}

// --- MaintainL4 gates and error propagation --------------------------------------

func TestMaintainL4DisabledRunsGCOnly(t *testing.T) {
	store, emb, path := maintainTestStore(t)
	// One backfill candidate (heuristic would stamp it) plus a dangling edge
	// that the always-on GC must still sweep. The endpoint row is dropped on a
	// pinned FK-off connection (same legacy-pre-FK trick as the sweep test).
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=run", map[string]any{"action": "run"})
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "survivor", nil)
	if err := store.PutEdge(schema.NewEdge(ep.ID, "related_to", b.ID)); err != nil {
		t.Fatal(err)
	}
	side, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { side.Close() })
	conn, err := side.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "DELETE FROM memories WHERE id = ?", ep.ID); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Maintain.Enabled = false
	rep, err := MaintainL4(store, emb, cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Swept != 1 {
		t.Errorf("rep.Swept = %d, want 1 (GC runs even when maintain is disabled)", rep.Swept)
	}
	if rep.Skipped != "maintain_disabled" {
		t.Errorf("rep.Skipped = %q, want maintain_disabled", rep.Skipped)
	}
	if rep.Backfilled != 0 || rep.Linked != 0 || rep.Merged != 0 {
		t.Errorf("sub-steps ran while disabled: %+v", rep)
	}
	// The run candidate was left alone: no action_class was stamped.
	eps, err := store.IterMemories("test", string(schema.LayerEpisodic), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range eps {
		if m.MetaString("action_class") != "" {
			t.Errorf("episode got action_class %q while maintain was disabled", m.MetaString("action_class"))
		}
	}
}

func TestMaintainL4SweepErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	boom := errors.New("sweep boom")
	stub := &opsStubStore{Store: store, sweepDanglingEdges: func() (int, error) {
		return 0, boom
	}}

	if _, err := MaintainL4(stub, emb, config.Default(), "test", nil); !errors.Is(err, boom) {
		t.Fatalf("MaintainL4 err = %v, want %v", err, boom)
	}
}

func TestMaintainL4BackfillErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	// Heuristic misses ("reorganized"), so the backfill LLM is reached and fails.
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=reorganized", map[string]any{"action": "reorganized"})
	boom := errors.New("backfill boom")
	fake := &providers.FakeLLMProvider{StructuredFn: func([]providers.Message, providers.JSONSchema) (map[string]any, error) {
		return nil, boom
	}}

	if _, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{Backfill: fake}); !errors.Is(err, boom) {
		t.Fatalf("MaintainL4 err = %v, want %v", err, boom)
	}
}

func TestMaintainL4LinkingErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	// Two same-bucket episodes pass the gate; the link LLM then fails.
	linkTestEpisodes(t, store, emb, 2, "deploy the auth service", "deploy")
	cfg := config.Default()
	cfg.System2.MinEpisodesToRun = 2
	boom := errors.New("link boom")
	fake := &providers.FakeLLMProvider{StructuredFn: func([]providers.Message, providers.JSONSchema) (map[string]any, error) {
		return nil, boom
	}}

	if _, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake}); !errors.Is(err, boom) {
		t.Fatalf("MaintainL4 err = %v, want %v", err, boom)
	}
}

func TestMaintainL4AliasMergeErrorPropagates(t *testing.T) {
	store, emb, _, _, _ := aliasMergeFixture(t)
	boom := errors.New("merge boom")
	fake := &providers.FakeLLMProvider{StructuredFn: func([]providers.Message, providers.JSONSchema) (map[string]any, error) {
		return nil, boom
	}}

	if _, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{AliasMerge: fake}); !errors.Is(err, boom) {
		t.Fatalf("MaintainL4 err = %v, want %v", err, boom)
	}
}

// --- mergeResourcePair ------------------------------------------------------------

func TestMergeResourcePairNilWinnerMetadata(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	// Direct pair: winner has nil metadata, loser carries aliases.
	winner := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	winner.Workspace = "test"
	winner.Content = "customer database"
	// scanMemory never yields nil Metadata; the nil branch is defensive, so
	// reach it with a hand-built winner.
	winner.Metadata = nil
	loser := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	loser.Workspace = "test"
	loser.Content = "customer database"
	loser.Metadata = map[string]any{"aliases": []string{"custdb"}}
	for _, m := range []*schema.Memory{winner, loser} {
		vec, err := emb.Embed(m.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
	}

	if err := mergeResourcePair(store, emb, winner, loser); err != nil {
		t.Fatal(err)
	}
	got := resourceAliases(winner)
	if len(got) != 1 || got[0] != "custdb" {
		t.Errorf("winner aliases = %v, want [custdb]", got)
	}
	if !IsRetired(loser) || loser.MetaString("superseded_by") != winner.ID {
		t.Errorf("loser not retired into winner: retired=%v superseded_by=%q", IsRetired(loser), loser.MetaString("superseded_by"))
	}
}

func TestMergeResourcePairSkipsClosedAndReversedEdges(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	winner := putTestResource(t, store, emb, "winner entity", []string{"win"})
	loser := putTestResource(t, store, emb, "loser entity", []string{"lose"})
	other := putTestResource(t, store, emb, "unrelated", nil)

	// ep -> loser: still valid, must be rebuilt to point at the winner.
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=deploy", map[string]any{"action": "deploy"})
	valid := schema.NewEdge(ep.ID, "operates_on", loser.ID)
	valid.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", loser.ID)
	if err := store.PutEdge(valid); err != nil {
		t.Fatal(err)
	}
	// ep2 -> loser: already invalidated; must NOT be rebuilt.
	ep2 := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=migrate", map[string]any{"action": "migrate"})
	closed := schema.NewEdge(ep2.ID, "operates_on", loser.ID)
	closed.ID = schema.ProjectedEdgeID(ep2.ID, "operates_on", loser.ID)
	if err := store.PutEdge(closed); err != nil {
		t.Fatal(err)
	}
	now := schema.Now()
	if err := store.InvalidateEdge(closed.ID, now); err != nil {
		t.Fatal(err)
	}
	// loser -> other: loser is the src side, must NOT be rebuilt to the winner.
	if err := store.PutEdge(schema.NewEdge(loser.ID, "operates_on", other.ID)); err != nil {
		t.Fatal(err)
	}

	if err := mergeResourcePair(store, emb, winner, loser); err != nil {
		t.Fatal(err)
	}

	rebuilt := false
	for _, e := range mustNeighbors(t, store, winner.ID, "operates_on") {
		if e.ValidTo != nil {
			continue
		}
		if e.SrcID == ep.ID && e.DstID == winner.ID &&
			e.ID == schema.ProjectedEdgeID(ep.ID, "operates_on", winner.ID) {
			rebuilt = true
		}
		if e.SrcID == ep2.ID || e.SrcID == loser.ID {
			t.Errorf("invalid or reversed edge was rebuilt onto the winner: %+v", e)
		}
	}
	if !rebuilt {
		t.Error("the loser's valid incoming edge was not rebuilt to point at the winner")
	}
}

func TestMergeResourcePairEmbedErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	winner := putTestResource(t, store, emb, "winner entity", []string{"win"})
	loser := putTestResource(t, store, emb, "loser entity", []string{"lose"})

	failEmb := &selectEmbedder{EmbeddingProvider: emb, failContent: winner.Content}
	err := mergeResourcePair(store, failEmb, winner, loser)
	if err == nil {
		t.Fatal("mergeResourcePair with failing embedder: want error, got nil")
	}
	if IsRetired(loser) {
		t.Error("loser retired despite the merge aborting at the alias rewrite")
	}
}

func TestMergeResourcePairPutMemoryErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	winner := putTestResource(t, store, emb, "winner entity", []string{"win"})
	loser := putTestResource(t, store, emb, "loser entity", []string{"lose"})
	boom := errors.New("put boom")
	stub := &opsStubStore{Store: store, putMemory: func(*schema.Memory, []float32) error {
		return boom
	}}

	if err := mergeResourcePair(stub, emb, winner, loser); !errors.Is(err, boom) {
		t.Fatalf("mergeResourcePair err = %v, want %v", err, boom)
	}
}

func TestMergeResourcePairNeighborsErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	winner := putTestResource(t, store, emb, "winner entity", []string{"win"})
	// Loser without aliases: the alias rewrite is skipped, so the first store
	// call after it is Neighbors, which the stub fails.
	loser := putTestResource(t, store, emb, "loser entity", nil)
	boom := errors.New("neighbors boom")
	stub := &opsStubStore{Store: store, neighbors: func(string, string) ([]*schema.Edge, error) {
		return nil, boom
	}}

	if err := mergeResourcePair(stub, emb, winner, loser); !errors.Is(err, boom) {
		t.Fatalf("mergeResourcePair err = %v, want %v", err, boom)
	}
}

func TestMergeResourcePairEdgeRebuildPutErrorPropagates(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	winner := putTestResource(t, store, emb, "winner entity", nil)
	loser := putTestResource(t, store, emb, "loser entity", nil)
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=deploy", map[string]any{"action": "deploy"})
	if err := store.PutEdge(schema.NewEdge(ep.ID, "operates_on", loser.ID)); err != nil {
		t.Fatal(err)
	}
	// Only the rebuilt edge (dst = the winner) fails; everything before it
	// (no aliases, the Neighbors read) went through the real store.
	boom := errors.New("rebuild boom")
	inner := store
	stub := &opsStubStore{Store: store, putEdge: func(e *schema.Edge) error {
		if e.DstID == winner.ID {
			return boom
		}
		return inner.PutEdge(e)
	}}

	if err := mergeResourcePair(stub, emb, winner, loser); !errors.Is(err, boom) {
		t.Fatalf("mergeResourcePair err = %v, want %v", err, boom)
	}
	if IsRetired(loser) {
		t.Error("loser retired despite the merge aborting at the edge rebuild")
	}
}

func mustNeighbors(t *testing.T, store storage.Store, id, relation string) []*schema.Edge {
	t.Helper()
	edges, err := store.Neighbors(id, relation)
	if err != nil {
		t.Fatal(err)
	}
	return edges
}
