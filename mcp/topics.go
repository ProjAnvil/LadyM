package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/ProjAnvil/LadyM/schema"
)

func (s *server) topicTools() []toolDef {
	stringsProp := map[string]any{"type": "array", "items": strProp()}
	return []toolDef{
		{Name: "build_topic", Description: "CAS source-grounded topic navigation, no inference or LLM.", InputSchema: objSchema(map[string]any{
			"topic": strProp(), "input_ids": map[string]any{"type": "array", "items": strProp(), "minItems": 1, "maxItems": 64},
			"expected_revision": map[string]any{"type": "integer", "minimum": 0}, "workspace": strProp(),
		}, "topic", "input_ids", "expected_revision")},
		{Name: "get_topic", Description: "Get topic by name; stale bodies suppressed.", InputSchema: objSchema(map[string]any{"topic": strProp(), "workspace": strProp()}, "topic")},
		{Name: "hybrid_recall", Description: "Exact lexical + scoped vectors + bounded graph; stable RRF60.", InputSchema: objSchema(map[string]any{
			"query": strProp(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}, "workspace": strProp(),
		}, "query", "limit")},
		{Name: "bootstrap", Description: "Reference-only verified navigation, bytes only (default/max4096). No token estimates.", InputSchema: objSchema(map[string]any{
			"topic_ids": stringsProp, "environment": strProp(), "code_version": strProp(), "conditions": stringsProp, "task_id": strProp(),
			"unit": map[string]any{"type": "string", "enum": []string{"bytes"}, "default": "bytes"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 4096}, "workspace": strProp(),
		})},
		{Name: "set_task_state", Description: "Explicit user goal/next step only; TTL seconds defaults to24h. Never predictions.", InputSchema: objSchema(map[string]any{
			"id": strProp(), "goal": strProp(), "next_step": strProp(), "ttl_s": map[string]any{"type": "number", "minimum": 0, "default": float64(86400)}, "workspace": strProp(),
		}, "id", "goal")},
	}
}

func (s *server) callTopic(name string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil || args == nil {
		return "", schema.ErrInvalidClaim
	}
	var result any
	switch name {
	case "build_topic":
		var req schema.BuildTopicRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		result, err = s.eng.Scope(req.Workspace).BuildTopic(req.Topic, req.InputIDs, req.ExpectedRevision)
	case "get_topic":
		var req struct {
			Topic     string `json:"topic"`
			Workspace string `json:"workspace"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		result, err = s.eng.Scope(req.Workspace).GetTopic(req.Topic)
	case "hybrid_recall":
		var req schema.HybridRecallRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		result, err = s.eng.Scope(req.Workspace).HybridRecall(req.Query, req.Limit)
	case "bootstrap":
		var req schema.BootstrapRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		if req.Unit != "" && req.Unit != "bytes" {
			return "", fmt.Errorf("remote bootstrap supports bytes only; tokens require a host TokenCounter")
		}
		result, err = s.eng.Scope(req.Workspace).Bootstrap(req, nil)
	case "set_task_state":
		var req schema.SetTaskStateRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		result, err = s.eng.Scope(req.Workspace).SetTaskState(req.ID, req.Goal, req.Next, req.TTLS)
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
