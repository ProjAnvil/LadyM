package operations

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// --- helpers ---------------------------------------------------------------

// putCustomMem stores a fully customized memory (caller sets fields first).
func putCustomMem(t *testing.T, store *storage.SQLiteStore, emb *storage.HashingEmbedding, m *schema.Memory) *schema.Memory {
	t.Helper()
	vec, err := emb.Embed(m.Content)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(m, vec); err != nil {
		t.Fatal(err)
	}
	return m
}

func newCustomMem(layer schema.Layer, typ schema.MemoryType, content, workspace string) *schema.Memory {
	m := schema.NewMemory(layer, typ)
	m.Content = content
	m.Workspace = workspace
	return m
}

// errLLM is an LLM provider whose structured call always fails.
type errLLM struct{ err error }

func (e *errLLM) Name() string { return "err" }
func (e *errLLM) Complete(messages []providers.Message) (string, error) {
	return "", e.err
}
func (e *errLLM) CompleteStructured(messages []providers.Message, schema providers.JSONSchema) (map[string]any, error) {
	return nil, e.err
}
func (e *errLLM) Close() error { return nil }

var errBoom = errors.New("boom")

// --- activation & util -------------------------------------------------------

func TestNeighbourCountsFor(t *testing.T) {
	a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	got := NeighbourCountsFor([]*schema.Memory{a, b}, map[string]int{a.ID: 3})
	if len(got) != 2 || got[a.ID] != 3 || got[b.ID] != 0 {
		t.Errorf("NeighbourCountsFor = %v", got)
	}
}

func TestTypeBoostForQuery(t *testing.T) {
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	if got := TypeBoostForQuery(m, nil, 0.25); got != 0 {
		t.Errorf("empty query types boost = %v, want 0", got)
	}
	if got := TypeBoostForQuery(m, []schema.MemoryType{schema.TypeFact}, 0.25); got != 0 {
		t.Errorf("non-matching type boost = %v, want 0", got)
	}
	if got := TypeBoostForQuery(m, []schema.MemoryType{schema.TypeFact, schema.TypeCodeSymbol}, 0.25); got != 0.25 {
		t.Errorf("matching type boost = %v, want 0.25", got)
	}
}

func TestFrequencyFactorNegative(t *testing.T) {
	if got := FrequencyFactor(-5); got != 0 {
		t.Errorf("FrequencyFactor(-5) = %v, want 0 (clamped)", got)
	}
}

func TestRecencyFactorEdgeCases(t *testing.T) {
	if got := RecencyFactor(200, 100, 100); got != 1.0 {
		t.Errorf("RecencyFactor with negative age = %v, want 1 (clamped)", got)
	}
	got := RecencyFactor(schema.Now(), 3600, 0) // now==0 defaults to schema.Now()
	if math.Abs(got-1.0) > 1e-3 {
		t.Errorf("RecencyFactor with default now = %v, want ~1", got)
	}
}

func TestActivationScoreComponents(t *testing.T) {
	cfg := config.Default()
	w := cfg.Activation
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	m.LastAccessAt = 0
	m.AccessCount = 1
	score := ActivationScore(m, 0.5, w, map[string]int{m.ID: 3}, []schema.MemoryType{schema.TypeCodeSymbol}, 100)
	want := w.Similarity*0.5 +
		w.Recency*RecencyFactor(0, w.RecencyHalfLifeS, 100) +
		w.Frequency*math.Log(2) +
		w.Graph*math.Log(4) +
		w.TypeBoost
	if math.Abs(score-want) > 1e-9 {
		t.Errorf("ActivationScore = %v, want %v", score, want)
	}
	// negative similarity is clamped to 0
	clamped := ActivationScore(m, -1, w, nil, nil, 100)
	wantClamped := w.Recency*RecencyFactor(0, w.RecencyHalfLifeS, 100) + w.Frequency*math.Log(2)
	if math.Abs(clamped-wantClamped) > 1e-9 {
		t.Errorf("ActivationScore with negative sim = %v, want %v", clamped, wantClamped)
	}
}

func TestParseFloatOrZero(t *testing.T) {
	if got := parseFloatOrZero("junk"); got != 0 {
		t.Errorf("parseFloatOrZero(junk) = %v, want 0", got)
	}
	if got := parseFloatOrZero("1.5"); got != 1.5 {
		t.Errorf("parseFloatOrZero(1.5) = %v, want 1.5", got)
	}
}

// --- decay --------------------------------------------------------------------

func decayableMem(t *testing.T, store *storage.SQLiteStore, emb *storage.HashingEmbedding, content string, lastAccess float64) *schema.Memory {
	t.Helper()
	m := newCustomMem(schema.LayerEpisodic, schema.TypeEvent, content, "test")
	m.LastAccessAt = lastAccess
	return putCustomMem(t, store, emb, m)
}

func TestDecayDryRunThenReal(t *testing.T) {
	store, emb := newParityStore(t)
	now := schema.Now()
	old := decayableMem(t, store, emb, "ancient event", now-40*24*3600)
	fresh := decayableMem(t, store, emb, "recent event", now)

	rep, err := Decay(store, "test", nil, 0, 0, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Examined != 2 || rep.Forgotten != 1 || len(rep.ForgottenIDs) != 1 || rep.ForgottenIDs[0] != old.ID {
		t.Errorf("dry-run report = %+v", rep)
	}
	if got, _ := store.GetMemory(old.ID); got == nil {
		t.Error("dry run must not delete the memory")
	}

	rep, err = Decay(store, "test", nil, 0, 0, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Forgotten != 1 {
		t.Errorf("real-run report = %+v", rep)
	}
	if got, _ := store.GetMemory(old.ID); got != nil {
		t.Error("real run should delete the decayed memory")
	}
	if got, _ := store.GetMemory(fresh.ID); got == nil {
		t.Error("fresh memory must survive decay")
	}
}

func TestDecayAboveActivationFloorKept(t *testing.T) {
	store, emb := newParityStore(t)
	now := schema.Now()
	// Age exceeds maxAgeS, but with a huge half-life the recency activation
	// stays above the floor.
	decayableMem(t, store, emb, "old but warm event", now-200)
	weights := &config.ActivationWeights{Recency: 0.3, RecencyHalfLifeS: 1e12}
	rep, err := Decay(store, "test", weights, 100, 0, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Examined != 1 || rep.Forgotten != 0 {
		t.Errorf("report = %+v, want examined=1 forgotten=0", rep)
	}
}

// --- consolidate --------------------------------------------------------------

func TestConsolidateOfflineAdd(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "set up the deployment pipeline", map[string]any{"agent": "dev"})
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "wrote unit tests for auth", nil)

	// empty workspace falls back to cfg.Workspace
	rep, err := Consolidate(store, emb, cfg, "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionAdd)] != 2 || rep.PromotedToSemantic != 2 || rep.KeptEpisodes != 2 {
		t.Errorf("report = %+v", rep)
	}
	if len(rep.Details) != 2 {
		t.Errorf("details = %v", rep.Details)
	}
	facts, err := store.IterMemories("test", string(schema.LayerSemantic), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("semantic facts = %d, want 2", len(facts))
	}
	var found bool
	for _, f := range facts {
		if f.MetaString("source_episode") == ep.ID {
			found = true
		}
		if f.MetaString("agent") != "" && f.MetaString("agent") != "dev" {
			t.Errorf("episode metadata not copied: %v", f.Metadata)
		}
	}
	if !found {
		t.Error("promoted fact should carry source_episode metadata")
	}
}

func TestConsolidateOfflineNoopOnIdenticalHash(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens", "test")
	fact.ContentHash = schema.ContentHash(fact.Content)
	putCustomMem(t, store, emb, fact)
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "auth uses jwt tokens", nil)

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionNoop)] != 1 || rep.PromotedToSemantic != 0 {
		t.Errorf("report = %+v, want one NOOP and no promotions", rep)
	}
}

