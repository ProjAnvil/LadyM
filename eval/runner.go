//go:build eval

package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/hook"
)

// Options controls one evaluation run.
type Options struct {
	// Bin is the ladym binary to probe. Empty falls back to LADYM_BIN.
	Bin string
	// Trials is how many fresh corpus copies each scenario measures. More trials
	// means stabler percentiles and proportionally more disk copies.
	Trials int
	// Spawns is how many invocations run against each corpus copy. Repeats on one
	// file buy percentile stability cheaply; the file itself is copied fresh per
	// trial so measurements never see another trial's writes.
	Spawns int
	// StageEvents is how many writes feed the per-scale stage breakdown.
	StageEvents int
	// EngineOpenRuns is how many engine opens are timed to characterise the
	// startup cost that the in-process breakdown cannot see.
	EngineOpenRuns int
	Workspace      string
	Seed           int64
}

// DefaultOptions uses counts that keep the large scale affordable: each trial
// copies the whole corpus, so more rows means fewer repeats.
func DefaultOptions() Options {
	return Options{
		Trials: 3, Spawns: 4, StageEvents: 40, EngineOpenRuns: 5,
		Workspace: evalWorkspace, Seed: 20261021,
	}
}

// Scenario is one scale x fixture cell of the report.
type Scenario struct {
	Scale         string  `json:"scale"`
	Fixture       string  `json:"fixture"`
	Harness       string  `json:"harness"`
	Event         string  `json:"event"`
	Trials        int     `json:"trials"`
	NewRows       int     `json:"new_rows"`
	SpawnMs       Summary `json:"spawn_ms"`
	PersistedToks Summary `json:"persisted_tokens"`
	ContentToks   Summary `json:"content_tokens"`
	SummaryToks   Summary `json:"summary_tokens"`
	MetadataToks  Summary `json:"metadata_tokens"`
	Amplification Summary `json:"amplification"`
}

// StageRow is the per-scale write-path breakdown.
type StageRow struct {
	Scale    string  `json:"scale"`
	Corpus   int     `json:"corpus_rows"`
	SpawnP50 float64 `json:"spawn_p50_ms"`
	// ProcessTaxMs is SpawnP50 minus the real in-process write: the cost the agent
	// pays that has nothing to do with writing memory.
	ProcessTaxMs float64 `json:"process_tax_ms"`
	Segments     Segments `json:"segments"`
}

// Report is the complete result of one run.
type Report struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Bin         string     `json:"bin"`
	Trials      int        `json:"trials"`
	Scenarios   []Scenario `json:"scenarios"`
	Stages      []StageRow `json:"stages"`
}

// Evaluate runs the full matrix: every fixture against every corpus scale, plus
// a per-scale stage breakdown. It seeds one corpus template per scale and hands
// each trial a private copy, so no measurement ever sees another's writes.
func Evaluate(opts Options, counter *Counter) (Report, error) {
	if counter == nil {
		return Report{}, fmt.Errorf("evaluate: counter is nil, token figures would all be zero")
	}
	bin := opts.Bin
	if bin == "" {
		if env := os.Getenv("LADYM_BIN"); env != "" {
			bin = env
		} else {
			return Report{}, fmt.Errorf("evaluate: Options.Bin and LADYM_BIN are both unset")
		}
	}
	rep := Report{GeneratedAt: time.Now(), Bin: bin, Trials: opts.Trials}

	fixtures := Fixtures()
	for _, sc := range StandardScales {
		templateDir, err := os.MkdirTemp("", "ladym-eval-corpus-"+sc.Name)
		if err != nil {
			return rep, err
		}
		defer os.RemoveAll(templateDir)

		corpus, err := Seed(sc.Size, templateDir, opts.Seed, opts.Workspace)
		if err != nil {
			return rep, fmt.Errorf("seed %s corpus: %w", sc.Name, err)
		}
		for _, fx := range fixtures {
			s, err := runScenario(bin, corpus, fx, sc, opts, counter)
			if err != nil {
				return rep, fmt.Errorf("scenario %s/%s: %w", sc.Name, fx.Name, err)
			}
			rep.Scenarios = append(rep.Scenarios, s)
		}
		stage, err := measureStages(bin, corpus, sc, opts)
		if err != nil {
			return rep, fmt.Errorf("stages for %s: %w", sc.Name, err)
		}
		rep.Stages = append(rep.Stages, stage)
	}
	return rep, nil
}

