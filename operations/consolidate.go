package operations

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// Action is one of the four consolidation decisions (mem0's ADD/UPDATE/DELETE/NOOP).
type Action string

const (
	ActionAdd    Action = "ADD"
	ActionUpdate Action = "UPDATE"
	ActionDelete Action = "DELETE"
	ActionNoop   Action = "NOOP"
)

// ConsolidationReport reports the outcome of a consolidation pass.
type ConsolidationReport struct {
	Actions             map[string]int
	KeptEpisodes        int
	PromotedToSemantic  int
	SkippedConsolidated int
	Details             []map[string]any
}

func newConsolidationReport() *ConsolidationReport {
	return &ConsolidationReport{Actions: map[string]int{
		string(ActionAdd): 0, string(ActionUpdate): 0,
		string(ActionDelete): 0, string(ActionNoop): 0,
	}}
}

// LLMClassifier is a pluggable (candidate, similar) → (Action, newText) classifier.
// A non-nil error aborts the consolidation pass (Python: exceptions propagate).
type LLMClassifier func(candidate string, similar []string) (Action, string, error)

type similarFact struct {
	Memory *schema.Memory
	Sim    float64
}

// consolidatedAtKey stamps an episode once it has completed a consolidation
// pass, whatever the verdict. Selection skips stamped episodes, so the
// per-cycle cost tracks the backlog, not the store size — and unlike a
// caller-side `since` timestamp, the mark survives aborted passes: an
// episode a failed cycle never reached stays unstamped and is simply picked
// up by the next one.
const consolidatedAtKey = "consolidated_at"

// IsConsolidated reports whether the episode has completed a consolidation
// pass (stamped for every ADD/UPDATE/DELETE/NOOP verdict).
func IsConsolidated(m *schema.Memory) bool {
	v, ok := m.MetaFloat(consolidatedAtKey)
	return ok && v > 0
}

// Similarity is not authority to mutate evidence. Offline consolidation only
// suppresses exact live duplicates; every unequal candidate is additive.
func offlineClassify(candidate string, similar []similarFact) (Action, string) {
	for _, s := range similar {
		if schema.IsUsable(s.Memory) && s.Memory.Layer == schema.LayerSemantic &&
			(s.Memory.Type == schema.TypeFact || s.Memory.Type == schema.TypeNote) &&
			s.Memory.Content == candidate {
			return ActionNoop, ""
		}
	}
	return ActionAdd, ""
}

func guardMemory(batch *storage.MemoryBatch, m *schema.Memory) {
	for _, guard := range batch.Guards {
		if guard.Expected.ID == m.ID {
			return
		}
	}
	batch.Guards = append(batch.Guards, storage.MemoryGuard{Expected: m})
}

