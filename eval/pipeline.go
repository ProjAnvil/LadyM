//go:build eval

package eval

import (
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// DriftTolerance is how far stage-by-stage replication may deviate from the real
// write before its breakdown counts as untrustworthy.
const DriftTolerance = 0.20

// Segments is the stage-by-stage breakdown of one hook write together with the
// real end-to-end write that validates it. Scope is a stage in its own right
// because the hook builds a fresh Scope per event, and that allocation is real
// cost, not bookkeeping.
type Segments struct {
	// EngineOpenMs is the cost of opening an engine against the corpus, which the
	// hook pays once per process and the in-process breakdown cannot see.
	EngineOpenMs float64 `json:"engine_open_ms"`
	ScopeMs      float64 `json:"scope_ms"`
	NormalizeMs  float64 `json:"normalize_ms"`
	ResolveMs   float64 `json:"resolve_ms"`
	RecordMs    float64 `json:"record_ms"`
	EdgesMs     float64 `json:"edges_ms"`
	TotalMs     float64 `json:"total_ms"`
	RealMs      float64 `json:"real_ms"`
	Drift       float64 `json:"drift"`
}

// Measure compares stage-by-stage replication against the real write path. Both
// sides write the same episodes into their OWN copy of the corpus, then report
// medians. The separate copies are essential: on one shared file whichever path
// runs first heats the SQLite page cache and flatters the other. Single samples
// are useless here because the work is sub-millisecond and dominated by timer
// noise, hence the many-events-then-median approach.
func Measure(corpus, workspace string, evs []engine.OperationEvent) (Segments, error) {
	if len(evs) == 0 {
		return Segments{}, fmt.Errorf("measure: no events to write")
	}

	replDir, err := os.MkdirTemp("", "ladym-eval-replicate")
	if err != nil {
		return Segments{}, err
	}
	defer os.RemoveAll(replDir)
	replDB, err := copyCorpus(corpus, replDir)
	if err != nil {
		return Segments{}, err
	}
	replicated, err := replicateAll(replDB, replDir, workspace, evs)
	if err != nil {
		return Segments{}, err
	}

	realDir, err := os.MkdirTemp("", "ladym-eval-real")
	if err != nil {
		return Segments{}, err
	}
	defer os.RemoveAll(realDir)
	realDB, err := copyCorpus(corpus, realDir)
	if err != nil {
		return Segments{}, err
	}
	realVals, err := realAll(realDB, realDir, workspace, evs)
	if err != nil {
		return Segments{}, err
	}

	got := Segments{
		ScopeMs:     medianField(replicated, func(s Segments) float64 { return s.ScopeMs }),
		NormalizeMs: medianField(replicated, func(s Segments) float64 { return s.NormalizeMs }),
		ResolveMs:   medianField(replicated, func(s Segments) float64 { return s.ResolveMs }),
		RecordMs:    medianField(replicated, func(s Segments) float64 { return s.RecordMs }),
		EdgesMs:     medianField(replicated, func(s Segments) float64 { return s.EdgesMs }),
		TotalMs:     medianField(replicated, func(s Segments) float64 { return s.TotalMs }),
		RealMs:      Summarize(realVals).P50,
	}
	if got.RealMs > 0 {
		got.Drift = math.Abs(got.TotalMs-got.RealMs) / got.RealMs
	} else {
		got.Drift = 1
	}
	return got, nil
}

// replicateOne walks the same steps engine.Scope.RecordOperation takes, timing
// each. Stages are the only way to see which part of a write dominates; the real
// call reports a single opaque number.
func replicateOne(eng *engine.Engine, workspace string, ev engine.OperationEvent) (Segments, error) {
	cfg := eng.Config
	var s Segments

	// The hook builds one Scope per event, so replication pays that too.
	start := time.Now()
	sc := eng.Scope(workspace)
	s.ScopeMs = sinceMs(start)

	start = time.Now()
	meta := maps.Clone(ev.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	actionClass := ev.ActionClass
	if actionClass == "" {
		actionClass = operations.NormalizeAction(ev.Action, cfg)
	}
	if actionClass != "" {
		meta["action_class"] = actionClass
	}
	if ev.Action != "" {
		meta["action_raw"] = ev.Action
	}
	s.NormalizeMs = sinceMs(start)

	// Resource-bound stages are gated rather than timed empty: an empty loop
	// still costs tens of nanoseconds, and reporting that as work would imply
	// an event with no resources paid for resource resolution.
	var resources []*schema.Memory
	if len(ev.Resources) > 0 {
		start := time.Now()
		for _, name := range ev.Resources {
			res, _, err := operations.ResolveResource(eng.Store, eng.Provider, cfg, workspace, name)
			if err != nil {
				return Segments{}, fmt.Errorf("resolve resource %q: %w", name, err)
			}
			resources = append(resources, res)
		}
		s.ResolveMs = sinceMs(start)
	}

	start = time.Now()
	mem, err := sc.Episodic.Record(ev.Agent, ev.Action, ev.Observation, ev.Outcome, ev.Tags, meta)
	if err != nil {
		return Segments{}, fmt.Errorf("record episode: %w", err)
	}
	s.RecordMs = sinceMs(start)

	if len(resources) > 0 {
		start := time.Now()
		for _, res := range resources {
			e := schema.NewEdge(mem.ID, "operates_on", res.ID)
			e.ID = schema.ProjectedEdgeID(mem.ID, "operates_on", res.ID)
			if err := eng.Store.PutEdge(e); err != nil {
				return Segments{}, fmt.Errorf("put edge: %w", err)
			}
		}
		s.EdgesMs = sinceMs(start)
	}

	s.TotalMs = s.ScopeMs + s.NormalizeMs + s.ResolveMs + s.RecordMs + s.EdgesMs
	return s, nil
}

func replicateAll(dbPath, dir, workspace string, evs []engine.OperationEvent) ([]Segments, error) {
	eng, err := openCorpusEngine(dir, dbPath, workspace)
	if err != nil {
		return nil, err
	}
	defer eng.Close()

	out := make([]Segments, 0, len(evs))
	for _, ev := range evs {
		s, err := replicateOne(eng, workspace, ev)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func realAll(dbPath, dir, workspace string, evs []engine.OperationEvent) ([]float64, error) {
	eng, err := openCorpusEngine(dir, dbPath, workspace)
	if err != nil {
		return nil, err
	}
	defer eng.Close()

	out := make([]float64, 0, len(evs))
	for _, ev := range evs {
		// Scope creation counts toward the real write exactly as it does in the
		// hook: one Scope per event, then one RecordOperation through it.
		start := time.Now()
		sc := eng.Scope(workspace)
		if _, err := sc.RecordOperation(ev); err != nil {
			return nil, fmt.Errorf("real RecordOperation: %w", err)
		}
		out = append(out, sinceMs(start))
	}
	return out, nil
}

// EngineOpenMs times engine construction against a corpus copy. Every hook
// invocation pays this once, and it is invisible to the in-process stage
// breakdown, so without it the split between startup and write work would be an
// inference rather than a measurement.
func EngineOpenMs(corpus, workspace string, runs int) (float64, error) {
	dir, err := os.MkdirTemp("", "ladym-eval-engineopen")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)

	db, err := copyCorpus(corpus, dir)
	if err != nil {
		return 0, err
	}
	vals := make([]float64, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		eng, err := openCorpusEngine(dir, db, workspace)
		if err != nil {
			return 0, err
		}
		vals = append(vals, sinceMs(start))
		eng.Close()
	}
	return Summarize(vals).P50, nil
}

// openCorpusEngine opens an engine bound to an existing corpus file.
func openCorpusEngine(dir, dbPath, workspace string) (*engine.Engine, error) {
	cfg := config.ForTesting(dir)
	cfg.DBPath = dbPath
	cfg.Workspace = workspace
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("engine on %s: %w", dbPath, err)
	}
	return eng, nil
}

// copyCorpus clones a corpus file into dir under its original name so each
// measurement starts from identical bytes on disk.
func copyCorpus(template, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(template))
	src, err := os.Open(template)
	if err != nil {
		return "", fmt.Errorf("open corpus template: %w", err)
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("create corpus copy: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("copy corpus: %w", err)
	}
	return dst, nil
}

// sinceMs converts an elapsed duration to milliseconds with sub-microsecond
// resolution, matching the unit every other field in this package uses.
func sinceMs(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
}

// medianField summarises one stage across samples.
func medianField(ss []Segments, pick func(Segments) float64) float64 {
	vals := make([]float64, 0, len(ss))
	for _, s := range ss {
		vals = append(vals, pick(s))
	}
	return Summarize(vals).P50
}
