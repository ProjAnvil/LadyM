//go:build eval

package eval

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestWriteMarkdownSortsScalesAndPreservesScenarioOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	rep := Report{Scenarios: []Scenario{
		{Scale: "small", Fixture: "small-first"},
		{Scale: "large", Fixture: "large-only"},
		{Scale: "small", Fixture: "small-second"},
	}}
	if err := writeMarkdown(path, rep); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	large := strings.Index(report, "## Scale: large\n")
	small := strings.Index(report, "## Scale: small\n")
	if large < 0 || small < 0 || large >= small {
		t.Errorf("scale sections missing or out of order:\n%s", report)
	}
	if strings.Count(report, "## Scale: small\n") != 1 {
		t.Errorf("repeated scale produced duplicate sections:\n%s", report)
	}
	first := strings.Index(report, "small-first")
	second := strings.Index(report, "small-second")
	if first < small || second <= first {
		t.Errorf("scenario rows missing or out of order:\n%s", report)
	}
}

func TestRunScenarioCountsTrialsAndSpawns(t *testing.T) {
	corpus, err := Seed(2, t.TempDir(), 1, evalWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewCounter(Cl100kBase)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Trials = 2
	opts.Spawns = 3
	fx := fixtureByName(t, "claude/tool-read")
	got, err := runScenario(ResolveBinary(t), corpus, fx, Scale{Name: "tiny", Size: 2}, opts, counter)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trials != 2 || got.SpawnMs.N != 6 || got.NewRows != 2 {
		t.Errorf("scenario counts: Trials=%d, spawns=%d, rows=%d; want 2, 6, 2",
			got.Trials, got.SpawnMs.N, got.NewRows)
	}
	for name, stats := range map[string]Summary{
		"persisted": got.PersistedToks, "content": got.ContentToks,
		"summary": got.SummaryToks, "metadata": got.MetadataToks,
		"amplification": got.Amplification,
	} {
		if stats.N != 2 {
			t.Errorf("%s sample count = %d, want one per trial, not per spawn", name, stats.N)
		}
	}
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
