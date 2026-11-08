package observability

import (
	"github.com/ProjAnvil/LadyM/schema"
	"strings"
	"testing"
)

func TestMaintenanceMetrics(t *testing.T) {
	r := New()
	r.ObserveMaintenanceJob("secret-workspace/job-id", "user-text")
	r.ObserveMaintenanceJob("new_episode", "done")
	r.ObserveMaintenanceSources(5)
	r.ObserveMaintenanceStaleRejection()
	r.ObserveMaintenanceTopicSize(100, 50)
	r.ObserveMaintenanceCycle(&schema.MaintenanceReport{Calls: 2, ReservedBytes: 4000, MeasuredInputBytes: 2000, MeasuredOutputBytes: 100, StopReason: "reserved_bytes", PendingJobs: 2, Unit: "bytes"})
	text := render(t, r)
	if !strings.Contains(text, "ladym_maintenance_source_references_total 5") || !strings.Contains(text, "ladym_maintenance_stale_rejections_total 1") || !strings.Contains(text, "ladym_maintenance_topic_expansion_ratio_sum 2") {
		t.Fatal("missing source/stale/topic-size measurements")
	}
	for _, want := range []string{`operation="new_episode",state="done"`, `operation="unknown",state="unknown"`, `kind="reserved",unit="bytes"} 4000`, `reason="reserved_bytes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(text, "secret-workspace") || strings.Contains(text, "job-id") || strings.Contains(text, "user-text") || strings.Contains(text, `unit="tokens"`) {
		t.Fatal("high-cardinality or incorrect units")
	}
}
