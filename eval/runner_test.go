//go:build eval

package eval

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// repoPath resolves the module root so reports land in eval/.cache regardless of
// how the test binary was invoked.
func repoPath(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// TestEval is the entry point invoked by `make eval-hook`. It runs the full
// matrix and writes metrics.json plus report.md under eval/.cache/reports/.
//
// This is deliberately not an assertion-heavy test: numbers from a measurement
// run vary by machine, so hard thresholds here would be flaky gatekeeping. The
// structural invariants below are checked, everything else is reported.
func TestEval(t *testing.T) {
	if testing.Short() {
		t.Skip("eval harness: skipped under -short")
	}
	bin := ResolveBinary(t)

	counter, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}

	opts := DefaultOptions()
	opts.Bin = bin
	if n := os.Getenv("LADYM_EVAL_TRIALS"); n != "" {
		parsed, err := strconv.Atoi(n)
		if err != nil || parsed <= 0 {
			t.Fatalf("LADYM_EVAL_TRIALS = %q, want a positive integer", n)
		}
		opts.Trials = parsed
	}

	start := time.Now()
	rep, err := Evaluate(opts, counter)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	t.Logf("matrix finished in %v", time.Since(start).Round(time.Millisecond))

	// Structural invariants: every cell ran, and the stage breakdown stays within
	// the credibility tolerance the drift test established.
	want := len(StandardScales) * len(Fixtures())
	if len(rep.Scenarios) != want {
		t.Errorf("scenarios = %d, want %d", len(rep.Scenarios), want)
	}
	if len(rep.Stages) != len(StandardScales) {
		t.Errorf("stages = %d, want %d", len(rep.Stages), len(StandardScales))
	}
	for _, s := range rep.Stages {
		if s.Segments.Drift > DriftTolerance {
			t.Errorf("stage drift at %s = %.3f, want <= %.2f: breakdown is untrustworthy",
				s.Scale, s.Segments.Drift, DriftTolerance)
		}
	}

	dir, err := WriteReport(filepath.Join(repoPath(t), "eval"), rep)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("report written to %s", dir)

	for _, s := range rep.Stages {
		t.Logf("stages %-6s corpus=%d total=%.3fms real=%.3fms drift=%.3f",
			s.Scale, s.Corpus, s.Segments.TotalMs, s.Segments.RealMs, s.Segments.Drift)
	}
}
