// Package observability is LadyM's internal metrics facade over
// prometheus/client_golang (the de-facto standard, kept behind this package so
// api/engine/cli share one metric surface without import cycles). Each
// Registry owns a private prometheus.Registry — the global default is never
// used, which keeps tests isolated.
package observability

import (
	"net/http"
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

// HistogramBuckets are the ladym_http_request_duration_seconds bucket upper
// bounds (seconds).
var HistogramBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// Registry is a concurrency-safe metrics facade. The process-wide Default()
// serves production; tests build isolated instances with New().
type Registry struct {
	reg       *prometheus.Registry
	requests  *prometheus.CounterVec
	durations *prometheus.HistogramVec
	inFlight  prometheus.Gauge
	cycles    *prometheus.CounterVec
}

// New returns a registry with all ladym_* metrics registered.
func New() *Registry {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	r := &Registry{
		reg: reg,
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "ladym_http_requests_total",
			Help: "Total /api/* requests by endpoint and status class.",
		}, []string{"endpoint", "status"}),
		durations: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ladym_http_request_duration_seconds",
			Help:    "/api/* request duration in seconds.",
			Buckets: HistogramBuckets,
		}, []string{"endpoint"}),
		inFlight: factory.NewGauge(prometheus.GaugeOpts{
			Name: "ladym_http_requests_in_flight",
			Help: "/api/* requests currently being served.",
		}),
		cycles: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "ladym_system2_cycles_total",
			Help: "System2 cycles by result (ran/skipped/failed).",
		}, []string{"result"}),
	}
	// ladym_go_goroutines stays a single hand-registered gauge (not
	// collectors.NewGoCollector) so a scrape exposes the documented ladym_*
	// surface only, without the full go_* metric set.
	factory.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "ladym_go_goroutines",
		Help: "Current number of goroutines.",
	}, func() float64 { return float64(runtime.NumGoroutine()) })
	return r
}

var defaultRegistry = New()

// Default returns the process-wide registry (api handler and worker share it).
func Default() *Registry { return defaultRegistry }

// IncRequests increments ladym_http_requests_total{endpoint,status}; status is
// a class string ("2xx"/"4xx"/"5xx"), not a raw code.
func (r *Registry) IncRequests(endpoint, statusClass string) {
	r.requests.WithLabelValues(endpoint, statusClass).Inc()
}

// ObserveDuration records one request duration into
// ladym_http_request_duration_seconds{endpoint}.
func (r *Registry) ObserveDuration(endpoint string, seconds float64) {
	r.durations.WithLabelValues(endpoint).Observe(seconds)
}

// IncInFlight / DecInFlight track ladym_http_requests_in_flight.
func (r *Registry) IncInFlight() { r.inFlight.Inc() }
func (r *Registry) DecInFlight() { r.inFlight.Dec() }

// IncSystem2Cycle increments ladym_system2_cycles_total{result} with result in
// ran/skipped/failed.
func (r *Registry) IncSystem2Cycle(result string) {
	r.cycles.WithLabelValues(result).Inc()
}

// HTTPHandler serves the Prometheus text exposition of this registry.
func (r *Registry) HTTPHandler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// EndpointStats is the /api/metrics JSON read-out for one endpoint.
type EndpointStats struct {
	Requests int `json:"requests"`
	Errors   int `json:"errors"` // non-2xx responses
}

// mustGather gathers the metric families; a Gather error means an
// inconsistent metric, which the JSON read-out degrades around (empty data)
// rather than failing the endpoint.
func (r *Registry) mustGather() []*dto.MetricFamily {
	families, _ := r.reg.Gather()
	return families
}

// EndpointStatsSnapshot exports per-endpoint request/error counts for the
// /api/metrics JSON face (errors = all non-2xx classes).
func (r *Registry) EndpointStatsSnapshot() map[string]EndpointStats {
	out := map[string]EndpointStats{}
	for _, mf := range r.mustGather() {
		if mf.GetName() != "ladym_http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			endpoint, status := "", ""
			for _, lp := range m.GetLabel() {
				switch lp.GetName() {
				case "endpoint":
					endpoint = lp.GetValue()
				case "status":
					status = lp.GetValue()
				}
			}
			st := out[endpoint]
			st.Requests += int(m.GetCounter().GetValue())
			if status != "2xx" {
				st.Errors += int(m.GetCounter().GetValue())
			}
			out[endpoint] = st
		}
	}
	return out
}

// DurationSumCount returns the histogram _sum (seconds) and _count for one
// endpoint — the /api/metrics JSON face derives recall_avg_ms from it.
func (r *Registry) DurationSumCount(endpoint string) (sum float64, count int) {
	for _, mf := range r.mustGather() {
		if mf.GetName() != "ladym_http_request_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "endpoint" && lp.GetValue() == endpoint {
					return m.GetHistogram().GetSampleSum(), int(m.GetHistogram().GetSampleCount())
				}
			}
		}
	}
	return 0, 0
}

// System2Cycles returns the per-result cycle counts (tests compare deltas).
func (r *Registry) System2Cycles() map[string]uint64 {
	out := map[string]uint64{}
	for _, mf := range r.mustGather() {
		if mf.GetName() != "ladym_system2_cycles_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "result" {
					out[lp.GetValue()] = uint64(m.GetCounter().GetValue())
				}
			}
		}
	}
	return out
}
