//go:build !enterprise

package operations

import (
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// putEpisodeWithResource records an episodic event and links it to a resolved
// resource entity with an operates_on projected edge — the write shape
// produced by Scope.RecordOperation (Task 8).
func putEpisodeWithResource(t *testing.T, store storage.Store, emb *storage.HashingEmbedding, cfg *config.Config, ws, content, resourceName string) (*schema.Memory, *schema.Memory) {
	t.Helper()
	ep := putCustomMem(t, store, emb, newCustomMem(schema.LayerEpisodic, schema.TypeEvent, content, ws))
	res, _, err := ResolveResource(store, emb, cfg, ws, resourceName)
	if err != nil {
		t.Fatal(err)
	}
	e := schema.NewEdge(ep.ID, "operates_on", res.ID)
	e.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", res.ID)
	if err := store.PutEdge(e); err != nil {
		t.Fatal(err)
	}
	return ep, res
}

func TestConsolidateAddProjectsProvenance(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	ep, res := putEpisodeWithResource(t, store, emb, cfg, "test", "migrated the schema", "ladym.db")

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionAdd)] != 1 || rep.PromotedToSemantic != 1 {
		t.Fatalf("report = %+v, want one ADD promotion", rep)
	}
	facts, err := store.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	fact := facts[0]

	// produced_from: fact → source episode, deterministic projected id.
	pf, err := store.Neighbors(fact.ID, "produced_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(pf) != 1 || pf[0].SrcID != fact.ID || pf[0].DstID != ep.ID {
		t.Fatalf("produced_from edges = %+v, want one fact→episode(%s)", pf, ep.ID)
	}
	if want := schema.ProjectedEdgeID(fact.ID, "produced_from", ep.ID); pf[0].ID != want {
		t.Errorf("produced_from id = %s, want %s", pf[0].ID, want)
	}

	// operates_on: the fact inherits the episode's resource target.
	oo, err := store.Neighbors(fact.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(oo) != 1 || oo[0].DstID != res.ID {
		t.Fatalf("fact operates_on edges = %+v, want one fact→resource(%s)", oo, res.ID)
	}
	if want := schema.ProjectedEdgeID(fact.ID, "operates_on", res.ID); oo[0].ID != want {
		t.Errorf("fact operates_on id = %s, want %s", oo[0].ID, want)
	}
}

func TestConsolidateUpdateProjectsProvenance(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.Consolidate.DedupSimilarityThreshold = 0.0 // any surviving neighbour triggers UPDATE
	fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens for sessions", "test")
	fact.ContentHash = schema.ContentHash(fact.Content)
	putCustomMem(t, store, emb, fact)
	ep, res := putEpisodeWithResource(t, store, emb, cfg, "test", "auth uses jwt tokens for session", "ladym.db")

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Fatalf("report = %+v, want one UPDATE", rep)
	}

	// Existing behavior: the old fact is retired and supersedes the new one.
	old, err := store.GetMemory(fact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !IsRetired(old) {
		t.Fatal("UPDATE should retire the old fact")
	}
	merged, err := store.GetMemory(old.MetaString("superseded_by"))
	if err != nil {
		t.Fatal(err)
	}
	if merged == nil {
		t.Fatal("merged fact missing")
	}

	// The merged fact gets the same two provenance projections.
	pf, err := store.Neighbors(merged.ID, "produced_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(pf) != 1 || pf[0].DstID != ep.ID {
		t.Fatalf("merged produced_from edges = %+v, want one merged→episode(%s)", pf, ep.ID)
	}
	if want := schema.ProjectedEdgeID(merged.ID, "produced_from", ep.ID); pf[0].ID != want {
		t.Errorf("merged produced_from id = %s, want %s", pf[0].ID, want)
	}
	oo, err := store.Neighbors(merged.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(oo) != 1 || oo[0].DstID != res.ID {
		t.Fatalf("merged operates_on edges = %+v, want one merged→resource(%s)", oo, res.ID)
	}
	if want := schema.ProjectedEdgeID(merged.ID, "operates_on", res.ID); oo[0].ID != want {
		t.Errorf("merged operates_on id = %s, want %s", oo[0].ID, want)
	}
}

func TestConsolidateReplayNoopNotReplayed(t *testing.T) {
	// Replay protection: a second unstamped episode with identical content
	// classifies as NOOP via the content_hash equality (spec §6.3) — it is
	// not re-promoted, and no additional fact or provenance edge appears.
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putEpisodeWithResource(t, store, emb, cfg, "test", "auth uses jwt tokens", "ladym.db")
	putEpisodeWithResource(t, store, emb, cfg, "test", "auth uses jwt tokens", "ladym.db")

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionAdd)] != 1 || rep.Actions[string(ActionNoop)] != 1 {
		t.Fatalf("report = %+v, want ADD=1 NOOP=1", rep)
	}
	facts, err := store.IterMemories("test", string(schema.LayerSemantic), string(schema.TypeFact))
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1 (replay must not create a second fact)", len(facts))
	}
	pf, err := store.Neighbors(facts[0].ID, "produced_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(pf) != 1 {
		t.Fatalf("produced_from edges = %d, want 1 (no replay edge)", len(pf))
	}
}
