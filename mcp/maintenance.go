package mcp

import (
	"encoding/json"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

func (s *server) maintenanceTools() []toolDef {
	return []toolDef{
		{Name: "schedule_maintenance", Description: "Persist a bounded, allowlisted maintenance request. Never execute source commands.", InputSchema: objSchema(map[string]any{
			"operation": map[string]any{"type": "string", "enum": []string{"new_episode", "refresh_topic", "expire_claim"}},
			"target_id": strProp(), "input_ids": map[string]any{"type": "array", "items": strProp(), "maxItems": 64},
			"args": map[string]any{"type": "object"}, "workspace": strProp(),
		}, "operation", "target_id")},
		{Name: "list_maintenance_jobs", Description: "List scoped persisted job states and byte-budget checkpoints.", InputSchema: objSchema(map[string]any{
			"workspace": strProp(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 1000, "default": 100},
		})},
		{Name: "maintenance_status", Description: "Read persisted cycle report; input/output/reservations are bytes, not billing tokens.", InputSchema: objSchema(map[string]any{"workspace": strProp()})},
	}
}

func (s *server) callMaintenance(name string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil || args == nil {
		return "", engine.ErrInvalidMaintenance
	}
	var req schema.ScheduleMaintenanceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", engine.ErrInvalidMaintenance
	}
	scope := s.eng.Scope(req.Workspace)
	var result any
	switch name {
	case "schedule_maintenance":
		result, err = scope.ScheduleMaintenance(req.Operation, req.TargetID, req.InputIDs, req.Args)
	case "list_maintenance_jobs":
		limit := 100
		var bound struct {
			Limit *int `json:"limit"`
		}
		if err := json.Unmarshal(raw, &bound); err != nil {
			return "", engine.ErrInvalidMaintenance
		}
		if bound.Limit != nil {
			limit = *bound.Limit
		}
		result, err = scope.ListMaintenanceJobs(limit)
	case "maintenance_status":
		result, err = scope.MaintenanceStatus()
	default:
		return "", engine.ErrInvalidMaintenance
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
