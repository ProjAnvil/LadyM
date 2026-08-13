// Package engine holds the Engine — the single orchestrator and entry point for
// the SDK / CLI / MCP. All front-ends call the same Engine so behaviour is
// identical everywhere.
package engine

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/layers"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const consolidatePrompt = "You classify a candidate fact against similar existing facts. " +
	"Reply with JSON {action, new_text}. action in ADD|UPDATE|DELETE|NOOP. " +
	"ADD=brand new; UPDATE=refines an existing one (set new_text); " +
	"DELETE=existing is now wrong; NOOP=duplicate."

// Engine is the top-level LadyM orchestrator.
type Engine struct {
	Config   *config.Config
	Provider storage.EmbeddingProvider
	Store    *storage.SQLiteStore

	Working     *layers.WorkingMemory
	Episodic    *layers.EpisodicMemory
	Semantic    *layers.SemanticMemory
	Procedural  *layers.ProceduralMemory
	Associative *layers.AssociativeMemory

	mu                  sync.Mutex
	llmClassify         operations.LLMClassifier
	llmClassifyResolved bool
	agents              map[string]providers.LLMProvider
}

// New builds an Engine from cfg (defaults to config.Default() when nil).
func New(cfg *config.Config) (*Engine, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	e := &Engine{Config: cfg, agents: map[string]providers.LLMProvider{}}

	provider, err := storage.MakeProvider(cfg)
	if err != nil {
		return nil, err
	}
	e.Provider = provider

	if err := e.ensureProviderDim(); err != nil {
		return nil, err
	}

	store, err := storage.NewStore(cfg.DBPath, e.Provider.Dim(), cfg.PreferSQLiteVec, cfg.EnableWAL)
	if err != nil {
		return nil, err
	}
	e.Store = store

	if err := e.enforceEmbeddingDim(); err != nil {
		store.Close()
		return nil, err
	}

	e.Working = layers.NewWorkingMemory(64, cfg.Workspace)
	e.Episodic = layers.NewEpisodicMemory(store, provider, cfg.Workspace)
	e.Semantic = layers.NewSemanticMemory(store, provider, cfg.Workspace)
	e.Procedural = layers.NewProceduralMemory(store, provider, cfg.Workspace)
	e.Associative = layers.NewAssociativeMemory(store)
	return e, nil
}

// Close releases the store.
func (e *Engine) Close() error {
	return e.Store.Close()
}

// SetWorkspace retargets the engine's default workspace (used by the MCP tools
// which accept a per-call workspace override).
func (e *Engine) SetWorkspace(ws string) {
	if ws == "" {
		return
	}
	e.Config.Workspace = ws
	e.Working = layers.NewWorkingMemory(64, ws)
	e.Episodic.Workspace = ws
	e.Semantic.Workspace = ws
	e.Procedural.Workspace = ws
}

func (e *Engine) ensureProviderDim() error {
	if e.Provider.Dim() != 0 {
		return nil
	}
	if _, err := e.Provider.Embed("dimensionality probe"); err != nil {
		return &storage.EmbeddingProviderError{Msg: fmt.Sprintf(
			"cannot determine embedding dimension for provider %q: %v", e.Config.EmbeddingProvider, err)}
	}
	return nil
}

func (e *Engine) enforceEmbeddingDim() error {
	stored, err := e.Store.GetMeta("embedding_dim")
	if err != nil {
		return err
	}
	actual := e.Provider.Dim()
	providerName := e.Config.EmbeddingProvider
	if stored == "" {
		if err := e.Store.SetMeta("embedding_dim", strconv.Itoa(actual)); err != nil {
			return err
		}
		return e.Store.SetMeta("embedding_provider", providerName)
	}
	storedDim, _ := strconv.Atoi(stored)
	if storedDim != actual {
		if e.Config.EmbeddingAllowDimChange {
			e.Store.RebuildVectorIndex(actual)
			if err := e.reembedAll(); err != nil {
				return err
			}
			if err := e.Store.SetMeta("embedding_dim", strconv.Itoa(actual)); err != nil {
				return err
			}
			return e.Store.SetMeta("embedding_provider", providerName)
		}
		return storage.AssertDimMatches(storedDim, actual)
	}
	return nil
}

