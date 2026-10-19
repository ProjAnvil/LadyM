//go:build !enterprise

package operations

// MaintainL4 unit tests (spec §8.1/§8.2): dangling-edge GC, heuristic slot
// backfill, LLM backfill, nil-LLM safety, and the per-cycle batch cap.

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	_ "modernc.org/sqlite"
)

func maintainTestStore(t *testing.T) (storage.Store, *storage.HashingEmbedding, string) {
	t.Helper()
	emb := storage.NewHashingEmbedding(256)
	path := filepath.Join(t.TempDir(), "maintain.db")
	store, err := storage.NewStore(path, emb.Dim(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, emb, path
}

func TestMaintainL4SweepsDanglingEdges(t *testing.T) {
	store, emb, path := maintainTestStore(t)
	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "dangling endpoint", nil)
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "survivor", nil)
	if err := store.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
		t.Fatal(err)
	}
	// Drop A with the FK off on a pinned side connection (legacy pre-FK db).
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
	defer func() {
		if _, err := conn.ExecContext(t.Context(), "PRAGMA foreign_keys = ON"); err != nil {
			t.Errorf("restore PRAGMA foreign_keys = ON: %v", err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "DELETE FROM memories WHERE id = ?", a.ID); err != nil {
		t.Fatal(err)
	}

	rep, err := MaintainL4(store, emb, config.Default(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Swept != 1 {
		t.Errorf("rep.Swept = %d, want 1", rep.Swept)
	}
	edges, err := store.Neighbors(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("Neighbors(b) = %v, want empty (dangling edge swept)", edges)
	}
}

func TestMaintainL4BackfillHeuristic(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=run", map[string]any{"action": "run"})

	rep, err := MaintainL4(store, emb, config.Default(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backfilled != 1 {
		t.Errorf("rep.Backfilled = %d, want 1", rep.Backfilled)
	}
	got, err := store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetaString("action_class") != "execute" {
		t.Errorf("action_class = %q, want execute (alias run->execute)", got.MetaString("action_class"))
	}
	// Regression guard: the rewrite must re-embed, never NULL the vector.
	vec, err := emb.Embed(ep.Content)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range store.VectorSearch(vec, 5) {
		if hit.ID == ep.ID {
			found = true
		}
	}
	if !found {
		t.Error("episode not found in vector search after backfill rewrite (embedding lost?)")
	}
}

func TestMaintainL4BackfillLLM(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=reorganized", map[string]any{"action": "reorganized"})

	calls := 0
	fake := &providers.FakeLLMProvider{StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"resources": []any{"ladym.db"}, "action": "migrate"}, nil
	}}

	rep, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{Backfill: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backfilled != 1 {
		t.Errorf("rep.Backfilled = %d, want 1", rep.Backfilled)
	}
	if calls != 1 {
		t.Errorf("LLM calls = %d, want 1", calls)
	}
	got, err := store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetaString("action_class") != "migrate" {
		t.Errorf("action_class = %q, want migrate", got.MetaString("action_class"))
	}
	resources, err := store.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Content != "ladym.db" {
		t.Fatalf("resources = %v, want one ladym.db entity", resources)
	}
	edges, err := store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].DstID != resources[0].ID {
		t.Errorf("operates_on edges = %v, want one edge to the ladym.db resource", edges)
	}
	if edges[0].ID != schema.ProjectedEdgeID(ep.ID, "operates_on", resources[0].ID) {
		t.Errorf("edge id = %q, want projected deterministic id", edges[0].ID)
	}
}

func TestMaintainL4BackfillNilLLMAndIdempotent(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=run", map[string]any{"action": "run"})
	// Heuristic can't classify this one; with a nil agent it must be left
	// alone without any panic or error.
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=reorganized", map[string]any{"action": "reorganized"})

	rep, err := MaintainL4(store, emb, config.Default(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backfilled != 1 {
		t.Errorf("rep.Backfilled = %d, want 1 (heuristic only)", rep.Backfilled)
	}
	eps, err := store.IterMemories("test", string(schema.LayerEpisodic), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range eps {
		if m.MetaString("action") == "reorganized" && m.MetaString("action_class") != "" {
			t.Errorf("episode without a heuristic hit got action_class = %q with nil LLM", m.MetaString("action_class"))
		}
	}

	rep2, err := MaintainL4(store, emb, config.Default(), "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Backfilled != 0 {
		t.Errorf("second run rep.Backfilled = %d, want 0 (already backfilled)", rep2.Backfilled)
	}
}

func TestMaintainL4BackfillBatchCap(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	cfg := config.Default()
	if cfg.Maintain.BackfillBatch != 50 {
		t.Fatalf("default BackfillBatch = %d, want 50", cfg.Maintain.BackfillBatch)
	}
	for i := 0; i < 60; i++ {
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
			fmt.Sprintf("agent=bot | action=run | seq=%d", i), map[string]any{"action": "run"})
	}

	rep, err := MaintainL4(store, emb, cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backfilled != 50 {
		t.Errorf("rep.Backfilled = %d, want 50 (batch cap)", rep.Backfilled)
	}
	eps, err := store.IterMemories("test", string(schema.LayerEpisodic), "")
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, m := range eps {
		if m.MetaString("action_class") == "" {
			missing++
		}
	}
	if missing != 10 {
		t.Errorf("episodes without action_class after one cycle = %d, want 10", missing)
	}
}

// --- Task 18: semantic linking (spec §8.3) ---------------------------------------

func linkTestEpisodes(t *testing.T, store storage.Store, emb *storage.HashingEmbedding, count int, content, actionClass string) []*schema.Memory {
	t.Helper()
	var out []*schema.Memory
	for i := 0; i < count; i++ {
		m := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, content,
			map[string]any{"action": actionClass, "action_class": actionClass})
		out = append(out, m)
	}
	return out
}

func linkingFake(t *testing.T) (*providers.FakeLLMProvider, *int) {
	t.Helper()
	calls := 0
	fake := &providers.FakeLLMProvider{StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"link": true, "weight": 0.8}, nil
	}}
	return fake, &calls
}

func TestMaintainL4LinkingDisabledByConfig(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	linkTestEpisodes(t, store, emb, 2, "deploy the auth service", "deploy")
	cfg := config.Default()
	cfg.Maintain.LinkingEnabled = false
	cfg.System2.MinEpisodesToRun = 2
	fake, calls := linkingFake(t)

	rep, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != "linking" {
		t.Errorf("rep.Skipped = %q, want linking", rep.Skipped)
	}
	if *calls != 0 {
		t.Errorf("LLM calls = %d, want 0 (linking disabled)", *calls)
	}
	if rep.Linked != 0 {
		t.Errorf("rep.Linked = %d, want 0", rep.Linked)
	}
}

func TestMaintainL4LinkingSkippedWithoutLLM(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	linkTestEpisodes(t, store, emb, 2, "deploy the auth service", "deploy")
	cfg := config.Default()
	cfg.System2.MinEpisodesToRun = 2

	rep, err := MaintainL4(store, emb, cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != "linking" {
		t.Errorf("rep.Skipped = %q, want linking (no LLM agent)", rep.Skipped)
	}
	if rep.Linked != 0 {
		t.Errorf("rep.Linked = %d, want 0", rep.Linked)
	}
}

func TestMaintainL4LinkingEpisodeGate(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	linkTestEpisodes(t, store, emb, 2, "deploy the auth service", "deploy")
	cfg := config.Default() // MinEpisodesToRun = 3 > 2 episodes
	fake, calls := linkingFake(t)

	rep, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Skipped != "linking" {
		t.Errorf("rep.Skipped = %q, want linking (episode gate)", rep.Skipped)
	}
	if *calls != 0 {
		t.Errorf("LLM calls = %d, want 0 (below MinEpisodesToRun)", *calls)
	}
}

func TestMaintainL4LinkingBuckets(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	content := "deploy the auth service"
	// Same (layer, action_class) bucket: candidate pair.
	linkTestEpisodes(t, store, emb, 2, content, "deploy")
	// Same content but a different action_class: different bucket, no pair.
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, content,
		map[string]any{"action": "migrate", "action_class": "migrate"})
	// Same content and action_class but on L2: cross-layer, never a candidate.
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, content,
		map[string]any{"action_class": "deploy"})
	cfg := config.Default()
	cfg.System2.MinEpisodesToRun = 2
	fake, calls := linkingFake(t)

	rep, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Linked != 1 {
		t.Errorf("rep.Linked = %d, want 1 (one same-bucket pair)", rep.Linked)
	}
	if *calls != 1 {
		t.Errorf("LLM calls = %d, want 1", *calls)
	}
	eps, err := store.IterMemories("test", string(schema.LayerEpisodic), "")
	if err != nil {
		t.Fatal(err)
	}
	var deploy []*schema.Memory
	for _, m := range eps {
		if m.MetaString("action_class") == "deploy" {
			deploy = append(deploy, m)
		}
	}
	if len(deploy) != 2 {
		t.Fatalf("deploy episodes = %d, want 2", len(deploy))
	}
	edges, err := store.Neighbors(deploy[0].ID, "related_to")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("related_to edges at deploy[0] = %v, want exactly 1", edges)
	}
	e := edges[0]
	other := e.SrcID
	if other == deploy[0].ID {
		other = e.DstID
	}
	if other != deploy[1].ID {
		t.Errorf("related_to edge connects %s..%s, want the two deploy episodes", e.SrcID, e.DstID)
	}
	if e.ID != schema.ProjectedEdgeID(e.SrcID, "related_to", e.DstID) {
		t.Errorf("edge id = %q, want projected deterministic id", e.ID)
	}
	if e.Weight != 0.8 {
		t.Errorf("edge weight = %v, want 0.8 from LLM", e.Weight)
	}
}

func TestMaintainL4LinkingBudget(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	// 9 same-bucket episodes -> 36 candidate pairs; budget defaults to 20.
	linkTestEpisodes(t, store, emb, 9, "deploy the auth service", "deploy")
	cfg := config.Default()
	cfg.System2.MinEpisodesToRun = 2
	fake, calls := linkingFake(t)

	rep, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake})
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 20 {
		t.Errorf("LLM calls = %d, want 20 (budget cap)", *calls)
	}
	if rep.Linked != 20 {
		t.Errorf("rep.Linked = %d, want 20", rep.Linked)
	}
}