func TestConsolidateOfflineUpdateViaSupersededBy(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	content := "database migrated to postgres"
	fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, content, "test")
	fact.Metadata["superseded_by"] = schema.ContentHash(content)
	putCustomMem(t, store, emb, fact)
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, content, nil)

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Errorf("report = %+v, want one UPDATE", rep)
	}
}

func TestConsolidateOfflineUpdateViaSimilarity(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.Consolidate.DedupSimilarityThreshold = 0.0 // any surviving neighbour triggers UPDATE
	fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens for sessions", "test")
	fact.ContentHash = schema.ContentHash(fact.Content)
	putCustomMem(t, store, emb, fact)
	ep := newCustomMem(schema.LayerEpisodic, schema.TypeEvent, "auth uses jwt tokens for session", "test")
	ep.Summary = "jwt summary"
	putCustomMem(t, store, emb, ep)

	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Fatalf("report = %+v, want one UPDATE", rep)
	}
	got, err := store.GetMemory(fact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !IsRetired(got) {
		t.Error("UPDATE should retire the old fact")
	}
	newID := got.MetaString("superseded_by")
	merged, err := store.GetMemory(newID)
	if err != nil {
		t.Fatal(err)
	}
	if merged == nil || merged.Content != ep.Content || merged.Summary != "jwt summary" {
		t.Errorf("merged fact = %+v", merged)
	}
	if merged.MetaString("updated_from") != fact.ID || merged.MetaString("source_episode") != ep.ID {
		t.Errorf("merged metadata = %v", merged.Metadata)
	}
}

func TestConsolidateSinceFilter(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	now := schema.Now()
	old := newCustomMem(schema.LayerEpisodic, schema.TypeEvent, "old event", "test")
	old.CreatedAt = now - 1000
	putCustomMem(t, store, emb, old)
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "fresh event", nil)

	rep, err := Consolidate(store, emb, cfg, "test", nil, now-10)
	if err != nil {
		t.Fatal(err)
	}
	if rep.KeptEpisodes != 1 {
		t.Errorf("KeptEpisodes = %d, want 1 (old episode filtered by since)", rep.KeptEpisodes)
	}
}

