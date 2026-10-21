//go:build eval

package eval

import (
	"math"
	"testing"
)

const testFloatTolerance = 1e-9

func TestSummarizeEmptySample(t *testing.T) {
	got := Summarize(nil)
	if got.N != 0 {
		t.Errorf("N = %d, want 0 for an empty sample", got.N)
	}
	if got.Mean != 0 || got.P50 != 0 || got.Max != 0 {
		t.Errorf("empty sample = %+v, want all statistics zero", got)
	}
}

func TestSummarizeOddCount(t *testing.T) {
	got := Summarize([]float64{1, 2, 3, 4, 5})
	if got.N != 5 {
		t.Errorf("N = %d, want 5", got.N)
	}
	if math.Abs(got.Mean-3) > testFloatTolerance {
		t.Errorf("Mean = %v, want 3", got.Mean)
	}
	if got.Min != 1 || got.Max != 5 {
		t.Errorf("Min/Max = %v/%v, want 1/5", got.Min, got.Max)
	}
	if math.Abs(got.P50-3) > testFloatTolerance {
		t.Errorf("P50 = %v, want 3", got.P50)
	}
	if math.Abs(got.P95-4.8) > testFloatTolerance {
		t.Errorf("P95 = %v, want 4.8 (linear interpolation)", got.P95)
	}
	if math.Abs(got.P99-4.96) > testFloatTolerance {
		t.Errorf("P99 = %v, want 4.96", got.P99)
	}
}

func TestSummarizeStdDev(t *testing.T) {
	got := Summarize([]float64{1, 2, 3, 4, 5})
	if math.Abs(got.StdDev-math.Sqrt(2)) > testFloatTolerance {
		t.Errorf("StdDev = %v, want sqrt(2) = %v (population)", got.StdDev, math.Sqrt(2))
	}
}

func TestSummarizeDoesNotReorderCallerSlice(t *testing.T) {
	in := []float64{5, 1, 4, 2, 3}
	Summarize(in)
	want := []float64{5, 1, 4, 2, 3}
	for i := range in {
		if in[i] != want[i] {
			t.Fatalf("caller slice mutated at %d: got %v, want %v", i, in[i], want[i])
		}
	}
}

func TestSummarizeSingleSample(t *testing.T) {
	got := Summarize([]float64{7.5})
	for name, v := range map[string]float64{
		"Mean": got.Mean, "P50": got.P50, "P95": got.P95, "P99": got.P99,
		"Min": got.Min, "Max": got.Max,
	} {
		if math.Abs(v-7.5) > testFloatTolerance {
			t.Errorf("%s = %v, want 7.5 for a single sample", name, v)
		}
	}
	if got.StdDev != 0 {
		t.Errorf("StdDev = %v, want 0 for a single sample", got.StdDev)
	}
}

func TestPercentileEdges(t *testing.T) {
	sorted := []float64{10, 20, 30}
	if got := Percentile(sorted, 0); math.Abs(got-10) > testFloatTolerance {
		t.Errorf("Percentile(p0) = %v, want 10", got)
	}
	if got := Percentile(sorted, 100); math.Abs(got-30) > testFloatTolerance {
		t.Errorf("Percentile(p100) = %v, want 30", got)
	}
	if got := Percentile(sorted, 50); math.Abs(got-20) > testFloatTolerance {
		t.Errorf("Percentile(p50) = %v, want 20", got)
	}
}

func TestSummarizeIsOrderInsensitive(t *testing.T) {
	asc := Summarize([]float64{1, 2, 3, 4, 5})
	desc := Summarize([]float64{5, 4, 3, 2, 1})
	if math.Abs(asc.Mean-desc.Mean) > testFloatTolerance ||
		math.Abs(asc.P95-desc.P95) > testFloatTolerance {
		t.Errorf("statistics differ by input order: %+v vs %+v", asc, desc)
	}
}