func (e *Engine) reembedAll() error {
	all, err := e.Store.IterMemories("", "", "")
	if err != nil {
		return err
	}
	for _, m := range all {
		vec, err := e.Provider.Embed(m.Content)
		if err != nil {
			return err
		}
		if err := e.Store.PutMemory(m, vec); err != nil {
			return err
		}
	}
	return nil
}

// ---- agent wiring ----

func (e *Engine) makeAgent(op string) (providers.LLMProvider, error) {
	return providers.MakeAgent(e.Config, op)
}

// getAgent lazily builds + caches the LLM agent for one op. Returns nil for
// heuristic mode. A missing-key ConfigError propagates (fail-fast).
func (e *Engine) getAgent(op string) (providers.LLMProvider, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if a, ok := e.agents[op]; ok {
		return a, nil
	}
	a, err := e.makeAgent(op)
	if err != nil {
		return nil, err
	}
	e.agents[op] = a
	return a, nil
}

func makeClassifier(provider providers.LLMProvider) operations.LLMClassifier {
	return func(candidate string, similar []string) (operations.Action, string) {
		msgs := []providers.Message{
			{Role: "system", Content: consolidatePrompt},
			{Role: "user", Content: fmt.Sprintf("candidate: %s\nsimilar: %s", candidate, fmt.Sprint(similar))},
		}
		d, err := provider.CompleteStructured(msgs, `{"action": "ADD|UPDATE|DELETE|NOOP", "new_text": "string?"}`)
		if err != nil {
			return operations.ActionAdd, ""
		}
		action, _ := d["action"].(string)
		newText, _ := d["new_text"].(string)
		return operations.Action(action), newText
	}
}

// AttachLLMClassifier wires an explicit consolidation classifier. When fn is
// nil it builds the consolidate agent from config (offline → heuristic).
func (e *Engine) AttachLLMClassifier(fn operations.LLMClassifier) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.llmClassifyResolved = true
	if fn != nil {
		e.llmClassify = fn
		return nil
	}
	provider, err := e.makeAgent("consolidate")
	if err != nil {
		return err
	}
	if provider == nil {
		e.llmClassify = nil
		return nil
	}
	e.llmClassify = makeClassifier(provider)
	return nil
}

func (e *Engine) resolveLLMClassify() (operations.LLMClassifier, error) {
	e.mu.Lock()
	if e.llmClassifyResolved {
		e.mu.Unlock()
		return e.llmClassify, nil
	}
	e.mu.Unlock()

	provider, err := e.getAgent("consolidate")
	if err != nil {
		return nil, err
	}
	var classify operations.LLMClassifier
	if provider != nil {
		classify = makeClassifier(provider)
	}
	e.mu.Lock()
	e.llmClassify = classify
	e.llmClassifyResolved = true
	e.mu.Unlock()
	return classify, nil
}

// ---- write path ----

// Remember is the generic write, routing to the right layer. It returns an
// unpersisted Memory tagged gated=dedropped when the attention gate drops the
// content.
func (e *Engine) Remember(content string, layer schema.Layer, type_ schema.MemoryType, tags []string, metadata map[string]any, source, summary string) (*schema.Memory, error) {
	gate, err := operations.AttentionGate(content, e.Config, e.Store, e.getAgent, layer)
	if err != nil {
		return nil, err
	}
	if gate.Action == "drop" {
		meta := map[string]any{}
		for k, v := range metadata {
			meta[k] = v
		}
		meta["gated"] = "dropped"
		meta["reason"] = gate.Reason
		m := schema.NewMemory(layer, type_)
		m.Content = content
		m.Summary = summary
		m.Tags = tags
		m.Metadata = meta
		m.Source = source
		m.Workspace = e.Config.Workspace
		return m, nil
	}
	if gate.Action == "rewrite" && gate.Content != "" {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["gated"] = "rewritten"
		metadata["original"] = content
		content = gate.Content
	}

	switch layer {
	case schema.LayerWorking:
		return e.Working.Push(content, tags, metadata, source), nil
	case schema.LayerEpisodic:
		agent := source
		if agent == "" {
			agent = "user"
		}
		action := summary
		if action == "" {
			action = truncate80(content)
		}
		return e.Episodic.Record(agent, action, content, "", tags, metadata)
	case schema.LayerProcedural:
		if type_ == schema.TypeSnippet {
			title := summary
			if title == "" {
				title = "snippet"
			}
			return e.Procedural.PutSnippet(title, content, "python", tags)
		}
		name := summary
		if name == "" {
			name = truncate80(content)
		}
		return e.Procedural.PutPlaybook(name, splitLines(content), nil, "", tags)
	default:
		return e.Semantic.PutFact(content, summary, tags, metadata, source)
	}
}

