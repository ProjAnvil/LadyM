//go:build eval

package eval

import (
	"path/filepath"
	"testing"
)

// TestSeedRowCount asserts every requested row actually persists. Seeding
// bypasses the attention gate on purpose, so a short count means seeding
// dropped writes, not that content was judged noisy.
func TestSeedRowCount(t *testing.T) {
	const size = 120
	path, err := Seed(size, t.TempDir(), 7, evalWorkspace)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := ReadEpisodes(path, evalWorkspace)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	if len(rows) != size {
		t.Errorf("episodes = %d, want %d", len(rows), size)
	}
}

// TestSeedIsDeterministic guards comparability across runs: same seed must put
// byte-identical content on disk, otherwise cross-run numbers are noise.
func TestSeedIsDeterministic(t *testing.T) {
	const size = 90
	firstPath, err := Seed(size, t.TempDir(), 42, evalWorkspace)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	secondPath, err := Seed(size, filepath.Join(t.TempDir(), "nested.db"), 42, evalWorkspace)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	first, err := ReadEpisodes(firstPath, evalWorkspace)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	second, err := ReadEpisodes(secondPath, evalWorkspace)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("row counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Fatalf("row %d content diverges:\n %q\n %q", i, first[i].Content, second[i].Content)
		}
	}
}

// TestSeedDifferentSeedsDiverge stops a copper-plated Seed implementation where
// the seed argument is silently ignored from passing the determinism test.
func TestSeedDifferentSeedsDiverge(t *testing.T) {
	const size = 60
	aPath, err := Seed(size, t.TempDir(), 1, evalWorkspace)
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	bPath, err := Seed(size, t.TempDir(), 2, evalWorkspace)
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	a, err := ReadEpisodes(aPath, evalWorkspace)
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	b, err := ReadEpisodes(bPath, evalWorkspace)
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	same := 0
	for i := range a {
		if a[i].Content == b[i].Content {
			same++
		}
	}
	if same > len(a)/2 {
		t.Errorf("%d/%d rows identical across different seeds, want the corpus to differ", same, len(a))
	}
}

// TestSeedContentHasVariedLength catches a degenerate generator that emits one
// fixed-length string, which would make token statistics meaningless.
func TestSeedContentHasVariedLength(t *testing.T) {
	path, err := Seed(200, t.TempDir(), 11, evalWorkspace)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := ReadEpisodes(path, evalWorkspace)
	if err != nil {
		t.Fatalf("read episodes: %v", err)
	}
	shortest, longest := -1, -1
	for _, r := range rows {
		n := len(r.Content)
		if shortest < 0 || n < shortest {
			shortest = n
		}
		if n > longest {
			longest = n
		}
	}
	if longest <= shortest*2 {
		t.Errorf("content lengths nearly uniform: shortest %d, longest %d", shortest, longest)
	}
}