func TestMaintainL4LinkingSimPrefilter(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	linkTestEpisodes(t, store, emb, 1, "alpha bravo charlie delta", "deploy")
	linkTestEpisodes(t, store, emb, 1, "xray yankee zulu whiskey", "deploy")
	// Fixture sanity: the two contents must actually fall below the threshold.
	va, _ := emb.Embed("alpha bravo charlie delta")
	vb, _ := emb.Embed("xray yankee zulu whiskey")
	if sim := storage.CosineSimilarity(va, vb); sim >= config.Default().Maintain.LinkingSimilarity {
		t.Fatalf("fixture similarity = %v, want below %.2f", sim, config.Default().Maintain.LinkingSimilarity)
	}
	cfg := config.Default()
	cfg.System2.MinEpisodesToRun = 2
	fake, calls := linkingFake(t)

	rep, err := MaintainL4(store, emb, cfg, "test", &MaintainAgents{Link: fake})
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("LLM calls = %d, want 0 (sim below threshold)", *calls)
	}
	if rep.Linked != 0 {
		t.Errorf("rep.Linked = %d, want 0", rep.Linked)
	}
}

// --- Task 19: resource alias merge (spec §8.4) ------------------------------------

func putTestResource(t *testing.T, store storage.Store, emb *storage.HashingEmbedding, content string, aliases []string) *schema.Memory {
	t.Helper()
	return putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeResource, content,
		map[string]any{"aliases": aliases})
}

