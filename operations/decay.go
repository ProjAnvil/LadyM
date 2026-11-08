package operations

import (
	"cmp"
	"math"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// DecayReport reports the outcome of a decay pass.
type DecayReport struct {
	Examined     int
	Forgotten    int
	Protected    int
	ForgottenIDs []string
}

// Decay forgets episodic events whose activation has fallen below the floor.
// Code analysis, playbooks, and edges are never auto-forgotten.
func Decay(store storage.Store, workspace string, weights *config.ActivationWeights, maxAgeS, activationFloor, now float64, dryRun bool) (*DecayReport, error) {
	if weights == nil {
		weights = &config.ActivationWeights{
			Similarity: 1.0, Recency: 0.3, Frequency: 0.2, Graph: 0.15,
			TypeBoost: 0.25, RecencyHalfLifeS: 7 * 24 * 3600.0,
		}
	}
	maxAgeS = cmp.Or(maxAgeS, 30*24*3600.0)
	activationFloor = cmp.Or(activationFloor, 0.05)
	now = cmp.Or(now, schema.Now())
	report := &DecayReport{}
	references, err := store.EvidenceReferences(workspace)
	if err != nil {
		return nil, err
	}
	cutoff := now - maxAgeS
	if weights.Recency > activationFloor && weights.RecencyHalfLifeS > 0 {
		activationCutoff := now - weights.RecencyHalfLifeS*math.Log2(weights.Recency/activationFloor)
		cutoff = min(cutoff, math.Nextafter(activationCutoff, math.Inf(-1)))
	}
	episodes, err := store.IterMemories(workspace, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	for _, mem := range episodes {
		if schema.IsSystemType(mem.Type) {
			continue
		}
		report.Examined++
		age := now - mem.LastAccessAt
		if age < maxAgeS {
			continue
		}
		act := weights.Recency*RecencyFactor(mem.LastAccessAt, weights.RecencyHalfLifeS, now) +
			weights.Frequency*0.0
		if act < activationFloor {
			deleted, protected := !references[mem.ID], references[mem.ID]
			if !dryRun && cutoff > 0 {
				deleted, protected, err = store.DeleteMemorySafely(mem.ID, cutoff)
				if err != nil {
					return nil, err
				}
			} else if !dryRun {
				deleted, protected = false, false
			}
			if protected {
				report.Protected++
			}
			if deleted {
				report.Forgotten++
				report.ForgottenIDs = append(report.ForgottenIDs, mem.ID)
			}
		}
	}
	return report, nil
}
