package operations

import (
	"sort"
	"strconv"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/layers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// ProceduralizeReport reports the outcome of a proceduralization pass.
type ProceduralizeReport struct {
	ClustersExamined int
	PlaybooksCreated int
	Actions          map[string]int
	Details          []map[string]any
}

func newProceduralizeReport() *ProceduralizeReport {
	return &ProceduralizeReport{Actions: map[string]int{
		string(ActionAdd): 0, string(ActionUpdate): 0, string(ActionNoop): 0,
	}}
}

func retrieveExistingPlaybooks(store storage.Store, candidateVec []float32, ws string, topK int) ([]similarFact, error) {
	raw := store.VectorSearch(candidateVec, topK)
	var similar []similarFact
	for _, h := range raw {
		if h.Similarity < 0.1 {
			continue
		}
		m, err := store.GetMemory(h.ID)
		if err != nil {
			return nil, err
		}
		if m == nil || m.Workspace != ws {
			continue
		}
		if m.Layer != schema.LayerProcedural || m.Type != schema.TypePlaybook {
			continue
		}
		if IsRetired(m) {
			continue
		}
		similar = append(similar, similarFact{Memory: m, Sim: h.Similarity})
	}
	sort.SliceStable(similar, func(a, b int) bool { return similar[a].Sim > similar[b].Sim })
	return similar, nil
}

func classifyPlaybook(candidateHash string, similar []similarFact, threshold float64) Action {
	for _, s := range similar {
		if s.Memory.ContentHash != "" && s.Memory.ContentHash == candidateHash {
			return ActionNoop
		}
	}
	if len(similar) > 0 && similar[0].Sim >= threshold {
		return ActionUpdate
	}
	return ActionAdd
}

// Proceduralize clusters successful episodic events into L3 playbooks.
func Proceduralize(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, minClusterSize int, similarityThreshold float64) (*ProceduralizeReport, error) {
	ws := workspace
	if ws == "" {
		ws = cfg.Workspace
	}
	if minClusterSize == 0 {
		minClusterSize = 3
	}
	if similarityThreshold == 0 {
		similarityThreshold = 0.55
	}
	proc := layers.NewProceduralMemory(store, embedder, ws)
	report := newProceduralizeReport()

	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(episodes, func(a, b int) bool { return episodes[a].CreatedAt < episodes[b].CreatedAt })

	var succ []*schema.Memory
	for _, e := range episodes {
		outcome := e.MetaString("outcome")
		if outcome == "success" || outcome == "ok" || outcome == "done" {
			succ = append(succ, e)
		}
	}
	if len(succ) < minClusterSize {
		return report, nil
	}

	contents := make([]string, len(succ))
	for i, e := range succ {
		contents[i] = e.Content
	}
	vecs, err := embedder.EmbedBatch(contents)
	if err != nil {
		return nil, err
	}
	assigned := make([]bool, len(succ))

	for i, anchor := range succ {
		if assigned[i] {
			continue
		}
		cluster := []*schema.Memory{anchor}
		for j := i + 1; j < len(succ); j++ {
			if assigned[j] {
				continue
			}
			if storage.CosineSimilarity(vecs[i], vecs[j]) >= similarityThreshold {
				cluster = append(cluster, succ[j])
				assigned[j] = true
			}
		}
		if len(cluster) >= minClusterSize {
			assigned[i] = true
			report.ClustersExamined++
			// Python: Counter(c.metadata.get("action", "do") ...).most_common(1)
			// — missing action counts as "do", ties keep first-occurrence order.
			actionCounts := map[string]int{}
			var actionOrder []string
			for _, c := range cluster {
				a := c.MetaString("action")
				if a == "" {
					a = "do"
				}
				if _, ok := actionCounts[a]; !ok {
					actionOrder = append(actionOrder, a)
				}
				actionCounts[a]++
			}
			topAction := ""
			topN := 0
			for _, a := range actionOrder {
				if actionCounts[a] > topN {
					topAction = a
					topN = actionCounts[a]
				}
			}
			steps := deriveSteps(cluster)
			name := "How to " + topAction + " (" + strconv.Itoa(len(cluster)) + " episodes)"
			candidateContent := layers.PlaybookContent(name, steps)
			candidateHash := schema.ContentHash(candidateContent)
			candidateVec, err := embedder.Embed(candidateContent)
			if err != nil {
				return nil, err
			}
			similar, err := retrieveExistingPlaybooks(store, candidateVec, ws, cfg.Consolidate.MinEpisodesToTrigger+5)
			if err != nil {
				return nil, err
			}
			action := classifyPlaybook(candidateHash, similar, similarityThreshold)
			report.Actions[string(action)]++

			preconditions := []string{}
			seenAgents := map[string]bool{}
			for _, c := range cluster {
				a := c.MetaString("agent")
				if a == "" {
					a = "agent"
				}
				if !seenAgents[a] {
					seenAgents[a] = true
					preconditions = append(preconditions, a)
				}
			}

			if action == ActionAdd {
				if _, err := proc.PutPlaybook(name, steps, preconditions, "success", []string{topAction}); err != nil {
					return nil, err
				}
				report.PlaybooksCreated++
			} else if action == ActionUpdate && len(similar) > 0 {
				newMem, err := proc.PutPlaybook(name, steps, preconditions, "success", []string{topAction})
				if err != nil {
					return nil, err
				}
				if err := Retire(store, similar[0].Memory, newMem.ID); err != nil {
					return nil, err
				}
			}
			report.Details = append(report.Details, map[string]any{
				"action": string(action), "action_verb": topAction, "size": len(cluster),
			})
		}
	}
	return report, nil
}

func deriveSteps(cluster []*schema.Memory) []string {
	seen := map[string]bool{}
	var steps []string
	for _, c := range cluster {
		action := c.MetaString("action")
		observation := c.MetaString("observation")
		s := action
		if observation != "" {
			s += " — " + observation
		}
		if s != "" && !seen[s] {
			seen[s] = true
			steps = append(steps, s)
		}
	}
	return steps
}