func aliasMergeFixture(t *testing.T) (storage.Store, *storage.HashingEmbedding, *schema.Memory, *schema.Memory, *schema.Memory) {
	t.Helper()
	store, emb, _ := maintainTestStore(t)
	// Two highly similar resource entities (identical content => cos 1.0)
	// with distinct alias lists, plus an episode operating on r2.
	r1 := putTestResource(t, store, emb, "customer database", []string{"custdb"})
	r2 := putTestResource(t, store, emb, "customer database", []string{"customers db", "cust db"})
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"agent=bot | action=deploy", map[string]any{"action": "deploy", "action_class": "deploy"})
	e := schema.NewEdge(ep.ID, "operates_on", r2.ID)
	e.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", r2.ID)
	if err := store.PutEdge(e); err != nil {
		t.Fatal(err)
	}
	return store, emb, r1, r2, ep
}

func TestMaintainL4AliasMergeConfirm(t *testing.T) {
	store, emb, r1, r2, ep := aliasMergeFixture(t)
	calls := 0
	fake := &providers.FakeLLMProvider{StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"same_entity": true}, nil
	}}

	rep, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{AliasMerge: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Merged != 1 {
		t.Fatalf("rep.Merged = %d, want 1", rep.Merged)
	}
	if calls != 1 {
		t.Errorf("LLM calls = %d, want 1", calls)
	}

	// Exactly one resource retired; the other (winner) survives.
	resources, err := store.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		t.Fatal(err)
	}
	var winner, loser *schema.Memory
	for _, m := range resources {
		switch {
		case IsRetired(m):
			loser = m
		default:
			winner = m
		}
	}
	if winner == nil || loser == nil {
		t.Fatalf("after merge want one retired and one live resource, got %v", resources)
	}
	if loser.MetaString("superseded_by") != winner.ID {
		t.Errorf("loser superseded_by = %q, want winner id %q", loser.MetaString("superseded_by"), winner.ID)
	}

	// Winner's aliases absorbed the loser's (deduped).
	got := resourceAliases(winner)
	for _, want := range []string{"custdb", "customers db", "cust db"} {
		found := false
		for _, a := range got {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("winner aliases = %v, want to contain %q", got, want)
		}
	}

	// The episode's operates_on edge was rebuilt to point at the winner.
	edges, err := store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	validToWinner := false
	for _, e := range edges {
		if e.ValidTo != nil {
			continue
		}
		if e.DstID == winner.ID && e.ID == schema.ProjectedEdgeID(e.SrcID, "operates_on", winner.ID) {
			validToWinner = true
		}
		if e.DstID == loser.ID {
			t.Errorf("still-valid operates_on edge points at the retired loser: %+v", e)
		}
	}
	if !validToWinner {
		t.Errorf("no valid operates_on edge from the episode to the winner: %v", edges)
	}
	_ = r1
	_ = r2

	// Retire left a supersedes chain loser->winner.
	sup, err := store.Neighbors(loser.ID, "supersedes")
	if err != nil {
		t.Fatal(err)
	}
	foundChain := false
	for _, e := range sup {
		if e.SrcID == loser.ID && e.DstID == winner.ID && e.ValidTo == nil {
			foundChain = true
		}
	}
	if !foundChain {
		t.Errorf("no supersedes edge loser->winner: %v", sup)
	}

	// Regression guard: the winner is still vector-searchable after the
	// merge rewrite (re-embedded, never a nil vector).
	vec, err := emb.Embed(winner.Content)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range store.VectorSearch(vec, 5) {
		if hit.ID == winner.ID {
			found = true
		}
	}
	if !found {
		t.Error("winner not found in vector search after alias merge (embedding lost?)")
	}
}

