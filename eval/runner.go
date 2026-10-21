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
	// Bin is the ladym binary to probe. Empty resolves through LADYM_BIN.
	Bin string
	// Trials is how many fresh corpus copies each scenario measures. More trials
	// means stabler percentiles and proportionally more disk copies.
	Trials int
	// WarmRuns is how many further invocations follow each cold one on the same
	// file. Cold and warm are reported separately because they answer different
	// questions: cold is worst-case first touch, warm is what a working session
	// pays once its database file sits in page cache.
	WarmRuns int
	// StageEvents is how many writes feed the per-scale stage breakdown.
	StageEvents int
	Workspace   string
	Seed        int64
}

// DefaultOptions uses trial counts that keep the large scale affordable: each
// trial copies the whole corpus, so more rows means fewer repeats.
func DefaultOptions() Options {
	return Options{Trials: 3, WarmRuns: 3, StageEvents: 40, Workspace: evalWorkspace, Seed: 20261021}
}

// Scenario is one scale x fixture cell of the report.
type Scenario struct {
	Scale         string  `json:"scale"`
	Fixture       string  `json:"fixture"`
	Harness       string  `json:"harness"`
	Event         string  `json:"event"`
	Trials        int     `json:"trials"`
	NewRows       int     `json:"new_rows"`
	SpawnColdMs   Summary `json:"spawn_cold_ms"`
	SpawnWarmMs   Summary `json:"spawn_warm_ms"`
	PersistedToks Summary `json:"persisted_tokens"`
	ContentToks   Summary `json:"content_tokens"`
	MetadataToks  Summary `json:"metadata_tokens"`
	Amplification Summary `json:"amplification"`
}

// StageRow is the per-scale write-path breakdown.
type StageRow struct {
	Scale    string   `json:"scale"`
	Corpus   int      `json:"corpus_rows"`
	Segments Segments `json:"segments"`
}

// Report is the complete result of one run.
type Report struct {
	GeneratedAt time.Time `json:"generated_at"`
	Bin         string    `json:"bin"`
	Trials      int       `json:"trials"`
	Scenarios   []Scenario `json:"scenarios"`
	Stages      []StageRow `json:"stages"`
}

// Evaluate runs the full matrix: every fixture against every corpus scale, plus
// a per-scale stage breakdown. It seeds one corpus template per scale and hands
// each trial a private copy, so no measurement ever sees another's writes.
func Evaluate(opts Options, counter *Counter) (Report, error) {
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
		stage, err := measureStages(corpus, sc, opts)
		if err != nil {
			return rep, fmt.Errorf("stages for %s: %w", sc.Name, err)
		}
		rep.Stages = append(rep.Stages, stage)
	}
	return rep, nil
}

// runScenario writes one fixture repeatedly into fresh copies of the corpus and
// summarises cost and duration across trials.
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
	var cold, warm []float64
	persisted, content, meta, amp := []float64{}, []float64{}, []float64{}, []float64{}

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

		// First touch on a freshly copied file: page cache is cold, which is the
		// worst case a session pays on its first hook call.
		res, err := SpawnHook(bin, db, fx.Harness, payload)
		if err != nil {
			return out, err
		}
		cold = append(cold, res.DurationMs)

		// Steady state: what a working session pays once the file is hot.
		for w := 0; w < opts.WarmRuns; w++ {
			res, err := SpawnHook(bin, db, fx.Harness, payload)
			if err != nil {
				return out, err
			}
			warm = append(warm, res.DurationMs)
		}

		// Read once per trial rather than per invocation: with a 10k-row corpus
		// that difference dominates harness runtime.
		rows, err := ReadEpisodes(db, opts.Workspace)
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			if before[r.ID] {
				continue
			}
			out.NewRows++
			c := counter.Count(r.Content)
			m := counter.Count(r.Metadata)
			s := counter.Count(r.Summary)
			content = append(content, float64(c))
			meta = append(meta, float64(m))
			persisted = append(persisted, float64(c+m+s))
			if inputToks > 0 {
				amp = append(amp, float64(c+m+s)/float64(inputToks))
			}
		}
	}

	out.SpawnColdMs = Summarize(cold)
	out.SpawnWarmMs = Summarize(warm)
	out.PersistedToks = Summarize(persisted)
	out.ContentToks = Summarize(content)
	out.MetadataToks = Summarize(meta)
	out.Amplification = Summarize(amp)
	return out, nil
}

// measureStages produces the cost breakdown for one scale using a resource-bearing
// representative event, which is the shape that exercises every stage.
func measureStages(corpus string, sc Scale, opts Options) (StageRow, error) {
	rep := Fixture{
		Name: "stage-probe", Harness: "claude-code", EventName: "PostToolUse", Workspace: opts.Workspace,
		Payload: toolPayload("PostToolUse", "Read",
			map[string]any{"file_path": "/x/ladyM/engine/engine.go"}, "probe observation"),
	}
	payload, err := rep.PayloadJSON()
	if err != nil {
		return StageRow{}, err
	}
	p, err := hook.Parse([]byte(payload))
	if err != nil {
		return StageRow{}, err
	}
	base := p.ToOperation(rep.Harness)
	if base == nil {
		return StageRow{}, fmt.Errorf("stage probe fixture maps to no operation")
	}
	evs := make([]engine.OperationEvent, 0, opts.StageEvents)
	for i := 0; i < opts.StageEvents; i++ {
		ev := *base
		ev.Observation = fmt.Sprintf("probe observation seq=%d", i)
		evs = append(evs, ev)
	}
	segs, err := Measure(corpus, opts.Workspace, evs)
	if err != nil {
		return StageRow{}, err
	}
	return StageRow{Scale: sc.Name, Corpus: sc.Size, Segments: segs}, nil
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

// ReportsDir is where generated artifacts land, mirroring benchmarks/.cache.
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