func TestConsolidateLLMClassifier(t *testing.T) {
	newStoreWithFact := func(t *testing.T) (*storage.SQLiteStore, *storage.HashingEmbedding, *schema.Memory) {
		store, emb := newParityStore(t)
		fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens", "test")
		fact.ContentHash = schema.ContentHash(fact.Content)
		putCustomMem(t, store, emb, fact)
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "auth uses jwt tokens", nil)
		return store, emb, fact
	}

	t.Run("delete retires similar", func(t *testing.T) {
		store, emb, fact := newStoreWithFact(t)
		cfg := config.ForTesting(t.TempDir())
		rep, err := Consolidate(store, emb, cfg, "test", func(string, []string) (Action, string, error) {
			return ActionDelete, "", nil
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Actions[string(ActionDelete)] != 1 {
			t.Errorf("report = %+v, want one DELETE", rep)
		}
		got, _ := store.GetMemory(fact.ID)
		if !IsRetired(got) {
			t.Error("DELETE should retire the similar fact")
		}
	})

	t.Run("update without similar is skipped", func(t *testing.T) {
		store, emb := newParityStore(t)
		cfg := config.ForTesting(t.TempDir())
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "lonesome episode", nil)
		rep, err := Consolidate(store, emb, cfg, "test", func(string, []string) (Action, string, error) {
			return ActionUpdate, "", nil
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Actions[string(ActionUpdate)] != 1 || rep.PromotedToSemantic != 0 {
			t.Errorf("report = %+v", rep)
		}
	})

	t.Run("delete without similar is a no-op", func(t *testing.T) {
		store, emb := newParityStore(t)
		cfg := config.ForTesting(t.TempDir())
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "lonesome episode", nil)
		rep, err := Consolidate(store, emb, cfg, "test", func(string, []string) (Action, string, error) {
			return ActionDelete, "", nil
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Actions[string(ActionDelete)] != 1 {
			t.Errorf("report = %+v", rep)
		}
	})

	t.Run("classifier error aborts", func(t *testing.T) {
		store, emb, _ := newStoreWithFact(t)
		cfg := config.ForTesting(t.TempDir())
		_, err := Consolidate(store, emb, cfg, "test", func(string, []string) (Action, string, error) {
			return ActionNoop, "", errBoom
		}, 0)
		if !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("update uses classifier text", func(t *testing.T) {
		store, emb, fact := newStoreWithFact(t)
		cfg := config.ForTesting(t.TempDir())
		var gotSimilar []string
		_, err := Consolidate(store, emb, cfg, "test", func(candidate string, similar []string) (Action, string, error) {
			gotSimilar = similar
			return ActionUpdate, "rewritten fact", nil
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(gotSimilar) != 1 || gotSimilar[0] != fact.Content {
			t.Errorf("similar texts = %v", gotSimilar)
		}
		old, _ := store.GetMemory(fact.ID)
		merged, _ := store.GetMemory(old.MetaString("superseded_by"))
		if merged == nil || merged.Content != "rewritten fact" {
			t.Errorf("merged = %+v, want classifier text", merged)
		}
	})
}

// --- attention ------------------------------------------------------------------

func nilAgent(string) (providers.LLMProvider, error) { return nil, nil }

func TestAttentionWorkingLayerNeverGated(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	d, err := AttentionGate("ok", cfg, store, nilAgent, schema.LayerWorking)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "pass" {
		t.Errorf("decision = %+v, want pass", d)
	}
}

func TestAttentionNoiseAndBlank(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	d, err := AttentionGate("lol OK foo", cfg, store, nilAgent, schema.LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "drop" || d.Reason != "noise" {
		t.Errorf("decision = %+v, want drop/noise", d)
	}
	// blank content has no tokens → not "all noise" → passes offline
	d, err = AttentionGate("   ", cfg, store, nilAgent, schema.LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "pass" {
		t.Errorf("blank decision = %+v, want pass", d)
	}
}

func TestAttentionRecentDuplicateDrop(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "unique decision xyzzy", nil)
	d, err := AttentionGate("unique decision xyzzy", cfg, store, nilAgent, schema.LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "drop" || d.Reason != "recent duplicate" {
		t.Errorf("decision = %+v, want drop/recent duplicate", d)
	}
}

func TestAttentionLLMGate(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	llm := &fakeParityLLM{out: map[string]any{
		"action": "rewrite", "content": "cleaned up", "reason": "wordy",
	}}
	d, err := AttentionGate("some valuable but messy note", cfg, store,
		func(string) (providers.LLMProvider, error) { return llm, nil }, schema.LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != "rewrite" || d.Content != "cleaned up" || d.Reason != "wordy" {
		t.Errorf("decision = %+v", d)
	}
	if llm.gotUser != "some valuable but messy note" {
		t.Errorf("user message = %q", llm.gotUser)
	}
}

func TestAttentionErrors(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	_, err := AttentionGate("a real note", cfg, store,
		func(string) (providers.LLMProvider, error) { return nil, errBoom }, schema.LayerSemantic)
	if !errors.Is(err, errBoom) {
		t.Errorf("getAgent err = %v", err)
	}
	_, err = AttentionGate("another real note", cfg, store,
		func(string) (providers.LLMProvider, error) { return &errLLM{errBoom}, nil }, schema.LayerSemantic)
	if !errors.Is(err, errBoom) {
		t.Errorf("llm err = %v", err)
	}
}

// --- recall ---------------------------------------------------------------------

func TestRecallStopwordQueryIsSufficient(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "anything at all", nil)
	resp, err := Recall(store, emb, "the and of", cfg, "test", 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 1 || !resp.ReflectedSufficient {
		t.Errorf("response = %+v, want tier 1 sufficient (no content tokens)", resp)
	}
}

func TestRecallLayerAndTypeFilters(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	fact := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeNote, "auth uses jwt tokens", nil)

	resp, err := Recall(store, emb, "auth jwt", cfg, "test", 5,
		[]schema.Layer{schema.LayerSemantic}, []schema.MemoryType{schema.TypeFact}, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Memory.ID != fact.ID {
		ids := []string{}
		for _, r := range resp.Results {
			ids = append(ids, r.Memory.ID)
		}
		t.Errorf("filtered results = %v, want only the fact", ids)
	}

	resp, err = Recall(store, emb, "auth jwt", cfg, "test", 5,
		[]schema.Layer{schema.LayerEpisodic}, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("episodic-only results = %d, want 0", len(resp.Results))
	}
}

func TestRecallTier2GraphExpansion(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	anchor := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files", nil)
	linked := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"zz qqq completely different subject", nil)
	foreign := newCustomMem(schema.LayerSemantic, schema.TypeFact, "zz qqq foreign workspace note", "other")
	putCustomMem(t, store, emb, foreign)
	if err := store.PutEdge(schema.NewEdge(anchor.ID, "related_to", linked.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(schema.NewEdge(anchor.ID, "related_to", foreign.ID)); err != nil {
		t.Fatal(err)
	}

	resp, err := Recall(store, emb, "parseConfig config", cfg, "", 5, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 2 {
		t.Fatalf("TierReached = %d, want 2", resp.TierReached)
	}
	var found *schema.RecallResult
	for _, r := range resp.Results {
		if r.Memory.ID == linked.ID {
			found = r
		}
		if r.Memory.ID == foreign.ID {
			t.Error("cross-workspace neighbour must not be expanded")
		}
	}
	if found == nil {
		t.Fatal("graph expansion should pull the linked memory")
	}
	if found.Tier != 2 || len(found.Via) != 2 || found.Via[0] != anchor.ID || found.Via[1] != linked.ID {
		t.Errorf("expanded result = %+v", found)
	}
}

func TestRecallTier2FollowsSupersedes(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	anchor := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files", nil)
	newer := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"zz qqq newer parser version", nil)
	if err := store.PutEdge(schema.NewEdge(anchor.ID, "supersedes", newer.ID)); err != nil {
		t.Fatal(err)
	}

	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 5, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	var found *schema.RecallResult
	for _, r := range resp.Results {
		if r.Memory.ID == newer.ID {
			found = r
		}
	}
	if found == nil {
		t.Fatal("tier-2 should follow supersedes edges to the newer version")
	}
	if found.Tier != 2 {
		t.Errorf("newer version tier = %d, want 2", found.Tier)
	}
}

func TestRecallTier2GraphHopsLimit(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.Recall.GraphHops = 1
	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files", nil)
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"zz qqq one hop away", nil)
	c := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"vv nnn two hops away", nil)
	if err := store.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(schema.NewEdge(b.ID, "related_to", c.ID)); err != nil {
		t.Fatal(err)
	}

	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 20, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range resp.Results {
		seen[r.Memory.ID] = true
	}
	if !seen[b.ID] {
		t.Error("one-hop neighbour should be expanded")
	}
	if seen[c.ID] {
		t.Error("two-hop neighbour must be cut off by GraphHops=1")
	}
}

func TestRecallTier2TypeFilterAppliesToExpansion(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	anchor := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files", nil)
	note := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeNote,
		"zz qqq linked note", nil)
	if err := store.PutEdge(schema.NewEdge(anchor.ID, "related_to", note.ID)); err != nil {
		t.Fatal(err)
	}
	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 20, nil,
		[]schema.MemoryType{schema.TypeFact}, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resp.Results {
		if r.Memory.ID == note.ID {
			t.Error("type filter must also apply to tier-2 expansions")
		}
	}
	if resp.TierReached != 2 {
		t.Errorf("TierReached = %d, want 2", resp.TierReached)
	}
}

// --- l5 ---------------------------------------------------------------------------

func TestNormalizeAndDot(t *testing.T) {
	z := normalize([]float32{0, 0, 0})
	for _, x := range z {
		if x != 0 {
			t.Errorf("normalize(zero) = %v", z)
		}
	}
	if got := dot([]float32{1, 2}, []float32{3, 4}); got != 11 {
		t.Errorf("dot = %v, want 11", got)
	}
}

func TestConnectedComponents(t *testing.T) {
	if got := connectedComponents(nil, nil, 0.5); got != nil {
		t.Errorf("empty components = %v, want nil", got)
	}
	ids := []string{"a", "b", "c"}
	vecs := [][]float32{{1, 0}, {1, 0}, {0, 1}}
	comps := connectedComponents(ids, vecs, 0.9)
	if len(comps) != 2 {
		t.Fatalf("components = %v, want 2 groups", comps)
	}
	for _, g := range comps {
		if len(g) == 2 && (g[0] != "a" || g[1] != "b") {
			t.Errorf("unexpected pair %v", g)
		}
	}
}

func TestExtractL5SkipsCoveredMembers(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 0.0
	cfg.System2.L5MinClusterSize = 2
	cfg.System2.L5MergeEveryNCycles = 0
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}

	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 1 {
		t.Fatalf("first run NewModels = %d, want 1", rep.NewModels)
	}
	// second run: members are covered by the model's abstracts edges
	rep, err = ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 0 {
		t.Errorf("second run NewModels = %d, want 0 (members covered)", rep.NewModels)
	}
	// retire the model: coveredMemberIDs skips retired models, members free again
	ms, err := store.IterMemories("test", string(schema.LayerL5Mental), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("L5 models = %d", len(ms))
	}
	if err := Retire(store, ms[0], ""); err != nil {
		t.Fatal(err)
	}
	rep, err = ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 1 {
		t.Errorf("third run NewModels = %d, want 1 after retiring the model", rep.NewModels)
	}
}

func TestExtractL5TitleDefaultAndSummariseError(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 0.0
	cfg.System2.L5MinClusterSize = 2
	cfg.System2.L5MergeEveryNCycles = 0
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)

	// empty title falls back to "mental model"
	llm := &fakeParityLLM{out: map[string]any{"title": "", "model": "body"}}
	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 1 {
		t.Fatalf("NewModels = %d", rep.NewModels)
	}
	ms, _ := store.IterMemories("test", string(schema.LayerL5Mental), "")
	if len(ms) != 1 || ms[0].Summary != "mental model" {
		t.Errorf("model = %+v, want default title", ms)
	}

	// LLM failure skips the cluster without failing the run
	store2, emb2 := newParityStore(t)
	putParityMem(t, store2, emb2, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
	putParityMem(t, store2, emb2, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)
	rep, err = ExtractL5(store2, emb2, cfg, "test", &errLLM{errBoom}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 0 {
		t.Errorf("NewModels = %d, want 0 on LLM failure", rep.NewModels)
	}
}

// seedMergeableModels creates two similar L5 models each abstracting one fact.
func seedMergeableModels(t *testing.T, store *storage.SQLiteStore, emb *storage.HashingEmbedding) (m1, m2 *schema.Memory) {
	t.Helper()
	f1 := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "deployments need rollback plans", nil)
	f2 := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "releases need smoke tests", nil)
	m1 = newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "shared model about safe releases", "test")
	m2 = newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "shared model about safe releases", "test")
	putCustomMem(t, store, emb, m1)
	putCustomMem(t, store, emb, m2)
	for _, pair := range [][2]string{{m1.ID, f1.ID}, {m2.ID, f2.ID}} {
		e := schema.NewEdge(pair[0], abstractsRelation, pair[1])
		if err := store.PutEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	return m1, m2
}

func TestExtractL5MergeCycle(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 1.1 // no new extraction clusters
	cfg.System2.L5MinClusterSize = 2
	cfg.System2.L5MergeSimilarity = 0.9
	cfg.System2.L5MergeEveryNCycles = 2 // merge only every second cycle
	m1, m2 := seedMergeableModels(t, store, emb)
	llm := &fakeParityLLM{out: map[string]any{"title": "merged model", "model": "merged body"}}

	// cycle 1: counter increments, no merge
	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 0 {
		t.Fatalf("cycle 1 MergedModels = %d, want 0", rep.MergedModels)
	}
	raw, err := store.GetMeta("l5_merge_cycle_count")
	if err != nil {
		t.Fatal(err)
	}
	if raw != "1" {
		t.Errorf("merge counter = %q, want 1", raw)
	}

	// cycle 2: counter reaches n → merge, counter resets
	rep, err = ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 1 {
		t.Fatalf("cycle 2 MergedModels = %d, want 1", rep.MergedModels)
	}
	raw, _ = store.GetMeta("l5_merge_cycle_count")
	if raw != "0" {
		t.Errorf("merge counter after merge = %q, want 0", raw)
	}
	for _, m := range []*schema.Memory{m1, m2} {
		got, _ := store.GetMemory(m.ID)
		if !IsRetired(got) {
			t.Errorf("old model %s should be retired by the merge", m.ID)
		}
	}
	ms, err := store.IterMemories("test", string(schema.LayerL5Mental), "")
	if err != nil {
		t.Fatal(err)
	}
	var merged *schema.Memory
	for _, m := range ms {
		if !IsRetired(m) {
			merged = m
		}
	}
	if merged == nil || merged.Content != "merged model: merged body" {
		t.Fatalf("merged model = %+v", merged)
	}
	if merged.MetaString("source") != "" && merged.Source != "l5_merge" {
		t.Errorf("merged source = %q", merged.Source)
	}
	edges, err := store.Neighbors(merged.ID, abstractsRelation)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Errorf("merged model abstracts edges = %d, want 2", len(edges))
	}
	if len(rep.Clusters) != 1 || rep.Clusters[0]["action"] != "merged" {
		t.Errorf("clusters = %v", rep.Clusters)
	}
}

func TestExtractL5MergeWithoutMembers(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 1.1
	cfg.System2.L5MergeSimilarity = 0.9
	cfg.System2.L5MergeEveryNCycles = 1
	// two similar models with no abstracts edges → merge component has no members
	newCustom := newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "lonely model about nothing", "test")
	putCustomMem(t, store, emb, newCustom)
	putCustomMem(t, store, emb, newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "lonely model about nothing", "test"))
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}
	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 0 {
		t.Errorf("MergedModels = %d, want 0 (no members to merge)", rep.MergedModels)
	}
}

// --- l6 ---------------------------------------------------------------------------

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("short", 80); got != "short" {
		t.Errorf("truncateStr(short) = %q", got)
	}
	long := strings.Repeat("x", 100)
	if got := truncateStr(long, 80); len(got) != 80 {
		t.Errorf("truncateStr(long) len = %d, want 80", len(got))
	}
}

