package schema

import (
	"encoding/json"
	"errors"
)

type MaintenanceJob struct {
	ID             string            `json:"id"`
	Workspace      string            `json:"workspace"`
	Operation      string            `json:"operation"`
	TargetID       string            `json:"target_id"`
	State          string            `json:"state"`
	Priority       int               `json:"priority,omitzero"`
	Owner          string            `json:"owner,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	InputIDs       []string          `json:"input_ids"`
	Fingerprints   map[string]string `json:"fingerprints"`
	InputRevisions map[string]int64  `json:"input_revisions"`
	Args           map[string]any    `json:"args"`
	Revision       int64             `json:"revision"`
	Attempt        int               `json:"attempt"`
	NotBefore      float64           `json:"not_before"`
	LeaseUntil     float64           `json:"lease_until"`
	Checkpoint     map[string]any    `json:"checkpoint,omitempty"`
}

type MaintenanceReport struct {
	CandidateCount      int     `json:"candidate_count"`
	Calls               int     `json:"calls"`
	ReservedBytes       int     `json:"reserved_bytes"`
	MeasuredInputBytes  int     `json:"measured_input_bytes"`
	MeasuredOutputBytes int     `json:"measured_output_bytes"`
	CompletedJobs       int     `json:"completed_jobs"`
	PendingJobs         int     `json:"pending_jobs"`
	FailedJobs          int     `json:"failed_jobs"`
	ElapsedMS           float64 `json:"elapsed_ms"`
	StopReason          string  `json:"stop_reason"`
	Unit                string  `json:"unit"`
}

func MaintenanceJobFromMemory(m *Memory) (*MaintenanceJob, error) {
	if m == nil || m.Type != TypeMaintenanceJob {
		return nil, errors.New("invalid maintenance job")
	}
	raw, err := json.Marshal(m.Meta("job"))
	if err != nil {
		return nil, err
	}
	var job MaintenanceJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if job.ID != m.ID || job.Workspace != m.Workspace || job.Revision < 1 {
		return nil, errors.New("invalid maintenance job identity")
	}
	return &job, nil
}

func (j *MaintenanceJob) SetMemory(m *Memory) {
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	raw, _ := json.Marshal(j)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	m.Metadata["job"] = data
	m.UpdatedAt = Now()
}

// Accounting stamps are not knowledge inputs to the operation that wrote them.
func MaintenanceInputFingerprint(m *Memory) (string, error) {
	if m == nil {
		return "", ErrMemoryNotFound
	}
	input := m.Clone()
	delete(input.Metadata, "maintenance_input_stamp")
	return SourceFingerprint(input)
}