// runScenario writes one fixture repeatedly into fresh copies of the corpus and
// summarises cost and duration.
//
// Only the first spawn of each trial feeds the token sample. The rest would add
// byte-identical rows, which would flatten every percentile to a single value.
func runScenario(bin, corpus string, fx Fixture, sc Scale, opts Options, counter *Counter) (Scenario, error) {
	payload, err := fx.PayloadJSON()
	if err != nil {
		return Scenario{}, err
	}
	inputToks := counter.Count(payload)

	out := Scenario{
		Scale: sc.Name, Fixture: fx.Name, Harness: fx.Harness,
		Event: fx.EventName, Trials: opts.Trials,
	}
	spawn := []float64{}
	persisted, content, summary, meta, amp := []float64{}, []float64{}, []float64{}, []float64{}, []float64{}

	for i := 0; i < opts.Trials; i++ {
		dir, err := os.MkdirTemp("", "ladym-eval-trial")
		if err != nil {
			return out, err
		}
		defer os.RemoveAll(dir)

		db, err := copyCorpus(corpus, dir)
		if err != nil {
			return out, err
		}
		before, err := episodeIDs(db, opts.Workspace)
		if err != nil {
			return out, err
		}

		res, err := SpawnHook(bin, db, fx.Harness, payload)
		if err != nil {
			return out, err
		}
		spawn = append(spawn, res.DurationMs)

		rows, err := ReadEpisodes(db, opts.Workspace)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			if before[r.ID] {
				continue
			}
			out.NewRows++
			c, m, s := counter.Count(r.Content), counter.Count(r.Metadata), counter.Count(r.Summary)
			content = append(content, float64(c))
			meta = append(meta, float64(m))
			summary = append(summary, float64(s))
			persisted = append(persisted, float64(c+m+s))
			if inputToks > 0 {
				amp = append(amp, float64(c+m+s)/float64(inputToks))
			}
		}

		for n := 1; n < opts.Spawns; n++ {
			res, err := SpawnHook(bin, db, fx.Harness, payload)
			if err != nil {
				return out, err
			}
			spawn = append(spawn, res.DurationMs)
		}
	}

	out.SpawnMs = Summarize(spawn)
	out.PersistedToks = Summarize(persisted)
	out.ContentToks = Summarize(content)
	out.SummaryToks = Summarize(summary)
	out.MetadataToks = Summarize(meta)
	out.Amplification = Summarize(amp)
	return out, nil
}

// stageProbe is the resource-bearing event used for the per-scale breakdown. It
// is a synthetic shape rather than a fixture so the same event backs the stage
// timing and the spawn timing, which is what makes ProcessTaxMs meaningful.
func stageProbe(workspace string) Fixture {
	return Fixture{
		Name: "stage-probe", Harness: "claude-code", EventName: "PostToolUse", Workspace: workspace,
		Payload: toolPayload("PostToolUse", "Read",
			map[string]any{"file_path": "/x/ladyM/engine/engine.go"}, "probe observation"),
	}
}

// probeEvents turns the stage probe into n distinct events.
func probeEvents(probe Fixture, n int) ([]engine.OperationEvent, error) {
	payload, err := probe.PayloadJSON()
	if err != nil {
		return nil, err
	}
	p, err := hook.Parse([]byte(payload))
	if err != nil {
		return nil, err
	}
	base := p.ToOperation(probe.Harness)
	if base == nil {
		return nil, fmt.Errorf("stage probe maps to no operation")
	}
	out := make([]engine.OperationEvent, 0, n)
	for i := 0; i < n; i++ {
		ev := *base
		ev.Observation = fmt.Sprintf("probe observation seq=%d", i)
		out = append(out, ev)
	}
	return out, nil
}

// measureStages produces the per-scale breakdown for one representative event and
// times the same event end to end, so their difference is the process tax.
func measureStages(bin, corpus string, sc Scale, opts Options) (StageRow, error) {
	probe := stageProbe(opts.Workspace)
	evs, err := probeEvents(probe, opts.StageEvents)
	if err != nil {
		return StageRow{}, err
	}
	segs, err := Measure(corpus, opts.Workspace, evs)
	if err != nil {
		return StageRow{}, err
	}
	openMs, err := EngineOpenMs(corpus, opts.Workspace, opts.EngineOpenRuns)
	if err != nil {
		return StageRow{}, err
	}
	segs.EngineOpenMs = openMs

	payload, err := probe.PayloadJSON()
	if err != nil {
		return StageRow{}, err
	}
	dir, err := os.MkdirTemp("", "ladym-eval-stage-spawn")
	if err != nil {
		return StageRow{}, err
	}
	defer os.RemoveAll(dir)
	db, err := copyCorpus(corpus, dir)
	if err != nil {
		return StageRow{}, err
	}
	vals := make([]float64, 0, opts.Spawns)
	for i := 0; i < opts.Spawns; i++ {
		res, err := SpawnHook(bin, db, probe.Harness, payload)
		if err != nil {
			return StageRow{}, err
		}
		vals = append(vals, res.DurationMs)
	}
	spawnP50 := Summarize(vals).P50

	return StageRow{
		Scale:        sc.Name,
		Corpus:       sc.Size,
		SpawnP50:     spawnP50,
		ProcessTaxMs: spawnP50 - segs.RealMs,
		Segments:     segs,
	}, nil
}

// episodeIDs collects the episode IDs already in the corpus, so a trial can tell
// its own writes apart from pre-existing rows.
func episodeIDs(dbPath, workspace string) (map[string]bool, error) {
	rows, err := ReadEpisodes(dbPath, workspace)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.ID] = true
	}
	return out, nil
}

// ReportsDir is where generated artifacts land, relative to the eval package.
const ReportsDir = ".cache/reports"

// WriteReport persists metrics.json and report.md under a timestamped folder and
// returns that folder.
func WriteReport(root string, rep Report) (string, error) {
	dir := filepath.Join(root, ReportsDir, rep.GeneratedAt.Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(dir, "metrics.json"), rep); err != nil {
		return "", err
	}
	if err := writeMarkdown(filepath.Join(dir, "report.md"), rep); err != nil {
		return "", err
	}
	return dir, nil
}
