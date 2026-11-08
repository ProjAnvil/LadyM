package engine

import (
	"strconv"
	"testing"
)

func TestBootstrapWholeEntryBudget(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("whole-entry")
		task, err := s.SetTaskState("task", "real goal", "next", 60)
		if err != nil {
			t.Fatal(err)
		}
		limit := len(bootstrapHeader + "\n- id=" + strconv.Quote(task.ID) + " explicit task: ")
		result, err := s.Bootstrap(BootstrapRequest{TaskID: "task", Limit: limit}, nil)
		if err != nil || result.Content != bootstrapHeader || len(result.SourceIDs) != 0 {
			t.Fatalf("partial entry injected: %+v %v", result, err)
		}
	})
}
