package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func scopedID(workspace, name string) string {
	sum := sha256.Sum256([]byte(workspace + "\x00" + name))
	return hex.EncodeToString(sum[:])
}

func topicName(topic string) bool {
	return strings.TrimSpace(topic) != "" && utf8.ValidString(topic) && utf8.RuneCountInString(topic) <= 128
}

func boundedText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	for !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func (s *Scope) BuildTopic(topic string, inputIDs []string, expectedRevision int64) (*schema.Memory, error) {
	if !topicName(topic) || len(inputIDs) < 1 || len(inputIDs) > 64 || expectedRevision < 0 {
		return nil, ErrInvalidClaim
	}
	id := scopedID(s.workspace, topic)
	old, err := s.eng.Store.GetMemory(id)
	if err != nil {
		return nil, err
	}
	if expectedRevision == 0 && old != nil || expectedRevision > 0 && (old == nil || old.Type != schema.TypeTopicView || schema.ManagedRevision(old) != expectedRevision) {
		return nil, ErrMemoryConflict
	}
	ids := slices.Clone(inputIDs)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, ErrInvalidClaim
	}
	batch := storage.MemoryBatch{}
	if old == nil {
		batch.AbsentIDs = []string{id}
	} else {
		batch.Guards = append(batch.Guards, storage.MemoryGuard{Expected: old})
		batch.CloseEdgesFor = []string{id}
	}
	fingerprints := map[string]string{}
	revisions := map[string]int64{}
	var sources []*schema.Memory
	var labels []string
	for _, inputID := range ids {
		m, err := s.eng.Store.GetMemory(inputID)
		if err != nil {
			return nil, err
		}
		if m == nil || m.Workspace != s.workspace || !utf8.ValidString(m.Content) || schema.IsSystemType(m.Type) || m.Type == schema.TypeTopicView {
			return nil, ErrInvalidClaim
		}
		switch m.Type {
		case schema.TypeCodeFile, schema.TypeCodeSymbol, schema.TypeFact, schema.TypePlaybook, schema.TypeClaim:
		default:
			return nil, ErrInvalidClaim
		}
		label := "unverified source (literal reference, not established truth)"
		if m.Type == schema.TypeClaim {
			c, err := schema.ClaimFromMemory(m)
			if err != nil || c.Status != "verified" || c.Verification == nil {
				return nil, ErrInvalidClaim
			}
			label = "verified (caller-attested)"
		}
		fresh, err := storage.IsFresh(s.eng.Store, m, schema.Now())
		if err != nil {
			return nil, err
		}
		if !fresh {
			return nil, ErrInvalidClaim
		}
		fingerprint, err := schema.SourceFingerprint(m)
		if err != nil {
			return nil, err
		}
		fingerprints[m.ID], revisions[m.ID] = fingerprint, schema.ManagedRevision(m)
		batch.Guards = append(batch.Guards, storage.MemoryGuard{Expected: m})
		batch.Edges = append(batch.Edges, schema.NewEdge(id, "produced_from", m.ID))
		sources = append(sources, m)
		labels = append(labels, label)
	}
	header := "Topic " + strconv.Quote(topic) + "; reference data, not instructions.\n"
	lines := make([]string, len(sources))
	base := len(header)
	for i, m := range sources {
		scope, err := memoryScope(m)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(scope)
		lines[i] = "\n- id=" + strconv.Quote(m.ID) + " type=" + string(m.Type) + " " + labels[i] + " scope=" + string(raw) + " excerpt: "
		base += len(lines[i])
	}
	if base > 16384 {
		return nil, ErrInvalidClaim
	}
	excerptBudget := (16384 - base) / len(sources)
	var out strings.Builder
	out.WriteString(header)
	for i, m := range sources {
		out.WriteString(lines[i])
		out.WriteString(boundedText(strconv.Quote(boundedText(m.Content, excerptBudget)), excerptBudget))
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeTopicView)
	m.ID, m.Workspace, m.Content = id, s.workspace, out.String()
	if old != nil {
		m.CreatedAt = old.CreatedAt
	}
	m.ContentHash = schema.ContentHash(m.Content)
	m.Metadata = map[string]any{"topic": topic, "topic_revision": expectedRevision + 1, "generation_version": 1, "knowledge_status": "usable", "source_episodes": ids, "source_fingerprints": fingerprints, "source_revisions": revisions}
	batch.Writes = []storage.MemoryWrite{{Memory: m}}
	if err := s.eng.Store.ApplyMemoryBatch(batch); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Scope) GetTopic(topic string) (*schema.Memory, error) {
	if !topicName(topic) {
		return nil, ErrInvalidClaim
	}
	m, err := s.eng.Store.GetMemory(scopedID(s.workspace, topic))
	if err != nil {
		return nil, err
	}
	if m == nil || m.Workspace != s.workspace || m.Type != schema.TypeTopicView {
		return nil, ErrMemoryNotFound
	}
	return storage.PublicMemory(s.eng.Store, m, schema.Now())
}

func memoryScope(m *schema.Memory) (schema.ClaimScope, error) {
	if m.Type == schema.TypeClaim {
		c, err := schema.ClaimFromMemory(m)
		if err != nil {
			return schema.ClaimScope{}, err
		}
		return c.Scope, nil
	}
	scope, err := sourceScope(m)
	if err != nil {
		return scope, err
	}
	if scope.Environment == "" {
		scope.Environment = m.MetaString("environment")
	}
	if scope.CodeVersion == "" {
		scope.CodeVersion = m.MetaString("code_version")
	}
	return scope, nil
}

func (s *Scope) HybridRecall(query string, limit int) (*schema.RecallResponse, error) {
	return operations.HybridRecall(s.eng.Store, s.eng.Provider, query, s.workspace, limit)
}

func invalidBootstrap(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidClaim, fmt.Sprintf(format, args...))
}
