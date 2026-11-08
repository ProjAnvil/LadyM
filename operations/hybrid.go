package operations

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const hybridGraphBudget = 128

type lexicalCandidate struct {
	memory *schema.Memory
	score  int
}

func symbolRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

func exactLexical(text, query string) bool {
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], query)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(query)
		left, right := true, true
		first, _ := utf8.DecodeRuneInString(query)
		last, _ := utf8.DecodeLastRuneInString(query)
		if i > 0 && symbolRune(first) {
			r, _ := utf8.DecodeLastRuneInString(text[:i])
			left = !symbolRune(r)
		}
		if end < len(text) && symbolRune(last) {
			r, _ := utf8.DecodeRuneInString(text[end:])
			right = !symbolRune(r)
		}
		if left && right {
			return true
		}
		offset = i + 1
	}
	return false
}

// HybridRecall fuses independent literal, vector and bounded graph ranks.
// It never asks a language model to interpret a symbol or invent a relation.
func HybridRecall(store storage.Store, embedder storage.EmbeddingProvider, query, workspace string, limit int) (*schema.RecallResponse, error) {
	start := time.Now()
	query = strings.TrimSpace(query)
	if query == "" || !utf8.ValidString(query) || limit < 1 || limit > 200 {
		return nil, fmt.Errorf("%w: query and limit (1..200) required", schema.ErrInvalidClaim)
	}
	all, err := store.IterMemories(workspace, "", "")
	if err != nil {
		return nil, err
	}
	filter := storage.MemoryFilter{Workspace: workspace, ExcludeUnusable: true, ExcludeSystem: true}
	eligible := map[string]*schema.Memory{}
	var lexical []lexicalCandidate
	now := schema.Now()
	for _, m := range all {
		if !eligibleRecallMemory(m, filter) {
			continue
		}
		fresh, err := storage.IsFresh(store, m, now)
		if err != nil {
			return nil, err
		}
		if !fresh {
			continue
		}
		eligible[m.ID] = m
		score := 0
		for _, text := range []string{m.Content, m.Summary, m.Source, m.MetaString("file_path"), m.MetaString("symbol_name"), m.MetaString("name")} {
			if exactLexical(text, query) {
				score += 2
			}
		}
		if score > 0 && m.Type != schema.TypeTopicView {
			lexical = append(lexical, lexicalCandidate{m, score})
		}
	}
	slices.SortFunc(lexical, func(a, b lexicalCandidate) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.memory.ID, b.memory.ID))
	})
	if len(lexical) > limit {
		lexical = lexical[:limit]
	}
	vec, err := embedder.Embed(query)
	if err != nil {
		return nil, err
	}
	// Fetch the scoped eligible set before transitive freshness filtering:
	// expired sources must not consume vector slots before topK.
	hits, err := store.SearchMemories(vec, max(len(all), 1), filter)
	if err != nil {
		return nil, err
	}
	var lexicalIDs, vectorIDs []string
	for _, c := range lexical {
		lexicalIDs = append(lexicalIDs, c.memory.ID)
	}
	for _, hit := range hits {
		if eligible[hit.ID] != nil {
			vectorIDs = append(vectorIDs, hit.ID)
		}
		if len(vectorIDs) == limit {
			break
		}
	}
	queue := append(slices.Clone(lexicalIDs), vectorIDs...)
	seen := map[string]bool{}
	var graphIDs []string
	for _, id := range queue {
		if len(graphIDs) == hybridGraphBudget {
			break
		}
		if !slices.Contains(graphIDs, id) {
			graphIDs = append(graphIDs, id)
		}
	}
	visited := 0
	evidenceReads := 0
	for len(queue) > 0 && visited < hybridGraphBudget {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		visited++
		edges, err := store.Neighbors(id, "")
		if err != nil {
			return nil, err
		}
		slices.SortFunc(edges, func(a, b *schema.Edge) int {
			return cmp.Or(cmp.Compare(a.SrcID, b.SrcID), cmp.Compare(a.DstID, b.DstID), cmp.Compare(a.Relation, b.Relation), cmp.Compare(a.ID, b.ID))
		})
		var neighbors []string
		for _, edge := range edges {
			if edge.ValidTo != nil {
				continue
			}
			other := edge.DstID
			if other == id {
				other = edge.SrcID
			}
			if !seen[other] && eligible[other] == nil && edge.SrcID == id &&
				(edge.Relation == "produced_from" || edge.Relation == "abstracts") &&
				(eligible[id].Type == schema.TypeClaim || eligible[id].Type == schema.TypeTopicView) && evidenceReads < hybridGraphBudget {
				evidenceReads++
				source, err := store.GetMemory(other)
				if err != nil {
					return nil, err
				}
				if source != nil && source.Type == schema.TypeEvidence && source.Workspace == workspace {
					fresh, err := storage.IsFresh(store, source, now)
					if err != nil {
						return nil, err
					}
					if fresh {
						eligible[other] = source
					}
				}
			}
			if !seen[other] && eligible[other] != nil {
				neighbors = append(neighbors, other)
			}
		}
		slices.Sort(neighbors)
		neighbors = slices.Compact(neighbors)
		for _, other := range neighbors {
			if len(graphIDs) >= hybridGraphBudget {
				break
			}
			if !slices.Contains(graphIDs, other) {
				graphIDs = append(graphIDs, other)
				queue = append(queue, other)
			}
		}
	}
	scores := map[string]float64{}
	via := map[string][]string{}
	for i, ids := range [][]string{lexicalIDs, vectorIDs, graphIDs} {
		channel := []string{"lexical", "vector", "graph"}[i]
		for rank, id := range ids {
			scores[id] += 1 / float64(60+rank+1)
			via[id] = append(via[id], channel)
		}
	}
	results := make([]*schema.RecallResult, 0, len(scores))
	for id, score := range scores {
		results = append(results, &schema.RecallResult{Memory: eligible[id], Score: score, Tier: 1, Via: via[id]})
	}
	slices.SortFunc(results, compareRecallResults)
	if len(results) > limit {
		results = results[:limit]
	}
	return &schema.RecallResponse{Query: query, Results: results, TierReached: 1, ReflectedSufficient: len(lexicalIDs) > 0, ElapsedMs: float64(time.Since(start).Microseconds()) / 1000}, nil
}
