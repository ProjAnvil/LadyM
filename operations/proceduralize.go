package operations

import (
	"cmp"
	"fmt"
	"slices"
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
	raw, err := store.SearchMemories(candidateVec, topK, storage.MemoryFilter{
		Workspace: ws, Layers: []schema.Layer{schema.LayerProcedural},
		Types: []schema.MemoryType{schema.TypePlaybook}, ExcludeUnusable: true,
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
		if !schema.IsUsable(m) || m.Workspace != ws {
			continue
		}
		if m.Layer != schema.LayerProcedural || m.Type != schema.TypePlaybook {
			continue
		}
		similar = append(similar, similarFact{Memory: m, Sim: h.Similarity})
	}
	slices.SortFunc(similar, func(a, b similarFact) int {
		return cmp.Or(cmp.Compare(b.Sim, a.Sim), cmp.Compare(a.Memory.ID, b.Memory.ID))
	})
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
	ws = cmp.Or(ws, cfg.Workspace)
	minClusterSize = cmp.Or(minClusterSize, 3)
	similarityThreshold = cmp.Or(similarityThreshold, 0.55)
	report := newProceduralizeReport()

	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(episodes, func(a, b *schema.Memory) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})

	var succ []*schema.Memory
	for _, e := range episodes {
		if !schema.IsUsable(e) {
			continue
		}
		hook := slices.Contains(e.Tags, "hook") || e.MetaString("hook_event_name") != ""
		verifiedTask := e.MetaString("task_outcome") == "success" && e.MetaString("knowledge_status") == "verified"
		if hook && !verifiedTask {
			continue
		}
		outcome := e.MetaString("outcome")
		if verifiedTask || outcome == "success" || outcome == "ok" || outcome == "done" {
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
	vecs, err := storage.StoredEmbeddings(store, embedder, succ)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(contents) {
		return nil, fmt.Errorf("proceduralize: embedding batch returned %d vectors for %d episodes", len(vecs), len(contents))
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
				a = cmp.Or(a, "do")
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
			if action == ActionNoop {
				for j, existing := range similar {
					if existing.Memory.ContentHash == candidateHash {
						similar[0], similar[j] = similar[j], similar[0]
						break
					}
				}
			}

			preconditions := []string{}
			seenAgents := map[string]bool{}
			for _, c := range cluster {
				a := c.MetaString("agent")
				a = cmp.Or(a, "agent")
				if !seenAgents[a] {
					seenAgents[a] = true
					preconditions = append(preconditions, a)
				}
			}

			batch := storage.MemoryBatch{}
			if action == ActionNoop {
				guardMemory(&batch, similar[0].Memory)
				for _, source := range cluster {
					guardMemory(&batch, source)
				}
			} else {
				m := layers.BuildPlaybook(ws, name, steps, preconditions, "success", []string{topAction})
				sources := slices.Clone(cluster)
				if action == ActionUpdate {
					previous, err := sourceSnapshots(store, similar[0].Memory)
					if err != nil {
						return nil, err
					}
					sources = append(sources, previous...)
					stageRetirement(&batch, similar[0].Memory, m.ID)
				}
				status := "verified"
				for _, source := range sources {
					if source.MetaString("task_outcome") != "success" || source.MetaString("knowledge_status") != "verified" {
						status = "inferred"
						break
					}
				}
				m.Metadata["knowledge_status"] = status
				if err := stageProvenance(store, &batch, m, sources, "produced_from"); err != nil {
					return nil, err
				}
				batch.Writes = append(batch.Writes, storage.MemoryWrite{Memory: m, Vector: candidateVec})
			}
			if err := store.ApplyMemoryBatch(batch); err != nil {
				return nil, err
			}
			report.Actions[string(action)]++
			if action == ActionAdd {
				report.PlaybooksCreated++
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
