// Package operations holds LadyM's cognitive operations (activation, recall,
// consolidation, decay, proceduralization, supersedes, attention, L5/L6).
package operations

import (
	"math"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

// RecencyFactor is exponential decay: 1.0 right after access, 0.5 after
// halfLifeS seconds.
func RecencyFactor(lastAccessAt, halfLifeS, now float64) float64 {
	if now == 0 {
		now = schema.Now()
	}
	age := now - lastAccessAt
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, age/halfLifeS)
}

// FrequencyFactor is the diminishing-returns log curve log(1 + n).
func FrequencyFactor(accessCount int) float64 {
	if accessCount < 0 {
		accessCount = 0
	}
	return math.Log(1.0 + float64(accessCount))
}

// TypeBoostForQuery boosts items whose type matches a query-type prior.
func TypeBoostForQuery(mem *schema.Memory, queryTypes []schema.MemoryType, weight float64) float64 {
	if len(queryTypes) == 0 {
		return 0.0
	}
	for _, t := range queryTypes {
		if mem.Type == t {
			return weight
		}
	}
	return 0.0
}

// GraphFactor is spreading activation: items with more current graph
// neighbours get a small boost.
func GraphFactor(mem *schema.Memory, neighbourCounts map[string]int, weight float64) float64 {
	n := neighbourCounts[mem.ID]
	return weight * math.Log(1.0+float64(n))
}

// ActivationScore computes the full ACT-R-inspired activation score.
func ActivationScore(mem *schema.Memory, querySimilarity float64, w config.ActivationWeights, neighbourCounts map[string]int, queryTypes []schema.MemoryType, now float64) float64 {
	sim := querySimilarity
	if sim < 0 {
		sim = 0
	}
	return w.Similarity*sim +
		w.Recency*RecencyFactor(mem.LastAccessAt, w.RecencyHalfLifeS, now) +
		w.Frequency*FrequencyFactor(mem.AccessCount) +
		w.Graph*GraphFactor(mem, neighbourCounts, 1.0) +
		TypeBoostForQuery(mem, queryTypes, w.TypeBoost)
}

// InferQueryTypes heuristically detects whether a query is about code (boosting
// code_symbol/code_file) or a how-to (boosting playbook).
func InferQueryTypes(query string) []schema.MemoryType {
	q := strings.ToLower(query)
	codeSignals := []string{
		"function", "class", "method", "def ", "import", "module", "api", "endpoint",
		"variable", "implement", "where is", "where defined", "signature", "call",
	}
	for _, s := range codeSignals {
		if strings.Contains(q, s) {
			return []schema.MemoryType{schema.TypeCodeSymbol, schema.TypeCodeFile}
		}
	}
	for _, s := range []string{"how to", "how do i", "steps", "procedure", "playbook"} {
		if strings.Contains(q, s) {
			return []schema.MemoryType{schema.TypePlaybook}
		}
	}
	return []schema.MemoryType{}
}

// NeighbourCountsFor projects a {id: count} map onto just the given memories.
func NeighbourCountsFor(mems []*schema.Memory, counts map[string]int) map[string]int {
	out := map[string]int{}
	for _, m := range mems {
		out[m.ID] = counts[m.ID]
	}
	return out
}
