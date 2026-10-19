package operations

import (
	"sort"

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

const linkPrompt = `You decide whether two episodic memories describe semantically related work.
Return JSON: {"link": bool, "relation": "short relation name (e.g. related_to)", "weight": 0.0-1.0}.
Use link=false when the memories are unrelated despite surface similarity.`

// bucketMemLimit caps how many memories per (layer, action_class) bucket take
// part in pairwise linking, guarding against O(n^2) blowups (spec §8.3: keep
// the most recent 100).
const bucketMemLimit = 100

// linkEpisodes is sub-step ③: gated semantic linking. Candidates are pairs of
// non-retired episodes that share a (layer, action_class) bucket, pass the
// cosine pre-filter, and fit the per-cycle LLM budget; only LLM-confirmed
// pairs get a related_to edge with a deterministic projected id. The bool
// result reports whether the gate blocked the sub-step entirely.
func linkEpisodes(store storage.Store, embedder storage.EmbeddingProvider,
	cfg *config.Config, ws string, agents *MaintainAgents) (int, bool, error) {

	var link providers.LLMProvider
	if agents != nil {
		link = agents.Link
	}
	if !cfg.Maintain.LinkingEnabled || link == nil {
		return 0, true, nil
	}

	eps, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return 0, false, err
	}
	if len(eps) < cfg.System2.MinEpisodesToRun {
		return 0, true, nil
	}

	// Bucket by (layer, action_class) so pairs never cross layers or
	// action classes (anti-drift, spec §8.3).
	buckets := map[string][]*schema.Memory{}
	var order []string // deterministic bucket iteration order
	for _, m := range eps {
		if IsRetired(m) {
			continue
		}
		ac := m.MetaString("action_class")
		if ac == "" {
			continue
		}
		key := string(m.Layer) + "\x00" + ac
		if _, seen := buckets[key]; !seen {
			order = append(order, key)
		}
		buckets[key] = append(buckets[key], m)
	}

	// Embed every candidate content once (EmbedBatch cache).
	contentVec := map[string][]float32{}
	var texts []string
	for _, key := range order {
		for _, m := range buckets[key] {
			if _, ok := contentVec[m.Content]; !ok {
				contentVec[m.Content] = nil
				texts = append(texts, m.Content)
			}
		}
	}
	if len(texts) > 0 {
		vecs, err := embedder.EmbedBatch(texts)
		if err != nil {
			return 0, false, err
		}
		for i, t := range texts {
			contentVec[t] = vecs[i]
		}
	}

	budget := cfg.Maintain.LinkingBudget
	linked := 0
	for _, key := range order {
		members := buckets[key]
		// Most recent first; ties keep iteration order (stable).
		sort.SliceStable(members, func(i, j int) bool {
			return members[i].CreatedAt > members[j].CreatedAt
		})
		if len(members) > bucketMemLimit {
			members = members[:bucketMemLimit]
		}
		for i := 0; i < len(members) && budget > 0; i++ {
			for j := i + 1; j < len(members) && budget > 0; j++ {
				a, b := members[i], members[j]
				if storage.CosineSimilarity(contentVec[a.Content], contentVec[b.Content]) < cfg.Maintain.LinkingSimilarity {
					continue
				}
				d, err := link.CompleteStructured([]providers.Message{
					{Role: "system", Content: linkPrompt},
					{Role: "user", Content: a.Content + "\n---\n" + b.Content},
				}, providers.JSONSchema{
					"type": "object",
					"properties": map[string]any{
						"link":     map[string]any{"type": "boolean"},
						"relation": map[string]any{"type": "string"},
						"weight":   map[string]any{"type": "number"},
					},
					"required": []string{"link"},
				})
				if err != nil {
					return linked, false, err
				}
				budget--
				if !asBoolDefault(d["link"], false) {
					continue
				}
				relation := asStringDefault(d["relation"], "")
				if relation == "" {
					relation = "related_to"
				}
				weight, _ := asFloatOk(d["weight"])
				e := schema.NewEdge(a.ID, relation, b.ID)
				e.ID = schema.ProjectedEdgeID(a.ID, relation, b.ID)
				if weight > 0 {
					e.Weight = weight
				}
				if err := store.PutEdge(e); err != nil {
					return linked, false, err
				}
				linked++
			}
		}
	}
	return linked, false, nil
}

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

	// ③ Semantic linking.
	linked, skipped, err := linkEpisodes(store, embedder, cfg, ws, agents)
	if err != nil {
		return nil, err
	}
	report.Linked = linked
	if skipped && report.Skipped == "" {
		report.Skipped = "linking"
	}

	// ④⑤ alias merge: Task 19.
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

func asBoolDefault(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch t {
		case "true", "yes", "1":
			return true
		case "false", "no", "0":
			return false
		}
	case float64:
		return t != 0
	}
	return def
}

func asFloatOk(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		return parseFloatOrZero(t), t != ""
	}
	return 0, false
}
