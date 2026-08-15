package operations

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// --- helpers ---------------------------------------------------------------

func newParityStore(t *testing.T) (*storage.SQLiteStore, *storage.HashingEmbedding) {
	t.Helper()
	emb := storage.NewHashingEmbedding(256)
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "t.db"), emb.Dim(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, emb
}

func putParityMem(t *testing.T, store *storage.SQLiteStore, emb *storage.HashingEmbedding, layer schema.Layer, typ schema.MemoryType, content string, meta map[string]any) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(layer, typ)
	m.Content = content
	m.Metadata = meta
	m.Workspace = "test"
	vec, err := emb.Embed(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutMemory(m, vec); err != nil {
		t.Fatal(err)
	}
	return m
}

type fakeParityLLM struct {
	out     map[string]any
	gotUser string
}

func (f *fakeParityLLM) Name() string { return "fake" }
func (f *fakeParityLLM) Complete(messages []providers.Message) (string, error) {
	return "", nil
}
func (f *fakeParityLLM) CompleteStructured(messages []providers.Message, schemaDesc string) (map[string]any, error) {
	for _, m := range messages {
		if m.Role == "user" {
			f.gotUser = m.Content
		}
	}
	return f.out, nil
}
func (f *fakeParityLLM) Close() error { return nil }

// --- 1. system2 decay must not be a dry run --------------------------------

type fakeSystem2Runner struct {
	decayDryRun bool
	episodes    int
}

func (f *fakeSystem2Runner) Consolidate(workspace string, since float64) (*ConsolidationReport, error) {
	return &ConsolidationReport{}, nil
}
func (f *fakeSystem2Runner) Proceduralize(workspace string, minClusterSize int) (*ProceduralizeReport, error) {
	return newProceduralizeReport(), nil
}
func (f *fakeSystem2Runner) ExtractMentalModels(workspace string) (*L5ExtractionReport, error) {
	return &L5ExtractionReport{}, nil
}
func (f *fakeSystem2Runner) PredictForwardIntents(workspace string) (*L6PredictionReport, error) {
	return &L6PredictionReport{}, nil
}
func (f *fakeSystem2Runner) Decay(workspace string, dryRun bool, maxAgeS, activationFloor float64) (*DecayReport, error) {
	f.decayDryRun = dryRun
	return &DecayReport{}, nil
}
func (f *fakeSystem2Runner) CountRecentEpisodes(workspace string) (int, error) {
	return f.episodes, nil
}
func (f *fakeSystem2Runner) MinEpisodesToRun() int { return 3 }

func TestSystem2CycleDecayIsNotDryRun(t *testing.T) {
	r := &fakeSystem2Runner{}
	if _, err := RunSystem2Cycle(r, "test"); err != nil {
		t.Fatal(err)
	}
	if r.decayDryRun {
		t.Error("system2 cycle invoked Decay with dryRun=true; Python engine.decay() defaults to dry_run=False")
	}
}

// --- 2. L6 fixes ------------------------------------------------------------

