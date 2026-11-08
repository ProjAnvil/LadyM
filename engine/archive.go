package engine

import (
	"fmt"
	"math"
	"regexp"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const archiveMarker = "ladym.archive.v1"

var credentialField = regexp.MustCompile(`(?i)(["']?(?:authorization|password|passwd|api[_-]?key|access[_-]?token|refresh[_-]?token|token|secret)["']?\s*[:=]\s*)(?:"(?:\\.|[^"\\\r\n])*"|'(?:\\.|[^'\\\r\n])*'|(?:Bearer|Basic)\s+[^\s,;}\r\n]+|[^\s,;}\r\n]+)`)

// RedactEvidence is best-effort field redaction, not a PII or secret detector.
func RedactEvidence(content string) string {
	return credentialField.ReplaceAllString(content, `${1}"[REDACTED]"`)
}

func (s *Scope) ArchiveEvidence(kind, content, outcome, actor string) (*schema.Memory, error) {
	cfg := s.eng.Config.Archive
	if !cfg.Enabled {
		return nil, fmt.Errorf("%w: evidence archive disabled; set archive.enabled=true", ErrInvalidClaim)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	// Validate raw input before redaction, so secrets cannot evade the byte cap.
	m, err := s.newEvidence(kind, content, outcome, actor)
	if err != nil {
		return nil, err
	}
	if len(content) > cfg.MaxEntryBytes {
		return nil, fmt.Errorf("%w: archive entry byte limit", ErrInvalidClaim)
	}
	m.Content = RedactEvidence(content)
	if len(m.Content) > cfg.MaxEntryBytes {
		return nil, fmt.Errorf("%w: redacted archive entry byte limit", ErrInvalidClaim)
	}
	m.ContentHash = schema.ContentHash(m.Content)
	m.Metadata["outcome"] = RedactEvidence(outcome)
	m.Metadata["archive_marker"] = archiveMarker
	until := schema.Now() + cfg.RetentionS
	if math.IsInf(until, 0) {
		return nil, ErrInvalidClaim
	}
	m.Metadata["valid_until"] = until
	batch := storage.MemoryBatch{
		AbsentIDs: []string{m.ID}, Writes: []storage.MemoryWrite{{Memory: m}},
		Quota: &storage.MemoryQuota{Workspace: s.workspace, MaxBytes: cfg.MaxWorkspaceBytes, Marker: archiveMarker},
	}
	if err := s.eng.Store.ApplyMemoryBatch(batch); err != nil {
		return nil, err
	}
	return m, nil
}

func (e *Engine) ArchiveEvidence(kind, content, outcome, actor, workspace string) (*schema.Memory, error) {
	return e.Scope(workspace).ArchiveEvidence(kind, content, outcome, actor)
}
