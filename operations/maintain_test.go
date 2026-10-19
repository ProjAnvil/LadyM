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
