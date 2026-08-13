package operations

import (
	"sort"
	"strings"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type reflectionVerdict struct {
	sufficient bool
	coverage   float64
	nHits      int
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true,
	"to": true, "of": true, "and": true, "or": true,
}

func reflect(query string, hits []*schema.RecallResult, cfg config.RecallConfig) reflectionVerdict {
	qTokens := map[string]bool{}
	for _, t := range storage.Tokenize(query) {
		if !stopwords[t] {
			qTokens[t] = true
		}
	}
	if len(qTokens) == 0 {
		return reflectionVerdict{sufficient: true, coverage: 1.0, nHits: len(hits)}
	}
	var corpus strings.Builder
	for _, r := range hits {
		corpus.WriteString(r.Memory.Content)
		corpus.WriteString(" ")
		corpus.WriteString(r.Memory.Summary)
	}
	lower := strings.ToLower(corpus.String())
	covered := 0
	for t := range qTokens {
		if strings.Contains(lower, t) {
			covered++
		}
	}
	coverage := float64(covered) / float64(len(qTokens))
	sufficient := len(hits) >= cfg.ReflectionMinHits && coverage >= cfg.ReflectionMinCoverage
	return reflectionVerdict{sufficient: sufficient, coverage: coverage, nHits: len(hits)}
}

func rank(candidates []candidate, cfg *config.Config, neighbourCounts map[string]int, queryTypes []schema.MemoryType) []*schema.RecallResult {
	out := make([]*schema.RecallResult, 0, len(candidates))
	for _, c := range candidates {
		act := ActivationScore(c.Memory, c.Sim, cfg.Activation, neighbourCounts, queryTypes, 0)
		out = append(out, &schema.RecallResult{Memory: c.Memory, Score: act, Tier: 1, Via: []string{}})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out
}

type candidate struct {
	Memory *schema.Memory
	Sim    float64
}

// Recall runs the full two-tier retrieval pipeline.
func Recall(store *storage.SQLiteStore, embedder storage.EmbeddingProvider, query string, cfg *config.Config, workspace string, topK int, layers []schema.Layer, types []schema.MemoryType, minSimilarity float64) (*schema.RecallResponse, error) {
	start := time.Now()
	ws := workspace
	if ws == "" {
		ws = cfg.Workspace
	}
	rcfg := cfg.Recall
	k1 := topK
	if k1 == 0 {
		k1 = rcfg.TopKTier1
	}
	queryVec, err := embedder.Embed(query)
	if err != nil {
		return nil, err
	}
	queryTypes := types
	if len(queryTypes) == 0 {
		queryTypes = InferQueryTypes(query)
	}

	fetchK := k1 * 3
	if fetchK < k1 {
		fetchK = k1
	}
	rawHits := store.VectorIndex().Search(queryVec, fetchK)
	var cand []candidate
	for _, h := range rawHits {
		if h.Similarity < minSimilarity {
			continue
		}
		mem, err := store.GetMemory(h.ID)
		if err != nil {
			return nil, err
		}
		if mem == nil || mem.Workspace != ws {
			continue
		}
		if IsRetired(mem) {
			continue
		}
		if layers != nil && !layerIn(mem.Layer, layers) {
			continue
		}
		if types != nil && !typeIn(mem.Type, types) {
			continue
		}
		cand = append(cand, candidate{Memory: mem, Sim: h.Similarity})
	}

	neighbourCounts, err := store.NeighborCounts()
	if err != nil {
		return nil, err
	}

	tier1 := rank(cand, cfg, neighbourCounts, queryTypes)
	if len(tier1) > k1 {
		tier1 = tier1[:k1]
	}

	verdict := reflect(query, tier1, rcfg)
	if verdict.sufficient || !rcfg.EnableTier2 {
		commitAccess(store, resultIDs(tier1))
		return &schema.RecallResponse{
			Query: query, Results: tier1, TierReached: 1,
			ReflectedSufficient: verdict.sufficient,
			ElapsedMs:           float64(time.Since(start).Milliseconds()),
		}, nil
	}

	expanded, err := tier2Expand(store, tier1, cfg, ws)
	if err != nil {
		return nil, err
	}
	byID := map[string]*schema.RecallResult{}
	for _, r := range tier1 {
		byID[r.Memory.ID] = r
	}
	for _, ex := range expanded {
		if _, ok := byID[ex.Memory.ID]; ok {
			continue
		}
		if layers != nil && !layerIn(ex.Memory.Layer, layers) {
			continue
		}
		if types != nil && !typeIn(ex.Memory.Type, types) {
			continue
		}
		act := ActivationScore(ex.Memory, ex.Sim, cfg.Activation, neighbourCounts, queryTypes, 0)
		byID[ex.Memory.ID] = &schema.RecallResult{Memory: ex.Memory, Score: act, Tier: 2, Via: ex.Via}
	}
	k2 := topK
	if k2 == 0 {
		k2 = rcfg.TopKTier2
	}
	merged := make([]*schema.RecallResult, 0, len(byID))
	for _, r := range byID {
		merged = append(merged, r)
	}
	sort.SliceStable(merged, func(a, b int) bool { return merged[a].Score > merged[b].Score })
	if len(merged) > k2 {
		merged = merged[:k2]
	}

	commitAccess(store, resultIDs(merged))
	return &schema.RecallResponse{
		Query: query, Results: merged, TierReached: 2,
		ReflectedSufficient: true,
		ElapsedMs:           float64(time.Since(start).Milliseconds()),
	}, nil
}

func layerIn(l schema.Layer, layers []schema.Layer) bool {
	for _, x := range layers {
		if l == x {
			return true
		}
	}
	return false
}

func typeIn(t schema.MemoryType, types []schema.MemoryType) bool {
	for _, x := range types {
		if t == x {
			return true
		}
	}
	return false
}

func resultIDs(results []*schema.RecallResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.Memory.ID)
	}
	return ids
}