func TestMaintainL4AliasMergeReject(t *testing.T) {
	store, emb, _, _, _ := aliasMergeFixture(t)
	calls := 0
	fake := &providers.FakeLLMProvider{StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"same_entity": false}, nil
	}}

	rep, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{AliasMerge: fake})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Merged != 0 {
		t.Errorf("rep.Merged = %d, want 0 (LLM rejected)", rep.Merged)
	}
	if calls != 1 {
		t.Errorf("LLM calls = %d, want 1", calls)
	}
	resources, err := store.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range resources {
		if IsRetired(m) {
			t.Errorf("resource %q retired despite rejection", m.Content)
		}
	}
}

func TestMaintainL4AliasMergeSimPrefilter(t *testing.T) {
	store, emb, _ := maintainTestStore(t)
	putTestResource(t, store, emb, "alpha beta gamma", []string{"ab"})
	putTestResource(t, store, emb, "xray yankee zulu", []string{"xy"})
	va, _ := emb.Embed("alpha beta gamma")
	vb, _ := emb.Embed("xray yankee zulu")
	if sim := storage.CosineSimilarity(va, vb); sim >= config.Default().Maintain.AliasMergeSimilarity {
		t.Fatalf("fixture similarity = %v, want below %.2f", sim, config.Default().Maintain.AliasMergeSimilarity)
	}
	calls := 0
	fake := &providers.FakeLLMProvider{StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
		calls++
		return map[string]any{"same_entity": true}, nil
	}}

	rep, err := MaintainL4(store, emb, config.Default(), "test", &MaintainAgents{AliasMerge: fake})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("LLM calls = %d, want 0 (sim below threshold)", calls)
	}
	if rep.Merged != 0 {
		t.Errorf("rep.Merged = %d, want 0", rep.Merged)
	}
}
