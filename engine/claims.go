package engine

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

var ErrInvalidClaim = schema.ErrInvalidClaim
var ErrMemoryNotFound = schema.ErrMemoryNotFound
var ErrMemoryConflict = storage.ErrMemoryConflict

type ClaimFeedback struct {
	ExpectedRevision int64                `json:"expected_revision"`
	Action           string               `json:"action"`
	Replacement      string               `json:"replacement"`
	Method           string               `json:"method"`
	Reason           string               `json:"reason"`
	Actor            string               `json:"actor"`
	Evidence         []schema.EvidenceRef `json:"evidence_refs"`
}

// RecordEvidence records caller-attested results, never runs or verifies tools.
func (s *Scope) RecordEvidence(kind, content, outcome, actor string) (*schema.Memory, error) {
	if !schema.EvidenceKind(kind) || strings.TrimSpace(content) == "" || len(content) > 65536 ||
		!utf8.ValidString(content) || strings.TrimSpace(actor) == "" {
		return nil, ErrInvalidClaim
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeEvidence)
	m.Workspace, m.Content, m.Source = s.workspace, content, actor
	m.ContentHash = schema.ContentHash(content)
	m.Metadata = map[string]any{"verification_kind": kind, "outcome": outcome, "actor": actor, "knowledge_status": "observed"}
	if err := s.eng.Store.ApplyMemoryBatch(storage.MemoryBatch{AbsentIDs: []string{m.ID}, Writes: []storage.MemoryWrite{{Memory: m}}}); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Scope) evidenceGuards(refs []schema.EvidenceRef, scope schema.ClaimScope, attested bool) ([]storage.MemoryGuard, error) {
	if len(refs) > 64 {
		return nil, ErrInvalidClaim
	}
	guards := []storage.MemoryGuard{}
	seen := map[string]bool{}
	verified := false
	for _, ref := range refs {
		if seen[ref.MemoryID] {
			return nil, ErrInvalidClaim
		}
		seen[ref.MemoryID] = true
		m, err := s.eng.Store.GetMemory(ref.MemoryID)
		if err != nil {
			return nil, err
		}
		if m == nil || m.Workspace != s.workspace || slices.Contains([]schema.MemoryType{schema.TypeClaim, schema.TypeTopicView, schema.TypeMaintenanceJob, schema.TypeTaskState, schema.TypeForwardIntent}, m.Type) ||
			schema.ValidateEvidenceRef(ref, m) != nil {
			return nil, ErrInvalidClaim
		}
		fresh, err := s.IsFresh(m, schema.Now())
		if err != nil {
			return nil, err
		}
		if !fresh {
			return nil, ErrInvalidClaim
		}
		if env := m.MetaString("environment"); env != "" && env != scope.Environment {
			return nil, ErrInvalidClaim
		}
		if version := m.MetaString("code_version"); version != "" && version != scope.CodeVersion {
			return nil, ErrInvalidClaim
		}
		sourceScope, err := sourceScope(m)
		if err != nil || sourceScope.Environment != "" && sourceScope.Environment != scope.Environment ||
			sourceScope.CodeVersion != "" && sourceScope.CodeVersion != scope.CodeVersion {
			return nil, ErrInvalidClaim
		}
		for _, condition := range sourceScope.Conditions {
			if !slices.Contains(scope.Conditions, condition) {
				return nil, ErrInvalidClaim
			}
		}
		verified = verified || m.Type == schema.TypeEvidence &&
			(ref.Kind == "user_confirmation" || (ref.Kind == "test_result" || ref.Kind == "tool_result") && m.MetaString("outcome") == "success")
		guards = append(guards, storage.MemoryGuard{Expected: m})
	}
	if attested && !verified {
		return nil, ErrInvalidClaim
	}
	return guards, nil
}

func (s *Scope) CreateClaim(content string, refs []schema.EvidenceRef, scope schema.ClaimScope, status string, validUntil float64) (*schema.Claim, error) {
	if status == "" {
		status = "inferred"
	}
	if status != "inferred" && status != "user_asserted" || schema.ValidateClaim(content, refs, scope, validUntil) != nil {
		return nil, ErrInvalidClaim
	}
	guards, err := s.evidenceGuards(refs, scope, false)
	if err != nil {
		return nil, err
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeClaim)
	m.Content, m.Workspace, m.ContentHash = content, s.workspace, schema.ContentHash(content)
	c := &schema.Claim{Memory: m, Revision: 1, Status: status, Evidence: refs, Scope: scope, ValidUntil: validUntil}
	schema.SetClaimData(c)
	batch := storage.MemoryBatch{Guards: guards, AbsentIDs: []string{m.ID}}
	setClaimSources(m, refs, &batch)
	batch.Writes = []storage.MemoryWrite{{Memory: m}}
	if err := s.eng.Store.ApplyMemoryBatch(batch); err != nil {
		return nil, err
	}
	return c, nil
}

func setClaimSources(m *schema.Memory, refs []schema.EvidenceRef, batch *storage.MemoryBatch) {
	ids := []string{}
	for _, ref := range refs {
		ids = append(ids, ref.MemoryID)
		e := schema.NewEdge(m.ID, "produced_from", ref.MemoryID)
		e.ID = schema.ProjectedEdgeID(m.ID, e.Relation, ref.MemoryID)
		batch.Edges = append(batch.Edges, e)
	}
	m.Metadata["source_episodes"] = ids
}

func (s *Scope) IsFresh(m *schema.Memory, now float64) (bool, error) {
	if m == nil || m.Workspace != s.workspace {
		return false, nil
	}
	return storage.IsFresh(s.eng.Store, m, now)
}

