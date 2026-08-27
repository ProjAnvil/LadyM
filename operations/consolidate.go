package operations

import (
	"sort"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/layers"
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

// markConsolidated stamps the pass time back onto the episode, reusing the
// candidate vector so the embedding never drifts. UpdatedAt is deliberately
// left alone: the stamp is bookkeeping, not a content change, and must not
// refresh the episode's recency in activation scoring.
func markConsolidated(store storage.Store, ep *schema.Memory, vec []float32) error {
	if ep.Metadata == nil {
		ep.Metadata = map[string]any{}
	}
	ep.Metadata[consolidatedAtKey] = schema.Now()
	return store.PutMemory(ep, vec)
}

// offlineClassify compares a candidate against similar facts without an LLM.
func offlineClassify(candidate, candidateHash string, similar []similarFact, threshold float64) (Action, string) {
	for _, s := range similar {
		if s.Memory.ContentHash != "" && s.Memory.ContentHash == candidateHash {
			return ActionNoop, ""
		}
		if s.Memory.MetaString("superseded_by") == candidateHash {
			return ActionUpdate, ""
		}
	}
	if len(similar) > 0 && similar[0].Sim >= threshold {
		return ActionUpdate, candidate
	}
	return ActionAdd, ""
}

// Consolidate promotes salient episodic events into semantic facts. Selection
// covers the workspace's unconsolidated episodes (see consolidatedAtKey);
// when since is non-zero it additionally bounds the scan to episodes created
// at or after that time, so 0 means "everything still pending".
func Consolidate(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, llmClassify LLMClassifier, since float64) (*ConsolidationReport, error) {
	ws := workspace
	if ws == "" {
		ws = cfg.Workspace
	}
	sem := layers.NewSemanticMemory(store, embedder, ws)
	threshold := cfg.Consolidate.DedupSimilarityThreshold
	report := newConsolidationReport()

	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	pending := episodes[:0]
	for _, e := range episodes {
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
	sort.SliceStable(episodes, func(a, b int) bool { return episodes[a].CreatedAt < episodes[b].CreatedAt })
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
	}

	for i, ep := range episodes {
		vec := candVecs[i]
		raw := store.VectorSearch(vec, cfg.Consolidate.MinEpisodesToTrigger+5)
		var similar []similarFact
		for _, h := range raw {
			if h.Similarity < 0.1 {
				continue
			}
			m, err := store.GetMemory(h.ID)
			if err != nil {
				return nil, err
			}
			if m == nil || m.Workspace != ws || m.Layer != schema.LayerSemantic {
				continue
			}
			similar = append(similar, similarFact{Memory: m, Sim: h.Similarity})
		}
		sort.SliceStable(similar, func(a, b int) bool { return similar[a].Sim > similar[b].Sim })

		candHash := schema.ContentHash(ep.Content)
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
			action, newText = offlineClassify(ep.Content, candHash, similar, threshold)
		}

		report.Actions[string(action)]++
		report.Details = append(report.Details, map[string]any{
			"episode_id": ep.ID, "action": string(action), "n_similar": len(similar),
		})

		switch action {
		case ActionAdd:
			meta := map[string]any{}
			for k, v := range ep.Metadata {
				meta[k] = v
			}
			meta["source_episode"] = ep.ID
			addedContent := newText
			if addedContent == "" {
				addedContent = ep.Content
			}
			if _, err := sem.PutFact(addedContent, ep.Summary, ep.Tags, meta, "consolidate"); err != nil {
				return nil, err
			}
			report.PromotedToSemantic++
		case ActionUpdate:
			if len(similar) == 0 {
				continue
			}
			target := similar[0].Memory
			mergedContent := newText
			if mergedContent == "" {
				mergedContent = ep.Content
			}
			merged := target.Clone()
			merged.ID = schema.NewID()
			merged.Content = mergedContent
			if ep.Summary != "" {
				merged.Summary = ep.Summary
			}
			merged.UpdatedAt = schema.Now()
			merged.ContentHash = schema.ContentHash(mergedContent)
			merged.Metadata["source_episode"] = ep.ID
			merged.Metadata["updated_from"] = target.ID
			vec, err := embedder.Embed(merged.Content)
			if err != nil {
				return nil, err
			}
			if err := store.PutMemory(merged, vec); err != nil {
				return nil, err
			}
			if err := Retire(store, target, merged.ID); err != nil {
				return nil, err
			}
		case ActionDelete:
			if len(similar) > 0 {
				if err := Retire(store, similar[0].Memory, ""); err != nil {
					return nil, err
				}
			}
		}

		// Any verdict counts as processed: stamp the episode so selection
		// stops revisiting it (and stop re-embedding / re-classifying it).
		if err := markConsolidated(store, ep, vec); err != nil {
			return nil, err
		}
	}
	report.KeptEpisodes = len(episodes)
	return report, nil
}
