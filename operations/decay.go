package operations

import (
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// DecayReport reports the outcome of a decay pass.
type DecayReport struct {
	Examined     int
	Forgotten    int
	ForgottenIDs []string
}

// Decay forgets episodic events whose activation has fallen below the floor.
// Code analysis, playbooks, and edges are never auto-forgotten.
func Decay(store *storage.SQLiteStore, workspace string, weights *config.ActivationWeights, maxAgeS, activationFloor, now float64, dryRun bool) (*DecayReport, error) {
	if weights == nil {
		weights = &config.ActivationWeights{
			Similarity: 1.0, Recency: 0.3, Frequency: 0.2, Graph: 0.15,
			TypeBoost: 0.25, RecencyHalfLifeS: 7 * 24 * 3600.0,
		}
	}
	if maxAgeS == 0 {
		maxAgeS = 30 * 24 * 3600.0
	}
	if activationFloor == 0 {
		activationFloor = 0.05
	}
	if now == 0 {
		now = schema.Now()
	}
	report := &DecayReport{}
	episodes, err := store.IterMemories(workspace, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	for _, mem := range episodes {
		report.Examined++
		age := now - mem.LastAccessAt
		if age < maxAgeS {
			continue
		}
		act := weights.Recency*RecencyFactor(mem.LastAccessAt, weights.RecencyHalfLifeS, now) +
			weights.Frequency*0.0
		if act < activationFloor {
			report.Forgotten++
			report.ForgottenIDs = append(report.ForgottenIDs, mem.ID)
			if !dryRun {
				if err := store.DeleteMemory(mem.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	return report, nil
}