func TestPredictL6StoresIntentsAndWatermark(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "ran the test suite", nil)
	llm := &fakeParityLLM{out: map[string]any{"intents": []any{
		"not-a-map",
		map[string]any{"intent": "check the logs"},
		map[string]any{"intent": "deploy the fix", "confidence": 0.9, "horizon_s": 60.0},
	}}}

	rep, err := PredictL6(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Predictions != 2 || rep.EpisodesSeen != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Details[0]["confidence"] != 0.5 {
		t.Errorf("default confidence = %v, want 0.5", rep.Details[0]["confidence"])
	}
	ms, err := store.IterMemories("test", string(schema.LayerL6Predictive), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("stored intents = %d, want 2", len(ms))
	}
	byContent := map[string]*schema.Memory{}
	for _, m := range ms {
		byContent[m.Content] = m
	}
	def := byContent["check the logs"]
	if def == nil {
		t.Fatal("defaulted intent not stored")
	}
	if h, _ := def.MetaFloat("horizon_s"); h != cfg.System2.L6HorizonS {
		t.Errorf("default horizon = %v, want %v", h, cfg.System2.L6HorizonS)
	}
	custom := byContent["deploy the fix"]
	if custom == nil {
		t.Fatal("custom intent not stored")
	}
	if c, _ := custom.MetaFloat("confidence"); c != 0.9 {
		t.Errorf("confidence = %v, want 0.9", c)
	}
	if rep.WatermarkUpdatedTo <= 0 {
		t.Errorf("watermark = %v", rep.WatermarkUpdatedTo)
	}

	// second run: watermark filters out old episodes
	rep, err = PredictL6(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.EpisodesSeen != 0 || rep.Predictions != 0 {
		t.Errorf("second run report = %+v, want no new episodes", rep)
	}
}

// --- proceduralize ----------------------------------------------------------------

func putSuccessEpisodes(t *testing.T, store *storage.SQLiteStore, emb *storage.HashingEmbedding, content, action, outcome string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, content,
			map[string]any{"outcome": outcome, "action": action})
	}
}

