// Package observability holds LadyM's Prometheus text-exposition registry.
// Hand-rolled (no client_golang) to keep the single-static-binary dependency
// footprint; it imports nothing else from the project so api/engine/cli can
// all depend on it without cycles.
package observability

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// HistogramBuckets are the fixed ladym_http_request_duration_seconds bucket
// upper bounds (seconds); +Inf is implicit at render time.
var HistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type requestKey struct {
	endpoint string
	status   string
}

type histogram struct {
	buckets []uint64 // per-bucket (non-cumulative), len(HistogramBuckets)+1; last = +Inf
	sum     float64
	count   uint64
}

// Registry is a concurrency-safe metrics registry. The zero-value-ready
// global Default() serves production; tests build isolated instances with
// New().
type Registry struct {
	mu        sync.Mutex
	requests  map[requestKey]uint64
	durations map[string]*histogram
	inFlight  int64
	cycles    map[string]uint64
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		requests:  map[requestKey]uint64{},
		durations: map[string]*histogram{},
		cycles:    map[string]uint64{},
	}
}

var defaultRegistry = New()

// Default returns the process-wide registry (api handler and worker share it).
func Default() *Registry { return defaultRegistry }

// IncRequests increments ladym_http_requests_total{endpoint,status}; status is
// a class string ("2xx"/"4xx"/"5xx"), not a raw code.
func (r *Registry) IncRequests(endpoint, statusClass string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[requestKey{endpoint, statusClass}]++
}

// ObserveDuration records one request duration into
// ladym_http_request_duration_seconds{endpoint}.
func (r *Registry) ObserveDuration(endpoint string, seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.durations[endpoint]
	if h == nil {
		h = &histogram{buckets: make([]uint64, len(HistogramBuckets)+1)}
		r.durations[endpoint] = h
	}
	i := sort.SearchFloat64s(HistogramBuckets, seconds)
	h.buckets[i]++
	h.sum += seconds
	h.count++
}

// IncInFlight / DecInFlight track ladym_http_requests_in_flight.
func (r *Registry) IncInFlight() { r.mu.Lock(); r.inFlight++; r.mu.Unlock() }
func (r *Registry) DecInFlight() { r.mu.Lock(); r.inFlight--; r.mu.Unlock() }

// IncSystem2Cycle increments ladym_system2_cycles_total{result} with result in
// ran/skipped/failed.
func (r *Registry) IncSystem2Cycle(result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles[result]++
}

// EndpointStats is the /api/metrics JSON read-out for one endpoint.
type EndpointStats struct {
	Requests int `json:"requests"`
	Errors   int `json:"errors"` // non-2xx responses
}

// EndpointStatsSnapshot exports per-endpoint request/error counts for the
// /api/metrics JSON face (errors = all non-2xx classes).
func (r *Registry) EndpointStatsSnapshot() map[string]EndpointStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]EndpointStats{}
	for k, n := range r.requests {
		st := out[k.endpoint]
		st.Requests += int(n)
		if k.status != "2xx" {
			st.Errors += int(n)
		}
		out[k.endpoint] = st
	}
	return out
}

// DurationSumCount returns the histogram _sum (seconds) and _count for one
// endpoint — the /api/metrics JSON face derives recall_avg_ms from it.
func (r *Registry) DurationSumCount(endpoint string) (sum float64, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.durations[endpoint]; h != nil {
		return h.sum, int(h.count)
	}
	return 0, 0
}

// System2Cycles returns the per-result cycle counts (tests compare deltas).
func (r *Registry) System2Cycles() map[string]uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]uint64, len(r.cycles))
	for k, n := range r.cycles {
		out[k] = n
	}
	return out
}

// escapeLabelValue applies the Prometheus label-value escapes.
func escapeLabelValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}

// Render writes the Prometheus 0.0.4 text exposition of every metric, plus a
// scrape-time ladym_go_goroutines gauge.
func (r *Registry) Render(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fmt.Fprint(w, "# HELP ladym_http_requests_total Total /api/* requests by endpoint and status class.\n")
	fmt.Fprint(w, "# TYPE ladym_http_requests_total counter\n")
	keys := make([]requestKey, 0, len(r.requests))
	for k := range r.requests {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].endpoint != keys[j].endpoint {
			return keys[i].endpoint < keys[j].endpoint
		}
		return keys[i].status < keys[j].status
	})
	for _, k := range keys {
		fmt.Fprintf(w, `ladym_http_requests_total{endpoint="%s",status="%s"} %d`+"\n",
			escapeLabelValue(k.endpoint), k.status, r.requests[k])
	}

	fmt.Fprint(w, "# HELP ladym_http_request_duration_seconds /api/* request duration in seconds.\n")
	fmt.Fprint(w, "# TYPE ladym_http_request_duration_seconds histogram\n")
	endpoints := make([]string, 0, len(r.durations))
	for e := range r.durations {
		endpoints = append(endpoints, e)
	}
	sort.Strings(endpoints)
	for _, e := range endpoints {
		h := r.durations[e]
		le := escapeLabelValue(e)
		var cumulative uint64
		for i, b := range HistogramBuckets {
			cumulative += h.buckets[i]
			fmt.Fprintf(w, `ladym_http_request_duration_seconds_bucket{endpoint="%s",le="%s"} %d`+"\n",
				le, strconv.FormatFloat(b, 'g', -1, 64), cumulative)
		}
		cumulative += h.buckets[len(HistogramBuckets)]
		fmt.Fprintf(w, `ladym_http_request_duration_seconds_bucket{endpoint="%s",le="+Inf"} %d`+"\n", le, cumulative)
		fmt.Fprintf(w, `ladym_http_request_duration_seconds_sum{endpoint="%s"} %s`+"\n",
			le, strconv.FormatFloat(h.sum, 'g', -1, 64))
		fmt.Fprintf(w, `ladym_http_request_duration_seconds_count{endpoint="%s"} %d`+"\n", le, h.count)
	}

	fmt.Fprint(w, "# HELP ladym_http_requests_in_flight /api/* requests currently being served.\n")
	fmt.Fprint(w, "# TYPE ladym_http_requests_in_flight gauge\n")
	fmt.Fprintf(w, "ladym_http_requests_in_flight %d\n", r.inFlight)

	fmt.Fprint(w, "# HELP ladym_system2_cycles_total System2 cycles by result (ran/skipped/failed).\n")
	fmt.Fprint(w, "# TYPE ladym_system2_cycles_total counter\n")
	results := make([]string, 0, len(r.cycles))
	for res := range r.cycles {
		results = append(results, res)
	}
	sort.Strings(results)
	for _, res := range results {
		fmt.Fprintf(w, `ladym_system2_cycles_total{result="%s"} %d`+"\n", escapeLabelValue(res), r.cycles[res])
	}

	fmt.Fprint(w, "# HELP ladym_go_goroutines Current number of goroutines.\n")
	fmt.Fprint(w, "# TYPE ladym_go_goroutines gauge\n")
	fmt.Fprintf(w, "ladym_go_goroutines %d\n", runtime.NumGoroutine())
}
