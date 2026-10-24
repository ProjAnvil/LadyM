package operations

import (
	"cmp"
	"slices"

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

const aliasMergePrompt = `You decide whether two resource entity names refer to the same real-world entity.
Return JSON: {"same_entity": bool}. Only confirm when the names are aliases of one entity.`

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
		slices.SortStableFunc(members, func(a, b *schema.Memory) int {
			return cmp.Compare(b.CreatedAt, a.CreatedAt)
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

	// ④ Alias merge.
	merged, err := mergeAliases(store, embedder, cfg, ws, agents)
	if err != nil {
		return nil, err
	}
	report.Merged = merged

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

// mergeAliases is sub-step ④: LLM-confirmed alias merging of resource
// entities (spec §8.4). Pairs of non-retired resources with cosine similarity
// >= cfg.Maintain.AliasMergeSimilarity are sent to the l4_alias_merge op;
// confirmed pairs merge into a winner (older entity wins, id as tie-break):
// the loser's aliases join the winner's, the loser's still-valid incoming
// operates_on edges are rebuilt to point at the winner (deterministic ids),
// and Retire closes the loser with a supersedes chain.
func mergeAliases(store storage.Store, embedder storage.EmbeddingProvider,
	cfg *config.Config, ws string, agents *MaintainAgents) (int, error) {

	var merge providers.LLMProvider
	if agents != nil {
		merge = agents.AliasMerge
	}
	if merge == nil {
		return 0, nil
	}

	res, err := store.IterMemories(ws, string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		return 0, err
	}
	var live []*schema.Memory
	for _, m := range res {
		if !IsRetired(m) {
			live = append(live, m)
		}
	}
	if len(live) < 2 {
		return 0, nil
	}

	vecs, err := embedder.EmbedBatch(contentsOf(live))
	if err != nil {
		return 0, err
	}

	merged := 0
	for i := range live {
		if IsRetired(live[i]) {
			continue // merged into an earlier winner this cycle
		}
		for j := i + 1; j < len(live); j++ {
			if IsRetired(live[i]) || IsRetired(live[j]) {
				continue
			}
			if storage.CosineSimilarity(vecs[i], vecs[j]) < cfg.Maintain.AliasMergeSimilarity {
				continue
			}
			d, err := merge.CompleteStructured([]providers.Message{
				{Role: "system", Content: aliasMergePrompt},
				{Role: "user", Content: live[i].Content + "\n---\n" + live[j].Content},
			}, providers.JSONSchema{
				"type": "object",
				"properties": map[string]any{
					"same_entity": map[string]any{"type": "boolean"},
				},
				"required": []string{"same_entity"},
			})
			if err != nil {
				return merged, err
			}
			if !asBoolDefault(d["same_entity"], false) {
				continue
			}

			winner, loser := mergeWinner(live[i], live[j])
			if err := mergeResourcePair(store, embedder, winner, loser); err != nil {
				return merged, err
			}
			merged++
		}
	}
	return merged, nil
}

// mergeWinner picks which entity survives: the older one (smaller
// CreatedAt), id as the tie-break.
func mergeWinner(a, b *schema.Memory) (winner, loser *schema.Memory) {
	switch {
	case a.CreatedAt < b.CreatedAt:
		return a, b
	case b.CreatedAt < a.CreatedAt:
		return b, a
	case a.ID < b.ID:
		return a, b
	default:
		return b, a
	}
}

// mergeResourcePair folds loser into winner (spec §8.4 steps 1-3).
func mergeResourcePair(store storage.Store, embedder storage.EmbeddingProvider,
	winner, loser *schema.Memory) error {

	// 1. Winner absorbs the loser's aliases (deduped).
	if winner.Metadata == nil {
		winner.Metadata = map[string]any{}
	}
	aliasesChanged := false
	for _, a := range resourceAliases(loser) {
		if appendAlias(winner, a) {
			aliasesChanged = true
		}
	}
	if aliasesChanged {
		// Rewrite with a freshly embedded vector — never a nil one
		// (scanMemory does not read Memory.Embedding back; a nil vector
		// would NULL the embedding column and drop the winner from
		// vector search).
		vec, err := embedder.Embed(winner.Content)
		if err != nil {
			return err
		}
		if err := store.PutMemory(winner, vec); err != nil {
			return err
		}
	}

	// 2. Rebuild the loser's still-valid incoming operates_on edges to point
	// at the winner (deterministic projected ids, so re-runs upsert).
	neighbors, err := store.Neighbors(loser.ID, "operates_on")
	if err != nil {
		return err
	}
	for _, e := range neighbors {
		if e.ValidTo != nil || e.DstID != loser.ID {
			continue
		}
		ne := schema.NewEdge(e.SrcID, "operates_on", winner.ID)
		ne.ID = schema.ProjectedEdgeID(e.SrcID, "operates_on", winner.ID)
		if err := store.PutEdge(ne); err != nil {
			return err
		}
	}

	// 3. Retire the loser (closes its still-valid edges both directions and
	// leaves a supersedes chain); the rebuilt edges above do not touch the
	// loser, so they survive.
	return Retire(store, loser, winner.ID)
}

func contentsOf(ms []*schema.Memory) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Content
	}
	return out
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