func TestL6ExpireSweepMissingValidToRetires(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	// missing valid_to → treated as 0 → retired
	stale := putParityMem(t, store, emb, schema.LayerL6Predictive, schema.TypeForwardIntent, "stale intent", nil)
	// non-numeric valid_to → Python float() raises → skipped
	weird := putParityMem(t, store, emb, schema.LayerL6Predictive, schema.TypeForwardIntent, "weird intent", map[string]any{"valid_to": "not-a-number"})
	// future valid_to → kept
	fresh := putParityMem(t, store, emb, schema.LayerL6Predictive, schema.TypeForwardIntent, "fresh intent", map[string]any{"valid_to": schema.Now() + 10000})

	rep, err := PredictL6(store, emb, cfg, "test", &fakeParityLLM{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.ExpiredRetired != 1 {
		t.Errorf("ExpiredRetired = %d, want 1 (missing valid_to counts as 0)", rep.ExpiredRetired)
	}
	got, err := store.GetMemory(stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !IsRetired(got) {
		t.Error("memory without valid_to should be retired as expired")
	}
	for _, m := range []*schema.Memory{weird, fresh} {
		got, err := store.GetMemory(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if IsRetired(got) {
			t.Errorf("memory %q should not be retired", m.Content)
		}
	}
}

func TestL6StripsIntentText(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerEpisodic, "episode", "ran the test suite", nil)
	llm := &fakeParityLLM{out: map[string]any{"intents": []any{
		map[string]any{"intent": "   "},
		map[string]any{"intent": "  check the logs  ", "confidence": 0.9},
	}}}
	rep, err := PredictL6(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Predictions != 1 {
		t.Fatalf("Predictions = %d, want 1 (whitespace-only intent dropped)", rep.Predictions)
	}
	ms, err := store.IterMemories("test", string(schema.LayerL6Predictive), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Content != "check the logs" {
		t.Errorf("stored intent = %+v, want content trimmed to %q", ms, "check the logs")
	}
}

func TestL6CorpusHasNoTrailingNewline(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerEpisodic, "episode", "first episode", nil)
	putParityMem(t, store, emb, schema.LayerEpisodic, "episode", "second episode", nil)
	llm := &fakeParityLLM{out: map[string]any{"intents": []any{}}}
	if _, err := PredictL6(store, emb, cfg, "test", llm, ""); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(llm.gotUser, "\n") {
		t.Errorf("user message ends with newline; Python uses \"\\n\".join: %q", llm.gotUser)
	}
	if !strings.Contains(llm.gotUser, "- first episode\n- second episode") {
		t.Errorf("corpus lines not joined as expected: %q", llm.gotUser)
	}
}

func TestL5CorpusHasNoTrailingNewline(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.System2.L5ClusterSimilarity = 0.0 // force one cluster
	cfg.System2.L5MergeEveryNCycles = 0   // skip merge pass
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "alpha fact one", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "beta fact two", nil)
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "gamma fact three", nil)
	llm := &fakeParityLLM{out: map[string]any{"title": "t", "model": "m"}}
	rep, err := ExtractL5(store, emb, cfg, "test", llm, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewModels == 0 {
		t.Fatal("expected a mental model to be extracted")
	}
	if strings.HasSuffix(llm.gotUser, "\n") {
		t.Errorf("user message ends with newline; Python uses \"\\n\".join: %q", llm.gotUser)
	}
}

// --- 3. proceduralize top action ---------------------------------------------

func proceduralizeCluster(t *testing.T, actions []string) *ProceduralizeReport {
	t.Helper()
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	for _, a := range actions {
		meta := map[string]any{"outcome": "success"}
		if a != "" {
			meta["action"] = a
		}
		putParityMem(t, store, emb, schema.LayerEpisodic, "episode", "deploy the service carefully", meta)
	}
	rep, err := Proceduralize(store, emb, cfg, "test", 2, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Details) == 0 {
		t.Fatal("expected at least one cluster")
	}
	return rep
}

func TestProceduralizeMissingActionCountsAsDo(t *testing.T) {
	// 2 explicit "do" + 2 missing (→ "do") + 3 "write" → "do" wins 4:3.
	rep := proceduralizeCluster(t, []string{"do", "", "write", "do", "", "write", "write"})
	if got := rep.Details[0]["action_verb"]; got != "do" {
		t.Errorf("action_verb = %v, want %q (missing action metadata counts as \"do\")", got, "do")
	}
}

func TestProceduralizeTopActionTieBreaksByFirstOccurrence(t *testing.T) {
	// Counter.most_common keeps first-seen order on ties, not lexicographic.
	rep := proceduralizeCluster(t, []string{"zebra", "apple"})
	if got := rep.Details[0]["action_verb"]; got != "zebra" {
		t.Errorf("action_verb = %v, want %q (tie broken by first occurrence)", got, "zebra")
	}
}

// --- 4. attention: config noise words used as-is ------------------------------

func TestAttentionConfigNoiseWordsNotLowercased(t *testing.T) {
	store, _ := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.Attention.NoiseWords = []string{"MixedCase"}
	d, err := AttentionGate("mixedcase", cfg, store, func(string) (providers.LLMProvider, error) { return nil, nil }, schema.LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action == "drop" {
		t.Error("config noise word \"MixedCase\" must be used as-is (Python does not lowercase it); \"mixedcase\" should pass the noise check")
	}
}

// --- 5. recall fixes -----------------------------------------------------------

func TestRecallElapsedMsIsFloat(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "auth uses JWT tokens", nil)
	resp, err := Recall(store, emb, "auth tokens", cfg, "test", 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ElapsedMs <= 0 || resp.ElapsedMs == math.Trunc(resp.ElapsedMs) {
		t.Errorf("ElapsedMs = %v, want fractional float milliseconds (Python: (time.time()-start)*1000)", resp.ElapsedMs)
	}
}

func TestRecallBacktrackPullsFileMemory(t *testing.T) {
	store, emb := newParityStore(t)
	cfg := config.ForTesting(t.TempDir())
	putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeCodeSymbol,
		"func parseConfig parses toml config files", map[string]any{"file_path": "/x/config.go"})
	file := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeCodeFile,
		"zz qqq unrelated content", map[string]any{"file_path": "/x/config.go"})
	resp, err := Recall(store, emb, "parseConfig config", cfg, "test", 0, nil, nil, 0.3)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TierReached != 2 {
		t.Fatalf("TierReached = %d, want 2", resp.TierReached)
	}
	var found *schema.RecallResult
	for _, r := range resp.Results {
		if r.Memory.ID == file.ID {
			found = r
		}
	}
	if found == nil {
		t.Fatal("backtrack should pull the code_file memory for a seen code symbol")
	}
	if found.Tier != 2 {
		t.Errorf("file memory tier = %d, want 2", found.Tier)
	}
}
