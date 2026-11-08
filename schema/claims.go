package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

var ErrInvalidClaim = errors.New("invalid claim")
var ErrMemoryNotFound = errors.New("memory not found")

type EvidenceRef struct {
	MemoryID    string `json:"memory_id"`
	Fingerprint string `json:"fingerprint"`
	Kind        string `json:"kind"`
	StartRune   int    `json:"start_rune"`
	EndRune     int    `json:"end_rune"`
}

type ClaimScope struct {
	Environment string   `json:"environment"`
	CodeVersion string   `json:"code_version"`
	Conditions  []string `json:"conditions"`
}

type Verification struct {
	Method    string        `json:"method"`
	Actor     string        `json:"actor"`
	CheckedAt float64       `json:"checked_at"`
	Evidence  []EvidenceRef `json:"evidence_refs"`
}

type Claim struct {
	Memory          *Memory       `json:"memory,omitempty"`
	Revision        int64         `json:"revision"`
	Status          string        `json:"status"`
	Evidence        []EvidenceRef `json:"evidence_refs"`
	CounterEvidence []EvidenceRef `json:"counter_evidence"`
	Scope           ClaimScope    `json:"scope"`
	Verification    *Verification `json:"verification,omitempty"`
	ValidUntil      float64       `json:"valid_until"`
}

type RecordEvidenceRequest struct {
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	Outcome   string `json:"outcome"`
	Actor     string `json:"actor"`
	Workspace string `json:"workspace"`
}

type CreateClaimRequest struct {
	Content    string        `json:"content"`
	Evidence   []EvidenceRef `json:"evidence_refs"`
	Scope      ClaimScope    `json:"scope"`
	Status     string        `json:"status"`
	ValidUntil float64       `json:"valid_until"`
	Workspace  string        `json:"workspace"`
}

type ClaimFeedback struct {
	ExpectedRevision int64         `json:"expected_revision"`
	Action           string        `json:"action"`
	Replacement      string        `json:"replacement"`
	Method           string        `json:"method"`
	Reason           string        `json:"reason"`
	Actor            string        `json:"actor"`
	Evidence         []EvidenceRef `json:"evidence_refs"`
}

type ClaimFeedbackRequest struct {
	ClaimFeedback
	Workspace string `json:"workspace"`
}

func KnowledgeFingerprint(m *Memory) string {
	if m == nil {
		return ""
	}
	h := sha256.Sum256([]byte(m.Content))
	return hex.EncodeToString(h[:])
}

func EvidenceKind(kind string) bool {
	return slices.Contains([]string{"observation", "test_result", "tool_result", "user_confirmation"}, kind)
}

func ValidateEvidenceRef(ref EvidenceRef, source *Memory) error {
	if source == nil || ref.MemoryID != source.ID || !EvidenceKind(ref.Kind) ||
		ref.Fingerprint != KnowledgeFingerprint(source) || ref.Kind != source.MetaString("verification_kind") ||
		ref.StartRune < 0 || ref.EndRune < 0 {
		return ErrInvalidClaim
	}
	if ref.StartRune == 0 && ref.EndRune == 0 {
		return nil
	}
	if ref.StartRune >= ref.EndRune || ref.EndRune > utf8.RuneCountInString(source.Content) {
		return ErrInvalidClaim
	}
	return nil
}

func ValidateClaim(content string, refs []EvidenceRef, scope ClaimScope, until float64) error {
	if strings.TrimSpace(content) == "" || !utf8.ValidString(content) || len(refs) > 64 ||
		len(scope.Conditions) > 32 || until < 0 || math.IsNaN(until) || math.IsInf(until, 0) {
		return ErrInvalidClaim
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.MemoryID == "" || seen[ref.MemoryID] {
			return ErrInvalidClaim
		}
		seen[ref.MemoryID] = true
	}
	return nil
}

func ManagedRevisionKey(m *Memory) string {
	if m == nil {
		return ""
	}
	switch m.Type {
	case TypeClaim:
		return "claim_revision"
	case TypeTopicView:
		return "topic_revision"
	case TypeMaintenanceJob:
		return "job_revision"
	case TypeTaskState:
		return "task_revision"
	}
	return ""
}

func ManagedRevision(m *Memory) int64 {
	key := ManagedRevisionKey(m)
	if key == "" {
		return 0
	}
	n, ok := m.MetaFloat(key)
	if !ok || n < 1 || n != math.Trunc(n) || n >= float64(math.MaxInt64) {
		return 0
	}
	return int64(n)
}

func ClaimFromMemory(m *Memory) (*Claim, error) {
	if m == nil {
		return nil, ErrMemoryNotFound
	}
	if m.Type != TypeClaim || ManagedRevision(m) == 0 {
		return nil, ErrInvalidClaim
	}
	raw, err := json.Marshal(m.Meta("claim_data"))
	if err != nil {
		return nil, ErrInvalidClaim
	}
	var c Claim
	if err := json.Unmarshal(raw, &c); err != nil || c.Status == "" {
		return nil, ErrInvalidClaim
	}
	c.Memory, c.Revision = m, ManagedRevision(m)
	c.Status = m.MetaString("knowledge_status")
	return &c, nil
}

func SetClaimData(c *Claim) {
	data := *c
	data.Memory = nil
	c.Memory.Metadata["claim_data"] = data
	c.Memory.Metadata["claim_revision"] = c.Revision
	c.Memory.Metadata["knowledge_status"] = c.Status
	c.Memory.Metadata["valid_until"] = c.ValidUntil
}