func TestProceduralizeDefaultsAndOutcomes(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "ok", 1)
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "done", 1)
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 1)
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "failed", 1)

	// zeros → defaults (minClusterSize 3, threshold 0.55); "" → cfg.Workspace
	rep, err := Proceduralize(store, emb, cfg, "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClustersExamined != 1 || rep.PlaybooksCreated != 1 || rep.Actions[string(ActionAdd)] != 1 {
		t.Errorf("report = %+v", rep)
	}
}

func TestProceduralizeBelowMinCluster(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 2)
	rep, err := Proceduralize(store, emb, cfg, "test", 3, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClustersExamined != 0 || rep.PlaybooksCreated != 0 {
		t.Errorf("report = %+v, want empty", rep)
	}
}

func TestProceduralizeNoopOnIdenticalRerun(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)

	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.PlaybooksCreated != 1 {
		t.Fatalf("first run report = %+v", rep)
	}
	rep, err = Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionNoop)] != 1 || rep.PlaybooksCreated != 0 {
		t.Errorf("second run report = %+v, want one NOOP", rep)
	}
}

func TestProceduralizeUpdateOnChangedCluster(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)
	if _, err := Proceduralize(store, emb, cfg, "test", 2, 0.01); err != nil {
		t.Fatal(err)
	}
	// a fourth episode changes the cluster size → new name/hash → UPDATE
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 1)
	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Fatalf("report = %+v, want one UPDATE", rep)
	}
	playbooks, err := store.IterMemories("test", string(schema.LayerProcedural), string(schema.TypePlaybook))
	if err != nil {
		t.Fatal(err)
	}
	var retired, active int
	for _, p := range playbooks {
		if IsRetired(p) {
			retired++
		} else {
			active++
		}
	}
	if retired != 1 || active != 1 {
		t.Errorf("playbooks retired=%d active=%d, want 1/1", retired, active)
	}
}

func TestProceduralizeMultipleClusters(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)
	putSuccessEpisodes(t, store, emb, "zz qqq unrelated topic entirely", "write", "success", 3)

	// high threshold: identical episodes still cluster (cosine 1.0), but the
	// two playbook candidates stay below the update threshold
	rep, err := Proceduralize(store, emb, cfg, "test", 3, 0.99)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClustersExamined != 2 || rep.PlaybooksCreated != 2 {
		t.Errorf("report = %+v, want two clusters", rep)
	}
	verbs := map[string]bool{}
	for _, d := range rep.Details {
		verbs[d["action_verb"].(string)] = true
	}
	if !verbs["deploy"] || !verbs["write"] {
		t.Errorf("action verbs = %v", verbs)
	}
}

// --- system2 ----------------------------------------------------------------------

type cycleRunner struct {
	episodes int
	fail     string
}

func (r *cycleRunner) Consolidate(workspace string, since float64) (*ConsolidationReport, error) {
	if r.fail == "consolidate" {
		return nil, errBoom
	}
	return newConsolidationReport(), nil
}
func (r *cycleRunner) Proceduralize(workspace string, minClusterSize int) (*ProceduralizeReport, error) {
	if r.fail == "proceduralize" {
		return nil, errBoom
	}
	return newProceduralizeReport(), nil
}
func (r *cycleRunner) ExtractMentalModels(workspace string) (*L5ExtractionReport, error) {
	if r.fail == "l5" {
		return nil, errBoom
	}
	return &L5ExtractionReport{}, nil
}
func (r *cycleRunner) PredictForwardIntents(workspace string) (*L6PredictionReport, error) {
	if r.fail == "l6" {
		return nil, errBoom
	}
	return &L6PredictionReport{}, nil
}
func (r *cycleRunner) Decay(workspace string, dryRun bool, maxAgeS, activationFloor float64) (*DecayReport, error) {
	if r.fail == "decay" {
		return nil, errBoom
	}
	return &DecayReport{}, nil
}
func (r *cycleRunner) CountRecentEpisodes(workspace string) (int, error) {
	if r.fail == "count" {
		return 0, errBoom
	}
	return r.episodes, nil
}
func (r *cycleRunner) MinEpisodesToRun() int { return 3 }