// RecordEvent logs an L1 episodic event.
func (e *Engine) RecordEvent(agent, action, observation, outcome string, tags []string, metadata map[string]any) (*schema.Memory, error) {
	return e.Episodic.Record(agent, action, observation, outcome, tags, metadata)
}

// Link creates an associative edge.
func (e *Engine) Link(srcID, dstID, relation string) (*schema.Edge, error) {
	return e.Associative.Link(srcID, dstID, relation, 1.0, nil, nil, nil)
}

// ---- read path ----

// Recall runs two-tier retrieval.
func (e *Engine) Recall(query string, workspace string, topK int, layers_ []schema.Layer, types []schema.MemoryType, minSimilarity float64) (*schema.RecallResponse, error) {
	return operations.Recall(e.Store, e.Provider, query, e.Config, workspace, topK, layers_, types, minSimilarity)
}

// SearchCode is a code-only shortcut.
func (e *Engine) SearchCode(query string, topK int, workspace string) (*schema.RecallResponse, error) {
	return e.Recall(query, workspace, topK, []schema.Layer{schema.LayerSemantic},
		[]schema.MemoryType{schema.TypeCodeSymbol, schema.TypeCodeFile}, 0.01)
}

// ---- cognitive operations ----

// Consolidate promotes episodic events into semantic facts.
func (e *Engine) Consolidate(workspace string, since float64) (*operations.ConsolidationReport, error) {
	classify, err := e.resolveLLMClassify()
	if err != nil {
		return nil, err
	}
	return operations.Consolidate(e.Store, e.Provider, e.Config, workspace, classify, since)
}

// Proceduralize clusters successful episodes into playbooks.
func (e *Engine) Proceduralize(workspace string, minClusterSize int) (*operations.ProceduralizeReport, error) {
	return operations.Proceduralize(e.Store, e.Provider, e.Config, workspace, minClusterSize, 0)
}

// ExtractMentalModels runs L5 extraction (skipped when no LLM is configured).
func (e *Engine) ExtractMentalModels(workspace string) (*operations.L5ExtractionReport, error) {
	llm, err := e.getAgent("l5_mental_model")
	if err != nil {
		return nil, err
	}
	prompt := ""
	if ac, err := providers.NewAgentRegistry(e.Config).Get("l5_mental_model"); err == nil {
		prompt = ac.PromptTemplate
	}
	return operations.ExtractL5(e.Store, e.Provider, e.Config, workspace, llm, prompt)
}

// PredictForwardIntents runs L6 prediction (skipped when no LLM is configured).
func (e *Engine) PredictForwardIntents(workspace string) (*operations.L6PredictionReport, error) {
	llm, err := e.getAgent("l6_forward_intent")
	if err != nil {
		return nil, err
	}
	prompt := ""
	if ac, err := providers.NewAgentRegistry(e.Config).Get("l6_forward_intent"); err == nil {
		prompt = ac.PromptTemplate
	}
	return operations.PredictL6(e.Store, e.Provider, e.Config, workspace, llm, prompt)
}

// Decay forgets low-activation episodic events.
func (e *Engine) Decay(workspace string, dryRun bool, maxAgeS, activationFloor float64) (*operations.DecayReport, error) {
	if maxAgeS == 0 {
		maxAgeS = 30 * 24 * 3600.0
	}
	if activationFloor == 0 {
		activationFloor = 0.05
	}
	return operations.Decay(e.Store, workspace, &e.Config.Activation, maxAgeS, activationFloor, 0, dryRun)
}

