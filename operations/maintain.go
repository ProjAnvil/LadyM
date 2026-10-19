package operations

import (
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// MaintainReport is the outcome of one MaintainL4 pass (spec §8).
type MaintainReport struct {
	Swept      int    // dangling edges deleted (GC)
	Backfilled int    // episodes that got action_class and/or resource edges
	Linked     int    // related_to edges confirmed by the LLM
	Merged     int    // resource entities merged (alias merge)
	Skipped    string // why the LLM sub-steps were skipped ("" = ran)
}

// MaintainAgents bundles the per-op LLM providers for MaintainL4's LLM
// sub-steps. A nil field (or a nil *MaintainAgents) means that sub-step runs
// in heuristic/offline mode or is skipped.
type MaintainAgents struct {
	Backfill   providers.LLMProvider
	Link       providers.LLMProvider
	AliasMerge providers.LLMProvider
}

const backfillPrompt = `You extract structured operation slots from an episodic memory.
Return JSON: {"resources": [resource names the agent operated on], "action": "verb"}.
Use the canonical action vocabulary when possible; empty lists are fine.`

// MaintainL4 runs the worker-maintenance step of a System 2 cycle (spec §8):
//
//	① dangling-edge GC (zero LLM, every cycle)
//	② slot backfill for episodes missing action_class (heuristic, LLM optional)
//	③ semantic linking (Task 18)
//	④ alias merge (Task 19)
//
// GC always runs; the remaining sub-steps are gated on cfg.Maintain.Enabled.
func MaintainL4(store storage.Store, embedder storage.EmbeddingProvider,
	cfg *config.Config, ws string, agents *MaintainAgents) (*MaintainReport, error) {

	report := &MaintainReport{}

	// ① Dangling-edge GC.
	swept, err := store.SweepDanglingEdges()
	if err != nil {
		return nil, err
	}
	report.Swept = swept

	if !cfg.Maintain.Enabled {
		report.Skipped = "maintain_disabled"
		return report, nil
	}

	// ② Slot backfill.
	backfilled, err := backfillEpisodes(store, embedder, cfg, ws, agents)
	if err != nil {
		return nil, err
	}
	report.Backfilled = backfilled

	// ③④⑤ semantic linking / alias merge: Task 18/19.
	return report, nil
}

// backfillEpisodes stamps action_class (heuristic first, then the l4_backfill
// LLM op for the misses) and creates operates_on edges for LLM-extracted
// resources, capped at cfg.Maintain.BackfillBatch episodes per cycle.
func backfillEpisodes(store storage.Store, embedder storage.EmbeddingProvider,
	cfg *config.Config, ws string, agents *MaintainAgents) (int, error) {

	eps, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return 0, err
	}
	var candidates []*schema.Memory
	for _, m := range eps {
		if m.MetaString("action_class") == "" {
			candidates = append(candidates, m)
		}
	}
	if cap := cfg.Maintain.BackfillBatch; cap > 0 && len(candidates) > cap {
		candidates = candidates[:cap]
	}

	var backfill providers.LLMProvider
	if agents != nil {
		backfill = agents.Backfill
	}

	backfilled := 0
	for _, ep := range candidates {
		// Re-check for resource edges only over the truncated batch.
		neighbors, err := store.Neighbors(ep.ID, "operates_on")
		if err != nil {
			return backfilled, err
		}
		if len(neighbors) > 0 {
			continue
		}

		// Heuristic: exact vocabulary match on the raw action.
		if class := NormalizeAction(ep.MetaString("action"), cfg); class != "" {
			if err := stampActionClass(store, embedder, ep, class); err != nil {
				return backfilled, err
			}
			backfilled++
			continue
		}

		// LLM backfill for heuristic misses.
		if backfill == nil {
			continue
		}
		d, err := backfill.CompleteStructured([]providers.Message{
			{Role: "system", Content: backfillPrompt},
			{Role: "user", Content: ep.Content},
		}, providers.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"resources": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "resource names the episode operated on",
				},
				"action": map[string]any{
					"type": "string", "description": "canonical action verb",
				},
			},
			"required": []string{"resources", "action"},
		})
		if err != nil {
			return backfilled, err
		}
		class := NormalizeAction(asStringDefault(d["action"], ""), cfg)
		var resourceNames []string
		switch v := d["resources"].(type) {
		case []string:
			resourceNames = v
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					resourceNames = append(resourceNames, s)
				}
			}
		}

		changed := false
		if class != "" {
			if ep.Metadata == nil {
				ep.Metadata = map[string]any{}
			}
			ep.Metadata["action_class"] = class
			changed = true
		}
		for _, name := range resourceNames {
			res, _, err := ResolveResource(store, embedder, cfg, ws, name)
			if err != nil {
				return backfilled, err
			}
			e := schema.NewEdge(ep.ID, "operates_on", res.ID)
			e.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", res.ID)
			if err := store.PutEdge(e); err != nil {
				return backfilled, err
			}
			changed = true
		}
		if changed {
			// Rewrite with a freshly embedded content vector. scanMemory
			// never reads Memory.Embedding back from the DB, so reusing
			// ep.Embedding would NULL the embedding column and silently
			// drop the episode from vector search.
			vec, err := embedder.Embed(ep.Content)
			if err != nil {
				return backfilled, err
			}
			if err := store.PutMemory(ep, vec); err != nil {
				return backfilled, err
			}
			backfilled++
		}
	}
	return backfilled, nil
}

// stampActionClass writes action_class onto ep and rewrites the memory with
// a freshly embedded vector (never a stale or nil one).
func stampActionClass(store storage.Store, embedder storage.EmbeddingProvider,
	ep *schema.Memory, class string) error {
	if ep.Metadata == nil {
		ep.Metadata = map[string]any{}
	}
	ep.Metadata["action_class"] = class
	vec, err := embedder.Embed(ep.Content)
	if err != nil {
		return err
	}
	return store.PutMemory(ep, vec)
}

func asStringDefault(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}