func TestRunSystem2CycleFull(t *testing.T) {
	rep, err := RunSystem2Cycle(&cycleRunner{episodes: 5}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if rep.SkippedLLMSteps || rep.Consolidate == nil || rep.Proceduralize == nil ||
		rep.L5 == nil || rep.L6 == nil || rep.Decay == nil {
		t.Errorf("report = %+v, want all steps populated", rep)
	}
}

func TestRunSystem2CycleSkipsLLMSteps(t *testing.T) {
	rep, err := RunSystem2Cycle(&cycleRunner{episodes: 1}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.SkippedLLMSteps || rep.L5 != nil || rep.L6 != nil {
		t.Errorf("report = %+v, want LLM steps skipped", rep)
	}
}

func TestRunSystem2CycleErrors(t *testing.T) {
	for _, fail := range []string{"consolidate", "proceduralize", "count", "l5", "l6", "decay"} {
		t.Run(fail, func(t *testing.T) {
			_, err := RunSystem2Cycle(&cycleRunner{episodes: 5, fail: fail}, "test")
			if !errors.Is(err, errBoom) {
				t.Errorf("fail=%s err = %v, want errBoom", fail, err)
			}
		})
	}
}

// --- supersedes -------------------------------------------------------------------

func TestRetireClosesEdgesAndWritesSupersedes(t *testing.T) {
	store, emb := newParityStore(t)
	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "old fact version", nil)
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "link target", nil)
	c := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "new fact version", nil)
	if err := store.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
		t.Fatal(err)
	}

	if err := Retire(store, a, c.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetMemory(a.ID)
	if got.MetaString("superseded_by") != c.ID {
		t.Errorf("superseded_by = %q", got.MetaString("superseded_by"))
	}
	edges, err := store.Neighbors(a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// the related_to edge is closed; only the new supersedes edge remains valid
	if len(edges) != 1 || edges[0].Relation != "supersedes" || edges[0].DstID != c.ID {
		t.Errorf("valid edges after retire = %+v", edges)
	}
	latest, err := LatestInChain(store, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest != c.ID {
		t.Errorf("LatestInChain = %s, want %s", latest, c.ID)
	}
}

func TestRetireNilMetadata(t *testing.T) {
	store, _ := newParityStore(t)
	m := &schema.Memory{ID: schema.NewID(), Layer: schema.LayerSemantic, Type: schema.TypeFact, Workspace: "test"}
	if err := Retire(store, m, ""); err != nil {
		t.Fatal(err)
	}
	if !m.MetaBool("superseded") {
		t.Error("retire should set superseded on nil-metadata memory")
	}
}

func TestLatestInChainMultiHopAndCycle(t *testing.T) {
	store, emb := newParityStore(t)
	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "v1", nil)
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "v2", nil)
	c := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "v3", nil)
	if err := store.PutEdge(schema.NewEdge(a.ID, "supersedes", b.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(schema.NewEdge(b.ID, "supersedes", c.ID)); err != nil {
		t.Fatal(err)
	}
	latest, err := LatestInChain(store, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest != c.ID {
		t.Errorf("LatestInChain(v1) = %s, want v3 %s", latest, c.ID)
	}
	latest, err = LatestInChain(store, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest != c.ID {
		t.Errorf("LatestInChain(v3) = %s, want itself", latest)
	}

	// a cycle must terminate instead of looping forever
	x := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "x", nil)
	y := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "y", nil)
	if err := store.PutEdge(schema.NewEdge(x.ID, "supersedes", y.ID)); err != nil {
		t.Fatal(err)
	}
	if err := store.PutEdge(schema.NewEdge(y.ID, "supersedes", x.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := LatestInChain(store, x.ID); err != nil {
		t.Fatal(err)
	}
}

// --- edge cases ------------------------------------------------------------------

func TestMaxF(t *testing.T) {
	if got := maxF(0.5, 0.1); got != 0.5 {
		t.Errorf("maxF(0.5, 0.1) = %v", got)
	}
	if got := maxF(0.1, 0.5); got != 0.5 {
		t.Errorf("maxF(0.1, 0.5) = %v", got)
	}
}

func TestRecallFiltersAndTruncation(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens alpha", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens beta", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens gamma", nil)
	// retired hit must be filtered out
	retired := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens retired", nil)
	if err := Retire(store, retired, ""); err != nil {
		t.Fatal(err)
	}
	// foreign-workspace hit must be filtered out
	putCustomMem(t, store, emb, newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens foreign", "other"))

	// topK smaller than the candidate pool truncates tier 1; the query tokens
	// are fully covered so reflection is sufficient.
	resp, err := Recall(store, emb, "auth jwt tokens", cfg, "test", 2, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Errorf("results = %d, want 2 (topK truncation)", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Memory.ID == retired.ID {
			t.Error("retired memory must be filtered from recall")
		}
		if r.Memory.Workspace != "test" {
			t.Error("foreign-workspace memory must be filtered from recall")
		}
	}
}

func TestRecallTier2DedupLayerFilterAndTruncation(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files", nil)
	// b is similar enough to enter tier 1 alongside a → expansion must skip it
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"parseConfig handles toml config files too", nil)
	// episodic neighbour: excluded from tier 2 by the layer filter
	ep := putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent,
		"zz qqq episodic neighbour", nil)
	// extra semantic neighbour: survives the filters, forces merged truncation
	d := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact,
		"vv nnn semantic neighbour", nil)
	for _, dst := range []string{b.ID, ep.ID, d.ID} {
		if err := store.PutEdge(schema.NewEdge(a.ID, "related_to", dst)); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 1,
		[]schema.Layer{schema.LayerSemantic}, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 2 {
		t.Fatalf("TierReached = %d, want 2", resp.TierReached)
	}
	if len(resp.Results) != 1 {
		t.Errorf("results = %d, want 1 (TopKTier2 truncation)", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Memory.ID == ep.ID {
			t.Error("layer filter must apply to tier-2 expansions")
		}
	}
}

func TestRecallBacktrackSkipsSymbolWithoutFilePath(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeCodeSymbol,
		"func parseConfig parses toml config files", nil) // no file_path metadata
	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 0, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 2 {
		t.Errorf("TierReached = %d, want 2", resp.TierReached)
	}
}

func TestConsolidateSortsSimilarBySim(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.Consolidate.DedupSimilarityThreshold = 0.0
	for _, c := range []string{"auth uses jwt tokens v1", "auth uses jwt tokens v2"} {
		f := newCustomMem(schema.LayerSemantic, schema.TypeFact, c, "test")
		f.ContentHash = schema.ContentHash(c)
		putCustomMem(t, store, emb, f)
	}
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "auth uses jwt tokens", nil)
	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Errorf("report = %+v, want one UPDATE", rep)
	}
}

func TestDecayDefaultNow(t *testing.T) {
	store, emb := newParityStore(t)
	decayableMem(t, store, emb, "ancient event", schema.Now()-40*24*3600)
	// now == 0 defaults to schema.Now()
	rep, err := Decay(store, "test", nil, 0, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Forgotten != 1 {
		t.Errorf("report = %+v, want 1 forgotten with default now", rep)
	}
}

func TestPredictL6EdgeCases(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L6MaxEpisodes = 1 // force window truncation
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "first episode", nil)
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "second episode", nil)
	// an already-retired intent is skipped by the expire sweep
	stale := putParityMem(t, store, emb, schema.LayerL6Predictive, schema.TypeForwardIntent, "old intent", nil)
	if err := Retire(store, stale, ""); err != nil {
		t.Fatal(err)
	}
	llm := &fakeParityLLM{out: map[string]any{"intents": []any{}}}
	// empty workspace falls back to cfg.Workspace
	rep, err := PredictL6(store, emb, cfg, "", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.EpisodesSeen != 1 {
		t.Errorf("EpisodesSeen = %d, want 1 (L6MaxEpisodes truncation)", rep.EpisodesSeen)
	}
	if rep.ExpiredRetired != 0 {
		t.Errorf("ExpiredRetired = %d, want 0 (retired intent skipped)", rep.ExpiredRetired)
	}

	// LLM failure propagates
	store2, emb2 := newParityStore(t)
	putParityMem(t, store2, emb2, schema.LayerEpisodic, schema.TypeEvent, "episode", nil)
	if _, err := PredictL6(store2, emb2, cfg, "test", &errLLM{errBoom}, ""); !errors.Is(err, errBoom) {
		t.Errorf("err = %v, want errBoom", err)
	}
}

func TestExtractL5ClusterBelowMinSize(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 0.0
	cfg.System2.L5MinClusterSize = 3 // only two facts → cluster too small
	cfg.System2.L5MergeEveryNCycles = 0
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}
	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels != 0 || len(rep.Clusters) != 0 {
		t.Errorf("report = %+v, want no models for undersized clusters", rep)
	}
}