// IndexCode indexes a codebase into L2 semantic memory.
func (e *Engine) IndexCode(root string, force bool, workspace string, languages []string) (*code.IndexReport, error) {
	return code.IndexCodebase(root, e.Store, e.Provider, e.Config, workspace, force, languages)
}

// Forget deletes a single memory by id.
func (e *Engine) Forget(memoryID string) error {
	return e.Store.DeleteMemory(memoryID)
}

// ---- System 2 ----

// CountRecentEpisodes counts episodic events in workspace.
func (e *Engine) CountRecentEpisodes(workspace string) (int, error) {
	ws := workspace
	if ws == "" {
		ws = e.Config.Workspace
	}
	eps, err := e.Store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return 0, err
	}
	return len(eps), nil
}

// MinEpisodesToRun returns the System 2 episode threshold.
func (e *Engine) MinEpisodesToRun() int { return e.Config.System2.MinEpisodesToRun }

// StartSystem2 launches a daemon goroutine running System2 cycles. Closing the
// returned channel asks the worker to stop after its current cycle.
func (e *Engine) StartSystem2(intervalS int, workspace string) chan struct{} {
	stop := make(chan struct{})
	interval := intervalS
	if interval == 0 {
		interval = e.Config.System2.IntervalS
	}
	maxErrs := e.Config.System2.MaxConsecutiveErrors

	workerCfg := *e.Config // shallow copy; nested structs are value types
	workerCfg.EnableWAL = true

	go func() {
		workerEng, err := New(&workerCfg)
		if err != nil {
			log.Printf("[ladym.system2] worker engine failed to start: %v", err)
			return
		}
		defer workerEng.Close()
		consecutiveErrs := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := operations.RunSystem2Cycle(workerEng, workspace); err != nil {
				consecutiveErrs++
				log.Printf("[ladym.system2] cycle failed (%d/%d consecutive): %v", consecutiveErrs, maxErrs, err)
				if consecutiveErrs >= maxErrs {
					log.Printf("[ladym.system2] worker stopping after %d consecutive failures", consecutiveErrs)
					return
				}
			} else {
				consecutiveErrs = 0
			}
			select {
			case <-stop:
				return
			case <-time.After(time.Duration(interval) * time.Second):
			}
		}
	}()
	return stop
}

// ---- introspection ----

// Stats returns aggregate store statistics.
func (e *Engine) Stats() (*schema.Stats, error) {
	counts, err := e.Store.Count(e.Config.Workspace)
	if err != nil {
		return nil, err
	}
	byLayer := map[string]int{}
	byType := map[string]int{}
	for k, n := range counts {
		layer, typ := splitCountKey(k)
		byLayer[layer] += n
		byType[typ] += n
	}
	nCodeSyms, err := countCodeSymbols(e.Store)
	if err != nil {
		return nil, err
	}
	mems, err := e.Store.IterMemories(e.Config.Workspace, "", "")
	if err != nil {
		return nil, err
	}
	totalTokens := 0
	for _, m := range mems {
		totalTokens += len(storage.Tokenize(m.Content))
	}
	avg := 0.0
	if len(mems) > 0 {
		avg = float64(totalTokens) / float64(len(mems))
	}
	workspaces, err := e.Store.Workspaces()
	if err != nil {
		return nil, err
	}
	edges, err := e.Store.CountEdges()
	if err != nil {
		return nil, err
	}
	total := 0
	for _, n := range byLayer {
		total += n
	}
	return &schema.Stats{
		TotalMemories: total, ByLayer: byLayer, ByType: byType,
		Edges: edges, CodeSymbols: nCodeSyms, Workspaces: workspaces,
		DBPath: e.Config.DBPath, AvgTokensPerMemory: avg,
	}, nil
}

func countCodeSymbols(store *storage.SQLiteStore) (int, error) {
	var n int
	err := store.DB().QueryRow("SELECT COUNT(*) FROM code_symbols").Scan(&n)
	return n, err
}

func splitCountKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

func truncate80(s string) string {
	r := []rune(s)
	if len(r) <= 80 {
		return s
	}
	return string(r[:80])
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
