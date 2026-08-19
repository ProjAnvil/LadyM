package engine

// Engine lifecycle and Remember write-path tests: constructor error paths,
// embedding-dimension transitions, remaining Remember routing branches, and
// System2 worker stop conditions.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// ---- small helpers ----

func TestTruncate80(t *testing.T) {
	short := "short string"
	if got := truncate80(short); got != short {
		t.Errorf("truncate80(%q) = %q, want unchanged", short, got)
	}
	long := strings.Repeat("a", 100)
	if got := truncate80(long); len([]rune(got)) != 80 {
		t.Errorf("truncate80(100 runes) = %d runes, want 80", len([]rune(got)))
	}
	// multi-byte runes must not be split
	mb := strings.Repeat("界", 100)
	if got := truncate80(mb); len([]rune(got)) != 80 {
		t.Errorf("truncate80(100 wide runes) = %d runes, want 80", len([]rune(got)))
	}
}

func TestSplitLines(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a\nb\nc", []string{"a", "b", "c"}},
		{"a\nb\n", []string{"a", "b"}}, // trailing newline: no empty final line
	}
	for _, tc := range cases {
		got := splitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("splitLines(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("splitLines(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestSplitCountKey(t *testing.T) {
	layer, typ := splitCountKey("L2_semantic/fact")
	if layer != "L2_semantic" || typ != "fact" {
		t.Errorf("splitCountKey with slash = (%q, %q)", layer, typ)
	}
	layer, typ = splitCountKey("noslash")
	if layer != "noslash" || typ != "" {
		t.Errorf("splitCountKey without slash = (%q, %q), want (noslash, \"\")", layer, typ)
	}
}

// ---- constructor error paths ----

func TestNewWithUnknownEmbeddingProvider(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingProvider = "bogus-provider"
	if _, err := New(cfg); err == nil {
		t.Fatal("New with unknown embedding provider should fail")
	}
}

func TestNewWithInvalidDBPath(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	// A path inside a regular file: MkdirAll cannot create the parent dir.
	blocker := filepath.Join(t.TempDir(), "afile")
	writeFile(t, blocker, "x")
	cfg.DBPath = filepath.Join(blocker, "ladym.db")
	if _, err := New(cfg); err == nil {
		t.Fatal("New with un-creatable db path should fail")
	}
}

// deferredDimEmbedder reports Dim()==0 (like Ollama before the first call) and
// can be made to fail the dimensionality probe.
type deferredDimEmbedder struct {
	inner *storage.HashingEmbedding
	err   error
}

func (d *deferredDimEmbedder) Dim() int { return 0 }

func (d *deferredDimEmbedder) Embed(text string) ([]float32, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.inner.Embed(text)
}

func (d *deferredDimEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		v, err := d.Embed(text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (d *deferredDimEmbedder) HealthCheck() (bool, string) { return d.inner.HealthCheck() }

func TestEnsureProviderDimProbeError(t *testing.T) {
	boom := errors.New("probe boom")
	emb := &deferredDimEmbedder{inner: storage.NewHashingEmbedding(32), err: boom}
	_, err := NewWithModels(config.ForTesting(t.TempDir()), &adapter.ModelRouting{Embedding: emb})
	if err == nil {
		t.Fatal("New with failing dimensionality probe should fail")
	}
	var provErr *storage.EmbeddingProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("err = %v, want EmbeddingProviderError", err)
	}
}

func TestEnsureProviderDimProbeSuccess(t *testing.T) {
	emb := &deferredDimEmbedder{inner: storage.NewHashingEmbedding(32)}
	eng, err := NewWithModels(config.ForTesting(t.TempDir()), &adapter.ModelRouting{Embedding: emb})
	if err != nil {
		t.Fatal(err)
	}
	eng.Close()
}

// ---- embedding-dim transitions on reopen ----

func reopenWithDim(t *testing.T, cfg *config.Config, dim int) (*Engine, error) {
	t.Helper()
	return NewWithModels(cfg, &adapter.ModelRouting{Embedding: storage.NewHashingEmbedding(dim)})
}

func TestReopenWithDimMismatchRejected(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := reopenWithDim(t, cfg, 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Remember("dim mismatch fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	_, err = reopenWithDim(t, cfg, 64) // EmbeddingAllowDimChange defaults to false
	if err == nil {
		t.Fatal("reopen with a different dim should fail")
	}
	var mismatch *storage.EmbeddingDimensionMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("err = %v, want EmbeddingDimensionMismatch", err)
	}
}

func TestReopenWithDimChangeAllowedReembeds(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingAllowDimChange = true
	eng, err := reopenWithDim(t, cfg, 32)
	if err != nil {
		t.Fatal(err)
	}
	m, err := eng.Remember("reembed me after the dim change", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	eng.Close()

	eng2, err := reopenWithDim(t, cfg, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()

	// The stored dim metadata follows the new provider.
	stored, err := eng2.Store.GetMeta("embedding_dim")
	if err != nil {
		t.Fatal(err)
	}
	if stored != "64" {
		t.Errorf("embedding_dim meta = %q, want 64", stored)
	}
	// The old memory survived and was re-embedded at the new dim.
	resp, err := eng2.Recall("reembed dim change", "", 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resp.Results {
		if r.Memory.ID == m.ID {
			found = true
		}
	}
	if !found {
		t.Error("memory missing after dim-change re-embed")
	}
}

// ---- Remember routing branches ----

func TestRememberWorkingLayer(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("scratch note", schema.LayerWorking, schema.TypeNote, []string{"tmp"}, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Layer != schema.LayerWorking {
		t.Errorf("layer = %s, want working", m.Layer)
	}
	if eng.Working.Len() != 1 {
		t.Errorf("working memory len = %d, want 1", eng.Working.Len())
	}
}

func TestRememberEpisodicDefaults(t *testing.T) {
	eng := newTestEngine(t)
	// No source/summary: agent defaults to "user", action to truncate80(content).
	content := strings.Repeat("episode content ", 10) // > 80 runes
	m, err := eng.Remember(content, schema.LayerEpisodic, schema.TypeEvent, nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Layer != schema.LayerEpisodic {
		t.Errorf("layer = %s, want episodic", m.Layer)
	}
	if m.MetaString("agent") != "user" {
		t.Errorf("agent = %q, want user", m.MetaString("agent"))
	}
	if action := m.MetaString("action"); len([]rune(action)) > 80 {
		t.Errorf("action = %q (%d runes), want <= 80", action, len([]rune(action)))
	}
}

func TestRememberProceduralPlaybook(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("step one\nstep two\nstep three", schema.LayerProcedural, schema.TypePlaybook, nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != schema.TypePlaybook || m.Layer != schema.LayerProcedural {
		t.Errorf("got layer=%s type=%s, want procedural/playbook", m.Layer, m.Type)
	}
	var nSteps int
	switch s := m.Metadata["steps"].(type) {
	case []string:
		nSteps = len(s)
	case []any:
		nSteps = len(s)
	}
	if nSteps != 3 {
		t.Errorf("playbook steps = %v, want 3 entries", m.Metadata["steps"])
	}
}

func TestRememberRewriteGate(t *testing.T) {
	gate := &providers.FakeLLMProvider{
		StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
			return map[string]any{"action": "rewrite", "content": "cleaned fact", "reason": "wordy"}, nil
		},
	}
	routing := &adapter.ModelRouting{Embedding: storage.NewHashingEmbedding(32), AttentionGate: gate}
	eng, err := NewWithModels(config.ForTesting(t.TempDir()), routing)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	m, err := eng.Remember("um well like the deploy uses helm charts yeah", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Content != "cleaned fact" {
		t.Errorf("content = %q, want rewritten", m.Content)
	}
	if m.MetaString("gated") != "rewritten" {
		t.Errorf("gated = %q, want rewritten", m.MetaString("gated"))
	}
}

func TestRememberGateErrorPropagates(t *testing.T) {
	boom := errors.New("gate boom")
	gate := &providers.FakeLLMProvider{
		StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
			return nil, boom
		},
	}
	routing := &adapter.ModelRouting{Embedding: storage.NewHashingEmbedding(32), AttentionGate: gate}
	eng, err := NewWithModels(config.ForTesting(t.TempDir()), routing)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if _, err := eng.Remember("a substantive fact worth keeping", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); !errors.Is(err, boom) {
		t.Fatalf("Remember err = %v, want wrapped %v", err, boom)
	}
}

// ---- classifier wiring ----

func TestAttachLLMClassifierNilOffline(t *testing.T) {
	eng := newTestEngine(t) // LLMProvider "none" → heuristic mode
	if err := eng.AttachLLMClassifier(nil); err != nil {
		t.Fatal(err)
	}
	eng.mu.Lock()
	resolved := eng.llmClassifyResolved
	classify := eng.llmClassify
	eng.mu.Unlock()
	if !resolved {
		t.Error("llmClassifyResolved = false after AttachLLMClassifier(nil)")
	}
	if classify != nil {
		t.Error("offline mode must keep the heuristic (nil) classifier")
	}
}

func TestAttachLLMClassifierNilBuildsFromRouting(t *testing.T) {
	boom := errors.New("structured boom")
	classifier := &providers.FakeLLMProvider{
		StructuredFn: func(_ []providers.Message, _ providers.JSONSchema) (map[string]any, error) {
			return nil, boom
		},
	}
	routing := &adapter.ModelRouting{Embedding: storage.NewHashingEmbedding(32), Consolidate: classifier}
	eng, err := NewWithModels(config.ForTesting(t.TempDir()), routing)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if err := eng.AttachLLMClassifier(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.RecordEvent("bot", "did a thing", "obs", "success", nil, nil); err != nil {
		t.Fatal(err)
	}
	// The built classifier wraps the injected provider, whose error must
	// propagate through Consolidate (makeClassifier error branch).
	if _, err := eng.Consolidate("", 0); !errors.Is(err, boom) {
		t.Fatalf("Consolidate err = %v, want wrapped %v", err, boom)
	}
}

// ---- L5/L6 agent resolution errors ----

func TestExtractMentalModelsMissingKey(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.LLMProvider = "openai"
	cfg.LLMAPIKeyEnv = "LADYM_TEST_DEFINITELY_UNSET_KEY"
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if _, err := eng.ExtractMentalModels(""); err == nil {
		t.Fatal("ExtractMentalModels with an unresolvable agent should fail")
	}
	if _, err := eng.PredictForwardIntents(""); err == nil {
		t.Fatal("PredictForwardIntents with an unresolvable agent should fail")
	}
}

// ---- misc engine methods ----

func TestSetWorkspaceEmptyIsNoop(t *testing.T) {
	eng := newTestEngine(t)
	before := eng.Config.Workspace
	eng.SetWorkspace("")
	if eng.Config.Workspace != before {
		t.Errorf("SetWorkspace(\"\") changed workspace to %q", eng.Config.Workspace)
	}
}

func TestForgetRemovesMemory(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("fact to forget", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Forget(m.ID); err != nil {
		t.Fatal(err)
	}
	got, err := eng.Store.GetMemory(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("forgotten memory should be gone")
	}
}

func TestCountRecentEpisodesExplicitWorkspace(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.RecordEvent("bot", "act", "obs", "success", nil, nil); err != nil {
		t.Fatal(err)
	}
	n, err := eng.CountRecentEpisodes(eng.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	n, err = eng.CountRecentEpisodes("empty-ws")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count in empty workspace = %d, want 0", n)
	}
}

// ---- store-level error branches (closed store) ----

func TestStoreErrorsAfterClose(t *testing.T) {
	eng, err := New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Remember("doomed fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	if _, err := eng.Stats(); err == nil {
		t.Error("Stats on a closed store should fail")
	}
	if _, err := eng.List("", nil, 10, 0); err == nil {
		t.Error("List on a closed store should fail")
	}
	if _, err := eng.CountRecentEpisodes(""); err == nil {
		t.Error("CountRecentEpisodes on a closed store should fail")
	}
}

// ---- System2 worker startup failure ----

func TestStartSystem2WorkerEngineFails(t *testing.T) {
	eng := newTestEngine(t)
	// Break the db path so the worker's engine constructor fails; the worker
	// must log and exit instead of panicking or hanging.
	blocker := filepath.Join(t.TempDir(), "afile")
	writeFile(t, blocker, "x")
	eng.Config.DBPath = filepath.Join(blocker, "ladym.db")

	stop := eng.StartSystem2(1, eng.Config.Workspace)
	time.Sleep(100 * time.Millisecond) // let the worker attempt startup
	close(stop)
	time.Sleep(50 * time.Millisecond)

	// The foreground engine is untouched by the worker's failure.
	if _, err := eng.Recall("anything", "", 5, nil, nil, 0); err != nil {
		t.Fatalf("recall after failed worker: %v", err)
	}
}

// ---- second pass: remaining branches ----

func TestNewWithNilConfig(t *testing.T) {
	// New(nil) falls back to config.Default(); redirect its db path into a
	// temp dir so the test stays hermetic.
	t.Setenv("LADYM_DB", filepath.Join(t.TempDir(), "nilcfg.db"))
	eng, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	eng.Close()
}

func TestAttachLLMClassifierAgentError(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.LLMProvider = "openai"
	cfg.LLMAPIKeyEnv = "LADYM_TEST_DEFINITELY_UNSET_KEY"
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// fn == nil builds the consolidate agent from config; the missing API key
	// must propagate (fail-fast).
	if err := eng.AttachLLMClassifier(nil); err == nil {
		t.Fatal("AttachLLMClassifier(nil) with an unresolvable agent should fail")
	}
}

func TestRememberDropsNoiseWithMetadata(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("lol", schema.LayerSemantic, schema.TypeFact, nil,
		map[string]any{"origin": "chat"}, "cli", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.MetaString("gated") != "dropped" {
		t.Errorf("gated = %q, want dropped", m.MetaString("gated"))
	}
	// caller metadata survives onto the unpersisted, gated memory
	if m.Metadata["origin"] != "chat" {
		t.Errorf("metadata = %v, want origin=chat copied through", m.Metadata)
	}
}

func TestRememberProceduralSnippetDefaultTitle(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("print('hi')", schema.LayerProcedural, schema.TypeSnippet, nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Summary != "snippet" {
		t.Errorf("summary = %q, want the default title %q", m.Summary, "snippet")
	}
}

// failingEmbedder has a fixed dim but fails every Embed call.
type failingEmbedder struct {
	dim int
	err error
}

func (f *failingEmbedder) Dim() int { return f.dim }

func (f *failingEmbedder) Embed(string) ([]float32, error) { return nil, f.err }

func (f *failingEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	return nil, f.err
}

func (f *failingEmbedder) HealthCheck() (bool, string) { return false, f.err.Error() }

func TestReopenDimChangeReembedFailure(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingAllowDimChange = true
	eng, err := reopenWithDim(t, cfg, 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Remember("will not survive the re-embed", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	// Re-embedding with a broken provider must abort the reopen with the
	// embed error, not silently wipe the store.
	boom := errors.New("embed boom")
	_, err = NewWithModels(cfg, &adapter.ModelRouting{Embedding: &failingEmbedder{dim: 64, err: boom}})
	if !errors.Is(err, boom) {
		t.Fatalf("reopen err = %v, want wrapped %v", err, boom)
	}
}

func TestStatsForDefaultsAndTableErrors(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.Remember("stats fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	// empty workspace falls back to the engine default
	st, err := eng.StatsFor("")
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalMemories != 1 {
		t.Errorf("total = %d, want 1", st.TotalMemories)
	}

	// a missing projection table surfaces as an error, not a panic
	backdoorExec(t, eng.Config.DBPath, "DROP TABLE code_symbols")
	if _, err := eng.Stats(); err == nil {
		t.Error("Stats with dropped code_symbols table should fail")
	}
}

func TestStatsForEdgesTableDropped(t *testing.T) {
	eng := newTestEngine(t)
	backdoorExec(t, eng.Config.DBPath, "DROP TABLE edges")
	if _, err := eng.Stats(); err == nil {
		t.Error("Stats with dropped edges table should fail")
	}
}

func TestListEqualTimestampsTiebreak(t *testing.T) {
	eng := newTestEngine(t)
	var ids []string
	for _, c := range []string{"tie fact one", "tie fact two"} {
		m, err := eng.Remember(c, schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	// force identical UpdatedAt so the sort hits the id tiebreak
	backdoorExec(t, eng.Config.DBPath, "UPDATE memories SET updated_at = 12345.0")
	got, err := eng.List("", nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d memories, want 2", len(got))
	}
	if got[0].ID <= got[1].ID {
		t.Errorf("tiebreak should order by id desc: %s then %s", got[0].ID, got[1].ID)
	}
}

// ---- System2 worker loop branches ----

func TestStartSystem2StopsBeforeFirstCycle(t *testing.T) {
	eng := newTestEngine(t)
	stop := eng.StartSystem2(60, eng.Config.Workspace)
	close(stop) // closed before the worker's first select → immediate return
	time.Sleep(100 * time.Millisecond)
}

func TestStartSystem2StopsAfterConsecutiveErrors(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.LLMProvider = "openai" // worker cycles fail: consolidate agent unresolvable
	cfg.LLMAPIKeyEnv = "LADYM_TEST_DEFINITELY_UNSET_KEY"
	cfg.System2.MaxConsecutiveErrors = 1
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	stop := eng.StartSystem2(1, eng.Config.Workspace)
	time.Sleep(300 * time.Millisecond) // first cycle fails → worker gives up
	close(stop)
	time.Sleep(50 * time.Millisecond)

	if _, err := eng.Recall("anything", "", 5, nil, nil, 0); err != nil {
		t.Fatalf("recall after worker stopped: %v", err)
	}
}

func TestStartSystem2IntervalElapses(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EnableWAL = true // main + worker engine share the db file
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	stop := eng.StartSystem2(1, eng.Config.Workspace) // 1s interval
	time.Sleep(2200 * time.Millisecond)               // let the timer fire at least once
	close(stop)
	time.Sleep(100 * time.Millisecond)

	if _, err := eng.Recall("anything", "", 5, nil, nil, 0); err != nil {
		t.Fatalf("recall after worker ticks: %v", err)
	}
}