func TestExtractL5MergeEarlyReturnAndDissimilar(t *testing.T) {
	// fewer than two models → mergeL5 returns immediately
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 1.1
	cfg.System2.L5MergeEveryNCycles = 1
	putCustomMem(t, store, emb, newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "only model", "test"))
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}
	rep, err := ExtractL5(store, emb, cfg, "", llm, "") // ws "" → cfg.Workspace
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 0 {
		t.Errorf("MergedModels = %d, want 0 with a single model", rep.MergedModels)
	}

	// two dissimilar models → singleton components are skipped
	store2, emb2 := newParityStore(t)
	putCustomMem(t, store2, emb2, newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "model about alpha deployments", "test"))
	putCustomMem(t, store2, emb2, newCustomMem(schema.LayerL5Mental, schema.TypeMentalModel, "zz qqq entirely different model", "test"))
	rep, err = ExtractL5(store2, emb2, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 0 {
		t.Errorf("MergedModels = %d, want 0 for dissimilar models", rep.MergedModels)
	}
}

func TestExtractL5MergeSummariseErrorAndTitleDefault(t *testing.T) {
	// LLM failure inside the merge skips the component without failing the run
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 1.1
	cfg.System2.L5MergeSimilarity = 0.9
	cfg.System2.L5MergeEveryNCycles = 1
	seedMergeableModels(t, store, emb)
	rep, err := ExtractL5(store, emb, cfg, "test", &errLLM{errBoom}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 0 {
		t.Errorf("MergedModels = %d, want 0 on LLM failure", rep.MergedModels)
	}

	// empty title from the LLM falls back to "mental model"
	store2, emb2 := newParityStore(t)
	seedMergeableModels(t, store2, emb2)
	llm := &fakeParityLLM{out: map[string]any{"title": "", "model": "body"}}
	rep, err = ExtractL5(store2, emb2, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.MergedModels != 1 {
		t.Fatalf("MergedModels = %d, want 1", rep.MergedModels)
	}
	ms, _ := store2.IterMemories("test", string(schema.LayerL5Mental), "")
	var merged *schema.Memory
	for _, m := range ms {
		if !IsRetired(m) {
			merged = m
		}
	}
	if merged == nil || merged.Summary != "mental model" {
		t.Errorf("merged model = %+v, want default title", merged)
	}
}

func TestProceduralizeSkipsForeignAndRetiredPlaybooks(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)

	// a same-content playbook in another workspace must be ignored
	foreign := newCustomMem(schema.LayerProcedural, schema.TypePlaybook, "How to deploy (3 episodes)\n1. deploy", "other")
	foreign.ContentHash = schema.ContentHash(foreign.Content)
	putCustomMem(t, store, emb, foreign)

	if _, err := Proceduralize(store, emb, cfg, "test", 2, 0.01); err != nil {
		t.Fatal(err)
	}
	// third run: the retired previous playbook (from the UPDATE below) and the
	// foreign one are both skipped during retrieval
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 1)
	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionUpdate)] != 1 {
		t.Fatalf("report = %+v, want one UPDATE", rep)
	}
	rep, err = Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionNoop)] != 1 {
		t.Errorf("third run report = %+v, want one NOOP", rep)
	}
}

func TestProceduralizeSortsMultipleSimilarPlaybooks(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)
	// two pre-existing playbooks similar to the candidate → retrieval sorts them
	for i := 0; i < 2; i++ {
		p := newCustomMem(schema.LayerProcedural, schema.TypePlaybook, "How to deploy (3 episodes)\n1. deploy", "test")
		p.ContentHash = schema.ContentHash(p.Content)
		putCustomMem(t, store, emb, p)
	}
	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Actions[string(ActionNoop)] != 1 {
		t.Errorf("report = %+v, want one NOOP", rep)
	}
}

func TestProceduralizeAssignedSkipAndObservationSteps(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	// A and C cluster together; B stays unassigned. When the anchor loop later
	// reaches B, its inner scan sees C already assigned.
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "deploy alpha service",
		map[string]any{"outcome": "success", "action": "deploy", "observation": "all good"})
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "zz qqq unrelated",
		map[string]any{"outcome": "success", "action": "write"})
	putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "deploy alpha service",
		map[string]any{"outcome": "success", "action": "deploy", "observation": "all good"})

	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.55)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClustersExamined != 1 || rep.PlaybooksCreated != 1 {
		t.Fatalf("report = %+v, want one cluster", rep)
	}
	playbooks, err := store.IterMemories("test", string(schema.LayerProcedural), string(schema.TypePlaybook))
	if err != nil {
		t.Fatal(err)
	}
	if len(playbooks) != 1 {
		t.Fatalf("playbooks = %d", len(playbooks))
	}
	if !strings.Contains(playbooks[0].Content, "deploy — all good") {
		t.Errorf("playbook steps should include the observation: %q", playbooks[0].Content)
	}
}

// --- fault injection ---------------------------------------------------------------

// selectiveEmbedder fails Embed and/or EmbedBatch on demand.
type selectiveEmbedder struct {
	inner     *storage.HashingEmbedding
	failEmbed bool
	failBatch bool
}

func (s *selectiveEmbedder) Embed(text string) ([]float32, error) {
	if s.failEmbed {
		return nil, errBoom
	}
	return s.inner.Embed(text)
}
func (s *selectiveEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	if s.failBatch {
		return nil, errBoom
	}
	return s.inner.EmbedBatch(texts)
}
func (s *selectiveEmbedder) Dim() int                    { return s.inner.Dim() }
func (s *selectiveEmbedder) HealthCheck() (bool, string) { return true, "ok" }

