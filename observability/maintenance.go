package observability

import "github.com/ProjAnvil/LadyM/schema"

func (r *Registry) ObserveMaintenanceJob(operation, state string) {
	switch operation {
	case "new_episode", "refresh_topic", "expire_claim":
	default:
		operation = "unknown"
	}
	switch state {
	case "pending", "running", "done", "failed", "superseded":
	default:
		state = "unknown"
	}
	r.jobs.WithLabelValues(operation, state).Inc()
	if state == "running" {
		r.attempts.WithLabelValues(operation).Inc()
	}
}

func (r *Registry) ObserveMaintenanceCycle(report *schema.MaintenanceReport) {
	reason := report.StopReason
	switch reason {
	case "":
		reason = "complete"
	case "calls", "reserved_bytes", "candidates", "wall_or_cancel":
	default:
		reason = "unknown"
	}
	r.stops.WithLabelValues(reason).Inc()
	r.budget.WithLabelValues("reserved", "bytes").Add(float64(report.ReservedBytes))
	r.budget.WithLabelValues("input", "bytes").Add(float64(report.MeasuredInputBytes))
	r.budget.WithLabelValues("output", "bytes").Add(float64(report.MeasuredOutputBytes))
	r.pending.Set(float64(report.PendingJobs))
	r.callCount.Add(float64(report.Calls))
	r.elapsed.Observe(report.ElapsedMS / 1000)
}

func (r *Registry) ObserveMaintenanceSources(count int) { r.sources.Add(float64(max(0, count))) }
func (r *Registry) ObserveMaintenanceStaleRejection()   { r.staleJobs.Inc() }
func (r *Registry) ObserveMaintenanceTopicSize(outputBytes, sourceBytes int) {
	if sourceBytes > 0 {
		r.topicSize.Observe(float64(outputBytes) / float64(sourceBytes))
	}
}
