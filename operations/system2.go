package operations

// System2Report is the outcome of one background consolidation cycle.
type System2Report struct {
	Consolidate     any
	Proceduralize   any
	L5              any
	L6              any
	Decay           any
	SkippedLLMSteps bool
}

// System2Runner is the interface the engine satisfies to run a System 2 cycle
// (kept as an interface so operations does not import engine).
type System2Runner interface {
	Consolidate(workspace string, since float64) (*ConsolidationReport, error)
	Proceduralize(workspace string, minClusterSize int) (*ProceduralizeReport, error)
	ExtractMentalModels(workspace string) (*L5ExtractionReport, error)
	PredictForwardIntents(workspace string) (*L6PredictionReport, error)
	Decay(workspace string, dryRun bool, maxAgeS, activationFloor float64) (*DecayReport, error)
	CountRecentEpisodes(workspace string) (int, error)
	MinEpisodesToRun() int
}

// RunSystem2Cycle runs one System 2 cycle through the runner.
func RunSystem2Cycle(runner System2Runner, workspace string) (*System2Report, error) {
	report := &System2Report{}
	cons, err := runner.Consolidate(workspace, 0)
	if err != nil {
		return nil, err
	}
	report.Consolidate = cons
	proc, err := runner.Proceduralize(workspace, 0)
	if err != nil {
		return nil, err
	}
	report.Proceduralize = proc
	n, err := runner.CountRecentEpisodes(workspace)
	if err != nil {
		return nil, err
	}
	if n >= runner.MinEpisodesToRun() {
		l5, err := runner.ExtractMentalModels(workspace)
		if err != nil {
			return nil, err
		}
		report.L5 = l5
		l6, err := runner.PredictForwardIntents(workspace)
		if err != nil {
			return nil, err
		}
		report.L6 = l6
	} else {
		report.SkippedLLMSteps = true
	}
	decay, err := runner.Decay(workspace, true, 0, 0)
	if err != nil {
		return nil, err
	}
	report.Decay = decay
	return report, nil
}