func TestRecallEmbedderAndStoreErrors(t *testing.T) {
	// embedder failure aborts the recall
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	bad := &selectiveEmbedder{inner: emb, failEmbed: true}
	if _, err := Recall(store, bad, "q", cfg, "test", 5, nil, nil, 0); !errors.Is(err, errBoom) {
		t.Errorf("embed err = %v, want errBoom", err)
	}

	// GetMemory failure on a vector hit propagates
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens", nil)
	store.Close()
	if _, err := Recall(store, emb, "auth jwt", cfg, "test", 5, nil, nil, 0); err == nil {
		t.Error("recall on a closed store with indexed hits should fail")
	}

	// with no indexed hits, NeighborCounts is the first failing store call
	store2, _ := newParityStore(t)
	store2.Close()
	if _, err := Recall(store2, emb, "auth jwt", cfg, "test", 5, nil, nil, 0); err == nil {
		t.Error("recall on a closed store should fail")
	}
}

func TestNegativeTopKPanics(t *testing.T) {
	// documents the current defensive behaviour: a negative topK slips past the
	// fetchK guard and panics on tier-1 truncation
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	defer func() {
		if recover() == nil {
			t.Error("Recall with negative topK should panic")
		}
	}()
	_, _ = Recall(store, emb, "q", cfg, "test", -1, nil, nil, 0)
}

func TestConsolidateFaults(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())

	t.Run("closed store", func(t *testing.T) {
		store, emb := newParityStore(t)
		store.Close()
		if _, err := Consolidate(store, emb, cfg, "test", nil, 0); err == nil {
			t.Error("consolidate on a closed store should fail")
		}
	})

	t.Run("embed batch failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "an episode", nil)
		bad := &selectiveEmbedder{inner: emb, failBatch: true}
		if _, err := Consolidate(store, bad, cfg, "test", nil, 0); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("add put-fact failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "an episode", nil)
		bad := &selectiveEmbedder{inner: emb, failEmbed: true}
		if _, err := Consolidate(store, bad, cfg, "test", nil, 0); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("update merge-embed failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		cfg := config.ForTesting(t.TempDir())
		cfg.Consolidate.DedupSimilarityThreshold = 0.0
		fact := newCustomMem(schema.LayerSemantic, schema.TypeFact, "auth uses jwt tokens", "test")
		fact.ContentHash = schema.ContentHash(fact.Content)
		putCustomMem(t, store, emb, fact)
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "auth uses jwt tokens too", nil)
		bad := &selectiveEmbedder{inner: emb, failEmbed: true}
		if _, err := Consolidate(store, bad, cfg, "test", nil, 0); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})
}

func TestConsolidateEpisodeCap(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	for i := 0; i < 501; i++ {
		m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
		m.Content = "episode"
		m.Workspace = "test"
		vec, err := emb.Embed(m.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := Consolidate(store, emb, cfg, "test", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.KeptEpisodes != 500 {
		t.Errorf("KeptEpisodes = %d, want 500 (capped)", rep.KeptEpisodes)
	}
}

func TestDecayClosedStore(t *testing.T) {
	store, _ := newParityStore(t)
	store.Close()
	if _, err := Decay(store, "test", nil, 0, 0, 0, false); err == nil {
		t.Error("decay on a closed store should fail")
	}
}

func TestAttentionClosedStore(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	store.Close()
	if _, err := AttentionGate("a real note", cfg, store, nilAgent, schema.LayerSemantic); err == nil {
		t.Error("attention gate on a closed store should fail")
	}
}

func TestPredictL6Faults(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())

	t.Run("closed store", func(t *testing.T) {
		store, emb := newParityStore(t)
		store.Close()
		if _, err := PredictL6(store, emb, cfg, "test", &fakeParityLLM{}, ""); err == nil {
			t.Error("PredictL6 on a closed store should fail")
		}
	})

	t.Run("intent embed failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putParityMem(t, store, emb, schema.LayerEpisodic, schema.TypeEvent, "episode", nil)
		llm := &fakeParityLLM{out: map[string]any{"intents": []any{
			map[string]any{"intent": "next step"},
		}}}
		bad := &selectiveEmbedder{inner: emb, failEmbed: true}
		if _, err := PredictL6(store, bad, cfg, "test", llm, ""); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})
}

func TestExtractL5Faults(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}

	t.Run("closed store", func(t *testing.T) {
		store, emb := newParityStore(t)
		store.Close()
		if _, err := ExtractL5(store, emb, cfg, "test", llm, ""); err == nil {
			t.Error("ExtractL5 on a closed store should fail")
		}
	})

	t.Run("extract embed-batch failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact", nil)
		bad := &selectiveEmbedder{inner: emb, failBatch: true}
		if _, err := ExtractL5(store, bad, cfg, "test", llm, ""); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("merge embed-batch failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		cfg := config.ForTesting(t.TempDir())
		cfg.System2.L5ClusterSimilarity = 1.1
		cfg.System2.L5MergeEveryNCycles = 1
		seedMergeableModels(t, store, emb)
		bad := &selectiveEmbedder{inner: emb, failBatch: true}
		if _, err := ExtractL5(store, bad, cfg, "test", llm, ""); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("store-model embed failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		cfg := config.ForTesting(t.TempDir())
		cfg.System2.L5ClusterSimilarity = 0.0
		cfg.System2.L5MinClusterSize = 2
		cfg.System2.L5MergeEveryNCycles = 0
		putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
		putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)
		bad := &selectiveEmbedder{inner: emb, failEmbed: true}
		if _, err := ExtractL5(store, bad, cfg, "test", llm, ""); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})
}

func TestProceduralizeFaults(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())

	t.Run("closed store", func(t *testing.T) {
		store, emb := newParityStore(t)
		store.Close()
		if _, err := Proceduralize(store, emb, cfg, "test", 0, 0); err == nil {
			t.Error("Proceduralize on a closed store should fail")
		}
	})

	t.Run("embed batch failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)
		bad := &selectiveEmbedder{inner: emb, failBatch: true}
		if _, err := Proceduralize(store, bad, cfg, "test", 2, 0.01); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})

	t.Run("candidate embed failure", func(t *testing.T) {
		store, emb := newParityStore(t)
		putSuccessEpisodes(t, store, emb, "deploy the service carefully", "deploy", "success", 3)
		bad := &selectiveEmbedder{inner: emb, failEmbed: true}
		if _, err := Proceduralize(store, bad, cfg, "test", 2, 0.01); !errors.Is(err, errBoom) {
			t.Errorf("err = %v, want errBoom", err)
		}
	})
}

func TestRetireAndLatestInChainClosedStore(t *testing.T) {
	store, emb := newParityStore(t)
	m := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "a fact", nil)
	store.Close()
	if err := Retire(store, m, ""); err == nil {
		t.Error("Retire on a closed store should fail")
	}
	if _, err := LatestInChain(store, m.ID); err == nil {
		t.Error("LatestInChain on a closed store should fail")
	}
}