func (s *Scope) GetClaim(id string) (*schema.Claim, error) {
	m, err := s.eng.Store.GetMemory(id)
	if err != nil {
		return nil, err
	}
	if m == nil || m.Workspace != s.workspace {
		return nil, ErrMemoryNotFound
	}
	c, err := schema.ClaimFromMemory(m)
	if err != nil {
		return nil, err
	}
	fresh, err := s.IsFresh(m, schema.Now())
	if err != nil {
		return nil, err
	}
	if !fresh {
		c.Memory, err = storage.PublicMemory(s.eng.Store, m, schema.Now())
		if c.Status != "rejected" {
			c.Status = "stale"
		}
		c.Verification = nil
	}
	return c, err
}

func (s *Scope) ApplyClaimFeedback(id string, feedback ClaimFeedback) (*schema.Claim, error) {
	m, err := s.eng.Store.GetMemory(id)
	if err != nil {
		return nil, err
	}
	if m == nil || m.Workspace != s.workspace {
		return nil, ErrMemoryNotFound
	}
	c, err := schema.ClaimFromMemory(m.Clone())
	if err != nil {
		return nil, err
	}
	if feedback.ExpectedRevision < 1 || c.Revision != feedback.ExpectedRevision {
		return nil, fmt.Errorf("%w: claim revision", ErrMemoryConflict)
	}
	batch := storage.MemoryBatch{Guards: []storage.MemoryGuard{{Expected: m}}}
	switch feedback.Action {
	case "verify", "correct":
		if strings.TrimSpace(feedback.Method) == "" || strings.TrimSpace(feedback.Actor) == "" {
			return nil, ErrInvalidClaim
		}
		if feedback.Action == "verify" {
			if _, err := s.evidenceGuards(c.Evidence, c.Scope, false); err != nil {
				return nil, err
			}
		} else if schema.ValidateClaim(feedback.Replacement, feedback.Evidence, c.Scope, c.ValidUntil) != nil {
			return nil, ErrInvalidClaim
		}
		guards, err := s.evidenceGuards(feedback.Evidence, c.Scope, true)
		if err != nil {
			return nil, err
		}
		batch.Guards = append(batch.Guards, guards...)
		if feedback.Action == "correct" {
			c.Memory.Content = feedback.Replacement
			c.Memory.ContentHash = schema.ContentHash(feedback.Replacement)
			c.Evidence = feedback.Evidence
			batch.CloseEdgesFor = []string{id}
		} else {
			// Retain original evidence as guards, including refs not repeated by
			// the feedback, so concurrent source edits cannot slip through.
			original, err := s.evidenceGuards(c.Evidence, c.Scope, false)
			if err != nil {
				return nil, err
			}
			batch.Guards = append(batch.Guards, original...)
			for _, ref := range feedback.Evidence {
				if !slices.Contains(c.Evidence, ref) {
					c.Evidence = append(c.Evidence, ref)
				}
			}
			if len(c.Evidence) > 64 {
				return nil, ErrInvalidClaim
			}
		}
		c.Status = "verified"
		c.Verification = &schema.Verification{Method: feedback.Method, Actor: feedback.Actor, CheckedAt: schema.Now(), Evidence: feedback.Evidence}
		delete(c.Memory.Metadata, "evidence_missing")
		setClaimSources(c.Memory, c.Evidence, &batch)
	case "reject":
		if strings.TrimSpace(feedback.Reason) == "" {
			return nil, ErrInvalidClaim
		}
		guards, err := s.evidenceGuards(feedback.Evidence, c.Scope, false)
		if err != nil {
			return nil, err
		}
		batch.Guards = append(batch.Guards, guards...)
		c.Status, c.CounterEvidence = "rejected", feedback.Evidence
		c.Memory.Metadata["rejection_reason"] = feedback.Reason
	default:
		return nil, ErrInvalidClaim
	}
	c.Revision++
	c.Memory.UpdatedAt = schema.Now()
	schema.SetClaimData(c)
	batch.Writes = []storage.MemoryWrite{{Memory: c.Memory, PreserveEmbedding: true}}
	if err := s.eng.Store.ApplyMemoryBatch(batch); err != nil {
		return nil, err
	}
	return c, nil
}

func genericTrustMetadata(metadata map[string]any) bool {
	return metadata["knowledge_status"] == "verified" || genericClaimMetadata(metadata)
}

func genericClaimMetadata(metadata map[string]any) bool {
	for _, key := range []string{"verification_kind", "verification", "claim_data", "claim_revision", "topic_revision", "job_revision", "task_revision"} {
		if _, ok := metadata[key]; ok {
			return true
		}
	}
	return false
}

// Keep JSON-backed scope decoding in the same typed rules as feedback.
func sourceScope(m *schema.Memory) (schema.ClaimScope, error) {
	var scope schema.ClaimScope
	raw, err := json.Marshal(m.Meta("scope"))
	if err == nil {
		err = json.Unmarshal(raw, &scope)
	}
	return scope, err
}

func (e *Engine) RecordEvidence(kind, content, outcome, actor, workspace string) (*schema.Memory, error) {
	return e.Scope(workspace).RecordEvidence(kind, content, outcome, actor)
}

func (e *Engine) CreateClaim(content string, refs []schema.EvidenceRef, scope schema.ClaimScope, status string, validUntil float64, workspace string) (*schema.Claim, error) {
	return e.Scope(workspace).CreateClaim(content, refs, scope, status, validUntil)
}

func (e *Engine) GetClaim(id, workspace string) (*schema.Claim, error) {
	return e.Scope(workspace).GetClaim(id)
}

func (e *Engine) ApplyClaimFeedback(id string, feedback ClaimFeedback, workspace string) (*schema.Claim, error) {
	return e.Scope(workspace).ApplyClaimFeedback(id, feedback)
}
