package schema

type ScheduleMaintenanceRequest struct {
	Workspace string         `json:"workspace,omitempty"`
	Operation string         `json:"operation"`
	TargetID  string         `json:"target_id"`
	InputIDs  []string       `json:"input_ids"`
	Args      map[string]any `json:"args,omitempty"`
}