func sourceSnapshots(store storage.Store, m *schema.Memory) ([]*schema.Memory, error) {
	ids := map[string]bool{}
	if id := m.MetaString("source_episode"); id != "" {
		ids[id] = true
	}
	switch values := m.Meta("source_episodes").(type) {
	case []string:
		for _, id := range values {
			ids[id] = true
		}
	case []any:
		for _, value := range values {
			if id, ok := value.(string); ok {
				ids[id] = true
			}
		}
	}
	edges, err := store.Neighbors(m.ID, "produced_from")
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		if edge.SrcID == m.ID && edge.ValidTo == nil {
			ids[edge.DstID] = true
		}
	}
	var sources []*schema.Memory
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		source, err := store.GetMemory(id)
		if err != nil {
			return nil, err
		}
		if !schema.IsUsable(source) || source.Workspace != m.Workspace {
			return nil, fmt.Errorf("%w: unavailable source %s", storage.ErrMemoryConflict, id)
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// stageProvenance snapshots every source and resource before the transaction.
// Metadata and deterministic projections are committed with the derived item.
func stageProvenance(store storage.Store, batch *storage.MemoryBatch, derived *schema.Memory, sources []*schema.Memory, relation string) error {
	seen := map[string]bool{}
	var ids []string
	for _, source := range sources {
		if !schema.IsUsable(source) || source.Workspace != derived.Workspace {
			return fmt.Errorf("%w: unavailable source", storage.ErrMemoryConflict)
		}
		if seen[source.ID] {
			continue
		}
		seen[source.ID] = true
		ids = append(ids, source.ID)
		guardMemory(batch, source)
		edge := schema.NewEdge(derived.ID, relation, source.ID)
		edge.ID = schema.ProjectedEdgeID(derived.ID, relation, source.ID)
		batch.Edges = append(batch.Edges, edge)
		if relation != "produced_from" {
			continue
		}
		neighbors, err := store.Neighbors(source.ID, "operates_on")
		if err != nil {
			return err
		}
		for _, neighbor := range neighbors {
			if neighbor.ValidTo != nil {
				continue
			}
			id := neighbor.DstID
			if neighbor.SrcID != source.ID {
				id = neighbor.SrcID
			}
			resource, err := store.GetMemory(id)
			if err != nil {
				return err
			}
			if !schema.IsUsable(resource) || resource.Workspace != derived.Workspace {
				return fmt.Errorf("%w: unavailable resource %s", storage.ErrMemoryConflict, id)
			}
			guardMemory(batch, resource)
			projected := schema.NewEdge(derived.ID, "operates_on", id)
			projected.ID = schema.ProjectedEdgeID(derived.ID, "operates_on", id)
			batch.Edges = append(batch.Edges, projected)
		}
	}
	slices.Sort(ids)
	if relation == "produced_from" {
		derived.Metadata["source_episodes"] = ids
	}
	return nil
}

// projectProvenance writes the L4 provenance projections for a newly produced
// fact (spec §6.3): a produced_from edge fact→source-episode, plus an
// operates_on edge fact→resource for every resource that episode operated on.
// Edge ids are deterministic (ProjectedEdgeID), so re-running a projection
// upserts instead of duplicating.
func projectProvenance(store storage.Store, factID, epID string) error {
	e := schema.NewEdge(factID, "produced_from", epID)
	e.ID = schema.ProjectedEdgeID(factID, "produced_from", epID)
	if err := store.PutEdge(e); err != nil {
		return err
	}
	neighbors, err := store.Neighbors(epID, "operates_on")
	if err != nil {
		return err
	}
	for _, n := range neighbors {
		if n.ValidTo != nil {
			continue // retired edges carry no projection
		}
		dst := n.DstID
		if n.SrcID != epID {
			// operates_on edges point at resources; tolerate the reversed
			// orientation by taking whichever endpoint is not the episode.
			dst = n.SrcID
		}
		pe := schema.NewEdge(factID, "operates_on", dst)
		pe.ID = schema.ProjectedEdgeID(factID, "operates_on", dst)
		if err := store.PutEdge(pe); err != nil {
			return err
		}
	}
	return nil
}

// Consolidate promotes salient episodic events into semantic facts. Selection
// covers the workspace's unconsolidated episodes (see consolidatedAtKey);
// when since is non-zero it additionally bounds the scan to episodes created
// at or after that time, so 0 means "everything still pending".
func Consolidate(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, llmClassify LLMClassifier, since float64) (*ConsolidationReport, error) {
	ws := workspace
	ws = cmp.Or(ws, cfg.Workspace)
	report := newConsolidationReport()

	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	pending := episodes[:0]
	for _, e := range episodes {
		if !schema.IsUsable(e) || e.Type != schema.TypeEvent {
			continue
		}
		if IsConsolidated(e) {
			report.SkippedConsolidated++
			continue
		}
		if since != 0 && e.CreatedAt < since {
			continue
		}
		pending = append(pending, e)
	}
	episodes = pending
	slices.SortFunc(episodes, func(a, b *schema.Memory) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	if len(episodes) > 500 {
		episodes = episodes[:500]
	}

	contents := make([]string, len(episodes))
	for i, e := range episodes {
		contents[i] = e.Content
	}
	var candVecs [][]float32
	if len(contents) > 0 {
		candVecs, err = embedder.EmbedBatch(contents)
		if err != nil {
			return nil, err
		}
		if len(candVecs) != len(contents) {
			return nil, fmt.Errorf("consolidate: embedding batch returned %d vectors for %d episodes", len(candVecs), len(contents))
		}
	}

	for i, ep := range episodes {
		vec := candVecs[i]
		raw, err := store.SearchMemories(vec, cfg.Consolidate.MinEpisodesToTrigger+5, storage.MemoryFilter{
			Workspace: ws, Layers: []schema.Layer{schema.LayerSemantic},
			Types: []schema.MemoryType{schema.TypeFact, schema.TypeNote}, ExcludeUnusable: true,
		})
		if err != nil {
			return nil, err
		}
		var similar []similarFact
		for _, h := range raw {
			if h.Similarity < 0.1 {
				continue
			}
			m, err := store.GetMemory(h.ID)
			if err != nil {
				return nil, err
			}
			if !schema.IsUsable(m) || m.Workspace != ws || m.Layer != schema.LayerSemantic ||
				(m.Type != schema.TypeFact && m.Type != schema.TypeNote) {
				continue
			}
			similar = append(similar, similarFact{Memory: m, Sim: h.Similarity})
		}
		slices.SortFunc(similar, func(a, b similarFact) int {
			return cmp.Or(cmp.Compare(b.Sim, a.Sim), cmp.Compare(a.Memory.ID, b.Memory.ID))
		})
		if llmClassify == nil {
			for j, fact := range similar {
				if fact.Memory.Content == ep.Content {
					similar[0], similar[j] = similar[j], similar[0]
					break
				}
			}
		}
		var previousSources []*schema.Memory
		var sourceErr error
		if len(similar) > 0 {
			previousSources, sourceErr = sourceSnapshots(store, similar[0].Memory)
		}

		var action Action
		var newText string
		if llmClassify != nil {
			simTexts := make([]string, 0, len(similar))
			for _, s := range similar {
				simTexts = append(simTexts, s.Memory.Content)
			}
			var cerr error
			action, newText, cerr = llmClassify(ep.Content, simTexts)
			if cerr != nil {
				return nil, cerr
			}
		} else {
			action, newText = offlineClassify(ep.Content, similar)
		}

		switch action {
		case ActionAdd:
		case ActionUpdate, ActionDelete, ActionNoop:
			if len(similar) == 0 {
				return nil, fmt.Errorf("consolidate: %s requires a live fact/note target", action)
			}
		default:
			return nil, fmt.Errorf("consolidate: invalid action %q", action)
		}
		batch := storage.MemoryBatch{Guards: []storage.MemoryGuard{{Expected: ep, PendingEpisode: true}}}
		stamped := ep.Clone()
		if stamped.Metadata == nil {
			stamped.Metadata = map[string]any{}
		}
		stamped.Metadata[consolidatedAtKey] = schema.Now()
		batch.Writes = append(batch.Writes, storage.MemoryWrite{Memory: stamped, PreserveEmbedding: true})
		if action != ActionAdd {
			guardMemory(&batch, similar[0].Memory)
		}
		switch action {
		case ActionAdd:
			meta := map[string]any{}
			maps.Copy(meta, ep.Metadata)
			meta["source_episode"] = ep.ID
			addedContent := newText
			addedContent = cmp.Or(addedContent, ep.Content)
			meta["knowledge_status"] = "inferred"
			fact := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			fact.Content, fact.Summary = addedContent, cmp.Or(ep.Summary, truncateStr(addedContent, 80))
			fact.Tags, fact.Metadata = slices.Clone(ep.Tags), meta
			fact.Workspace, fact.Source = ws, "consolidate"
			fact.ContentHash = schema.ContentHash(addedContent)
			factVec, err := embedder.Embed(addedContent)
			if err != nil {
				return nil, err
			}
			if err := stageProvenance(store, &batch, fact, []*schema.Memory{ep}, "produced_from"); err != nil {
				return nil, err
			}
			batch.Writes = append(batch.Writes, storage.MemoryWrite{Memory: fact, Vector: factVec})
		case ActionUpdate:
			if sourceErr != nil {
				return nil, sourceErr
			}
			target := similar[0].Memory
			mergedContent := newText
			mergedContent = cmp.Or(mergedContent, ep.Content)
			merged := target.Clone()
			if merged.Metadata == nil {
				merged.Metadata = map[string]any{}
			}
			merged.ID = schema.NewID()
			merged.Content = mergedContent
			if ep.Summary != "" {
				merged.Summary = ep.Summary
			}
			merged.UpdatedAt = schema.Now()
			merged.ContentHash = schema.ContentHash(mergedContent)
			merged.Metadata["source_episode"] = ep.ID
			merged.Metadata["updated_from"] = target.ID
			merged.Metadata["knowledge_status"] = "inferred"
			vec, err := embedder.Embed(merged.Content)
			if err != nil {
				return nil, err
			}
			if err := stageProvenance(store, &batch, merged, append(previousSources, ep), "produced_from"); err != nil {
				return nil, err
			}
			batch.Writes = append(batch.Writes, storage.MemoryWrite{Memory: merged, Vector: vec})
			stageRetirement(&batch, target, merged.ID)
		case ActionDelete:
			stageRetirement(&batch, similar[0].Memory, "")
		}
		if err := store.ApplyMemoryBatch(batch); err != nil {
			return nil, err
		}
		report.Actions[string(action)]++
		if action == ActionAdd {
			report.PromotedToSemantic++
		}
		report.Details = append(report.Details, map[string]any{
			"episode_id": ep.ID, "action": string(action), "n_similar": len(similar),
		})
	}
	report.KeptEpisodes = len(episodes)
	return report, nil
}