type expandedItem struct {
	Memory *schema.Memory
	Sim    float64
	Via    []string
}

func tier2Expand(store *storage.SQLiteStore, tier1 []*schema.RecallResult, cfg *config.Config, workspace string) ([]expandedItem, error) {
	var out []expandedItem
	seen := map[string]bool{}
	for _, r := range tier1 {
		seen[r.Memory.ID] = true
	}

	type frontierNode struct {
		id    string
		depth int
		path  []string
	}

	for _, anchor := range tier1 {
		frontier := []frontierNode{{anchor.Memory.ID, 1, []string{anchor.Memory.ID}}}
		for len(frontier) > 0 {
			cur := frontier[0]
			frontier = frontier[1:]
			if cur.depth > cfg.Recall.GraphHops {
				continue
			}
			// follow supersedes to the newest version even if filtered from tier-1
			supEdges, err := store.Neighbors(cur.id, "supersedes")
			if err != nil {
				return nil, err
			}
			for _, e := range supEdges {
				if e.SrcID == cur.id && !seen[e.DstID] {
					seen[e.DstID] = true
					newer, err := store.GetMemory(e.DstID)
					if err != nil {
						return nil, err
					}
					if newer != nil && newer.Workspace == workspace {
						out = append(out, expandedItem{Memory: newer, Sim: maxF(0.05, anchor.Score*0.6/float64(cur.depth)), Via: append(append([]string{}, cur.path...), e.DstID)})
					}
				}
			}
			edges, err := store.Neighbors(cur.id, "")
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				otherID := e.DstID
				if e.SrcID == cur.id {
					otherID = e.DstID
				} else {
					otherID = e.SrcID
				}
				if seen[otherID] {
					continue
				}
				seen[otherID] = true
				other, err := store.GetMemory(otherID)
				if err != nil {
					return nil, err
				}
				if other == nil || other.Workspace != workspace {
					continue
				}
				path := append(append([]string{}, cur.path...), otherID)
				out = append(out, expandedItem{Memory: other, Sim: maxF(0.05, anchor.Score*0.5/float64(cur.depth)), Via: path})
				frontier = append(frontier, frontierNode{otherID, cur.depth + 1, path})
			}
		}
	}

	// backtrack: for code symbols, pull their file memory too
	for memID := range seen {
		mem, err := store.GetMemory(memID)
		if err != nil {
			return nil, err
		}
		if mem == nil || mem.Type != schema.TypeCodeSymbol {
			continue
		}
		filePath := mem.MetaString("file_path")
		if filePath == "" {
			continue
		}
		files, err := store.IterMemories(workspace, "", string(schema.TypeCodeFile))
		if err != nil {
			return nil, err
		}
		for _, m := range files {
			if m.MetaString("file_path") == filePath && !seen[m.ID] {
				seen[m.ID] = true
				out = append(out, expandedItem{Memory: m, Sim: 0.1, Via: []string{memID, m.ID}})
			}
		}
	}
	return out, nil
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func commitAccess(store *storage.SQLiteStore, ids []string) {
	now := schema.Now()
	for _, id := range ids {
		_ = store.TouchMemory(id, now)
	}
}
