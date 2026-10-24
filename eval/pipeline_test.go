//go:build eval

package eval

import (
	"fmt"
	"testing"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/hook"
)

// DriftTolerance is how far the stage-by-stage replication may deviate from the
// real write before its breakdown is considered untrustworthy.
// pipelineEvents turns one fixture into n distinct events so repeated writes are
// not deduplicated away and can be summarised into a median.
func pipelineEvents(t *testing.T, fx Fixture, n int) []engine.OperationEvent {
	t.Helper()
	p, err := hook.Parse([]byte(fx.mustPayload(t)))
	if err != nil {
		t.Fatalf("parse fixture payload: %v", err)
	}
	base := p.ToOperation(fx.Harness)
	if base == nil {
		t.Fatalf("fixture %s maps to no operation", fx.Name)
	}
	out := make([]engine.OperationEvent, 0, n)
	for i := range n {
		ev := *base
		ev.Observation = fmt.Sprintf("%s seq=%d", base.Observation, i)
		out = append(out, ev)
	}
	return out
}

func fixtureByName(t *testing.T, name string) Fixture {
	t.Helper()
	for _, fx := range Fixtures() {
		if fx.Name == name {
			return fx
		}
	}
	t.Fatalf("no fixture named %q", name)
	return Fixture{}
}

// TestMeasureDriftWithinTolerance is the credibility gate for stage-level data:
// replication must track the real write closely, otherwise the breakdown is an
// artifact of reimplementation rather than a measurement of the product.
func TestMeasureDriftWithinTolerance(t *testing.T) {
	corpus, err := Seed(300, t.TempDir(), 5, evalWorkspace)
	if err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	fx := fixtureByName(t, "claude/tool-read")
	evs := pipelineEvents(t, fx, 40)

	got, err := Measure(corpus, evalWorkspace, evs)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if got.RealMs <= 0 {
		t.Fatalf("RealMs = %v, want positive", got.RealMs)
	}
	if got.Drift > DriftTolerance {
		t.Errorf("drift = %.3f, want <= %.2f (replicated %.3fms vs real %.3fms)",
			got.Drift, DriftTolerance, got.TotalMs, got.RealMs)
	}
	t.Logf("replicated=%.3fms real=%.3fms drift=%.3f (scope=%.3f normalize=%.3f resolve=%.3f record=%.3f edges=%.3f)",
		got.TotalMs, got.RealMs, got.Drift, got.ScopeMs, got.NormalizeMs, got.ResolveMs, got.RecordMs, got.EdgesMs)
}

// TestMeasureResourcelessEventHasNoresourceStages asserts the two resource-bound
// stages stay exactly zero when the event carries no resource, which is what
// makes missing them attributable rather than just small.
func TestMeasureResourcelessEventHasNoResourceStages(t *testing.T) {
	corpus, err := Seed(200, t.TempDir(), 6, evalWorkspace)
	if err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	fx := fixtureByName(t, "claude/tool-bash-no-resource")
	evs := pipelineEvents(t, fx, 12)

	got, err := Measure(corpus, evalWorkspace, evs)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if got.ResolveMs != 0 {
		t.Errorf("ResolveMs = %.6fms, want exactly 0 for an event without resources", got.ResolveMs)
	}
	if got.EdgesMs != 0 {
		t.Errorf("EdgesMs = %.6fms, want exactly 0 for an event without resources", got.EdgesMs)
	}
	if got.RecordMs <= 0 {
		t.Errorf("RecordMs = %.6fms, want positive: an episode was still written", got.RecordMs)
	}
}

// TestMeasureLeavesBothCopiesEquallyUsed checks the anti-warm-bias property in
// the only observable way: after Measure, each side's database must hold exactly
// the writes it made, which proves the two paths never shared a file.
func TestMeasureLeavesBothCopiesEquallyUsed(t *testing.T) {
	corpus, err := Seed(50, t.TempDir(), 7, evalWorkspace)
	if err != nil {
		t.Fatalf("seed corpus: %v", err)
	}
	before, err := ReadEpisodes(corpus, evalWorkspace)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	fx := fixtureByName(t, "claude/tool-read")
	if _, err := Measure(corpus, evalWorkspace, pipelineEvents(t, fx, 10)); err != nil {
		t.Fatalf("measure: %v", err)
	}

	after, err := ReadEpisodes(corpus, evalWorkspace)
	if err != nil {
		t.Fatalf("read corpus after measure: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("template corpus changed: %d rows before, %d after; Measure must write only into its own copies",
			len(before), len(after))
	}
}
