package mcp

import (
	"encoding/json"

	"github.com/ProjAnvil/LadyM/schema"
)

func evidenceRefsProp() map[string]any {
	kind := strProp()
	kind["enum"] = []string{"observation", "test_result", "tool_result", "user_confirmation"}
	runeIndex := map[string]any{"type": "integer", "minimum": 0, "default": 0}
	ref := objSchema(map[string]any{
		"memory_id": strProp(), "fingerprint": strProp(), "kind": kind,
		"start_rune": runeIndex, "end_rune": runeIndex,
	}, "memory_id", "fingerprint", "kind")
	return map[string]any{"type": "array", "maxItems": 64, "items": ref}
}

func (s *server) claimTools() []toolDef {
	evidence := objSchema(map[string]any{
		"kind":    map[string]any{"type": "string", "enum": []string{"observation", "test_result", "tool_result", "user_confirmation"}},
		"content": strProp(), "outcome": strProp(), "workspace": strProp(),
	}, "kind", "content")
	scope := objSchema(map[string]any{
		"environment": strProp(), "code_version": strProp(),
		"conditions": map[string]any{"type": "array", "items": strProp(), "maxItems": 32},
	})
	return []toolDef{
		{Name: "record_evidence", Description: "Record caller-attested evidence, actor=mcp. Does not execute tools.", InputSchema: evidence},
		{Name: "archive_evidence", Description: "Opt-in bounded, best-effort redacted evidence archive. Content strings only, no file import.", InputSchema: evidence},
		{Name: "create_claim", Description: "Create an inferred or user_asserted claim from scoped evidence; never directly verified.", InputSchema: objSchema(map[string]any{
			"content": strProp(), "evidence_refs": evidenceRefsProp(), "scope": scope, "workspace": strProp(),
			"status":      map[string]any{"type": "string", "enum": []string{"inferred", "user_asserted"}, "default": "inferred"},
			"valid_until": map[string]any{"type": "number", "minimum": 0},
		}, "content")},
		{Name: "get_claim", Description: "Read a scoped claim with freshness checking; stale bodies are suppressed.", InputSchema: objSchema(map[string]any{"memory_id": strProp(), "workspace": strProp()}, "memory_id")},
		{Name: "claim_feedback", Description: "CAS verify/correct/reject; requires caller-attested evidence for verification. Actor=mcp.", InputSchema: objSchema(map[string]any{
			"memory_id": strProp(), "workspace": strProp(), "expected_revision": map[string]any{"type": "integer", "minimum": 1},
			"action":      map[string]any{"type": "string", "enum": []string{"verify", "correct", "reject"}},
			"replacement": strProp(), "method": strProp(), "reason": strProp(), "evidence_refs": evidenceRefsProp(),
		}, "memory_id", "expected_revision", "action")},
	}
}

func (s *server) callClaim(name string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil || args == nil {
		return "", schema.ErrInvalidClaim
	}
	var result any
	switch name {
	case "record_evidence", "archive_evidence":
		var req schema.RecordEvidenceRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		scope := s.eng.Scope(req.Workspace)
		if name == "archive_evidence" {
			result, err = scope.ArchiveEvidence(req.Kind, req.Content, req.Outcome, "mcp")
		} else {
			result, err = scope.RecordEvidence(req.Kind, req.Content, req.Outcome, "mcp")
		}
	case "create_claim":
		var req schema.CreateClaimRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return "", schema.ErrInvalidClaim
		}
		result, err = s.eng.Scope(req.Workspace).CreateClaim(req.Content, req.Evidence, req.Scope, req.Status, req.ValidUntil)
	case "get_claim", "claim_feedback":
		var req struct {
			schema.ClaimFeedbackRequest
			MemoryID string `json:"memory_id"`
		}
		if err := json.Unmarshal(raw, &req); err != nil || req.MemoryID == "" {
			return "", schema.ErrInvalidClaim
		}
		scope := s.eng.Scope(req.Workspace)
		if name == "get_claim" {
			result, err = scope.GetClaim(req.MemoryID)
		} else {
			if req.ExpectedRevision < 1 {
				return "", schema.ErrInvalidClaim
			}
			req.Actor = "mcp"
			result, err = scope.ApplyClaimFeedback(req.MemoryID, req.ClaimFeedback)
		}
	}
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
