//go:build eval

package eval

import (
	"math"
	"sort"
)

// Summary holds descriptive statistics for one sample of measurements.
type Summary struct {
	N      int     `json:"n"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
	P50    float64 `json:"p50"`
	P95    float64 `json:"p95"`
	P99    float64 `json:"p99"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// Summarize computes descriptive statistics over vals. It sorts a private copy
// so the caller's slice keeps its order. An empty sample yields a zero Summary.
func Summarize(vals []float64) Summary {
	if len(vals) == 0 {
		return Summary{}
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(len(sorted))

	var sumSq float64
	for _, v := range sorted {
		d := v - mean
		sumSq += d * d
	}

	return Summary{
		N:      len(sorted),
		Mean:   mean,
		StdDev: math.Sqrt(sumSq / float64(len(sorted))),
		P50:    Percentile(sorted, 50),
		P95:    Percentile(sorted, 95),
		P99:    Percentile(sorted, 99),
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
	}
}

// Percentile returns the linearly interpolated p-th percentile (0-100) of an
// ascending-sorted sample. Interpolation is deliberate: nearest-rank ranking
// overstates high percentiles badly on the small samples this harness runs.
func Percentile(sorted []float64, p float64) float64 {
	switch n := len(sorted); {
	case n == 0:
		return 0
	case n == 1:
		return sorted[0]
	case p <= 0:
		return sorted[0]
	case p >= 100:
		return sorted[n-1]
	}

	rank := (p / 100) * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (rank-float64(lo))*(sorted[hi]-sorted[lo])
}
