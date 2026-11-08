package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
)

type MemoryFilter struct {
	Workspace       string
	Layers          []schema.Layer
	Types           []schema.MemoryType
	ExcludeRetired  bool
	ExcludeUnusable bool
}

func (f MemoryFilter) matches(m *schema.Memory) bool {
	return (f.Workspace == "" || m.Workspace == f.Workspace) &&
		(len(f.Layers) == 0 || slices.Contains(f.Layers, m.Layer)) &&
		(len(f.Types) == 0 || slices.Contains(f.Types, m.Type)) &&
		(!f.ExcludeRetired || !schema.IsRetired(m)) &&
		(!f.ExcludeUnusable || schema.IsUsable(m))
}

type MemoryGuard struct {
	Expected *schema.Memory
	// PendingEpisode additionally requires an unstamped, usable L1 event.
	PendingEpisode bool
}

type MemoryWrite struct {
	Memory *schema.Memory
	Vector []float32
	// PreserveEmbedding keeps the existing blob even when Vector is nil.
	// Without it, nil follows PutMemory's embedding-NULL semantics.
	PreserveEmbedding bool
}

type MemoryBatch struct {
	Guards        []MemoryGuard
	Writes        []MemoryWrite
	Edges         []*schema.Edge
	CloseEdgesFor []string
}

var ErrMemoryConflict = errors.New("memory snapshot conflict")

func memoryUpsertSQL(postgres, preserveEmbedding, preserveAccess bool) string {
	params := make([]string, 16)
	for i := range params {
		params[i] = "?"
		if postgres {
			params[i] = "$" + strconv.Itoa(i+1)
		}
	}
	updates := `layer=excluded.layer, type=excluded.type, content=excluded.content,
		summary=excluded.summary, tags=excluded.tags, metadata=excluded.metadata,
		source=excluded.source, workspace=excluded.workspace, updated_at=excluded.updated_at,
		content_hash=excluded.content_hash`
	if !preserveAccess {
		updates += ", last_access_at=excluded.last_access_at, access_count=excluded.access_count, activation=excluded.activation"
	}
	if !preserveEmbedding {
		updates += ", embedding=excluded.embedding"
	}
	return `INSERT INTO memories
		(id, layer, type, content, summary, tags, metadata, source, workspace,
		created_at, updated_at, last_access_at, access_count, activation, content_hash, embedding)
		VALUES (` + strings.Join(params, ",") + `) ON CONFLICT(id) DO UPDATE SET ` + updates
}

func memoryArgs(w MemoryWrite, embedding any) []any {
	m := w.Memory
	tags, _ := json.Marshal(m.Tags)
	meta, _ := json.Marshal(m.Metadata)
	return []any{m.ID, string(m.Layer), string(m.Type), m.Content, m.Summary, string(tags), string(meta),
		m.Source, m.Workspace, m.CreatedAt, m.UpdatedAt, m.LastAccessAt, m.AccessCount, m.Activation, m.ContentHash, embedding}
}

func validateVector(vector []float32, dim int) error {
	if len(vector) != dim {
		return fmt.Errorf("vector dim %d != store dim %d", len(vector), dim)
	}
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errors.New("vector contains non-finite element")
		}
	}
	return nil
}

func validateDeleteCutoff(autoBefore float64) error {
	if autoBefore < 0 || math.IsNaN(autoBefore) || math.IsInf(autoBefore, 0) {
		return errors.New("autoBefore must be finite and non-negative")
	}
	return nil
}

func validateBatch(batch MemoryBatch, dim int) error {
	for _, g := range batch.Guards {
		if g.Expected == nil || g.Expected.ID == "" {
			return errors.New("memory guard requires an expected memory ID")
		}
	}
	ids := map[string]bool{}
	for _, w := range batch.Writes {
		if w.Memory == nil || w.Memory.ID == "" {
			return errors.New("memory write requires a memory ID")
		}
		if ids[w.Memory.ID] {
			return fmt.Errorf("duplicate memory write %q", w.Memory.ID)
		}
		ids[w.Memory.ID] = true
		if w.Vector != nil {
			if err := validateVector(w.Vector, dim); err != nil {
				return err
			}
		}
		if _, err := json.Marshal(w.Memory.Metadata); err != nil {
			return fmt.Errorf("memory metadata: %w", err)
		}
	}
	for _, e := range batch.Edges {
		if e == nil || e.ID == "" || e.SrcID == "" || e.DstID == "" {
			return errors.New("edge requires ID and endpoints")
		}
		if _, err := json.Marshal(e.Metadata); err != nil {
			return fmt.Errorf("edge metadata: %w", err)
		}
		if math.IsNaN(e.Weight) || math.IsInf(e.Weight, 0) {
			return errors.New("edge weight must be finite")
		}
	}
	return nil
}

// JSON comparison gives typed metadata the same representation as stored JSON.
func equalKnowledge(a, b *schema.Memory) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, y := a.Clone(), b.Clone()
	for _, m := range []*schema.Memory{x, y} {
		m.CreatedAt, m.LastAccessAt, m.AccessCount, m.Activation = 0, 0, 0, 0
		m.Embedding = nil
		if m.Metadata == nil {
			m.Metadata = map[string]any{}
		}
		raw, err := json.Marshal(m.Metadata)
		if err != nil {
			return false
		}
		if err := json.Unmarshal(raw, &m.Metadata); err != nil {
			return false
		}
	}
	ax, ae := json.Marshal(x)
	bx, be := json.Marshal(y)
	return ae == nil && be == nil && bytes.Equal(ax, bx)
}

func legacySources(m *schema.Memory) []string {
	if m == nil || m.Type == schema.TypeForwardIntent {
		return nil
	}
	var ids []string
	if id := m.MetaString("source_episode"); id != "" {
		ids = append(ids, id)
	}
	switch values := m.Meta("source_episodes").(type) {
	case []string:
		ids = append(ids, values...)
	case []any:
		for _, value := range values {
			if id, ok := value.(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func evidenceRelation(relation string) bool {
	return relation == "produced_from" || relation == "abstracts"
}

func directlyDepends(m *schema.Memory, source string, edges []*schema.Edge) bool {
	if !schema.IsUsable(m) || m.Type == schema.TypeForwardIntent {
		return false
	}
	if slices.Contains(legacySources(m), source) {
		return true
	}
	return slices.ContainsFunc(edges, func(e *schema.Edge) bool {
		return e.ValidTo == nil && evidenceRelation(e.Relation) && e.SrcID == m.ID && e.DstID == source
	})
}

// memoryTransaction is private storage plumbing, not a public transaction API.
// Both implementations acquire a writer lock before any eligibility reads.
type memoryTransaction interface {
	get(string) (*schema.Memory, error)
	memories(string) ([]*schema.Memory, error)
	dependants(string, string) ([]*schema.Memory, error)
	edges(string) ([]*schema.Edge, error)
	put(MemoryWrite, bool) error
	putEdge(*schema.Edge) error
	closeEdges([]string, float64) error
	remove(string) error
	commit() error
	rollback()
}

func episodeCompleted(m *schema.Memory) bool {
	if m == nil {
		return false
	}
	value, ok := m.MetaFloat("consolidated_at")
	return ok && value > 0
}

func applyMemoryBatch(tx memoryTransaction, batch MemoryBatch, preserveAccess bool) error {
	defer tx.rollback()
	for _, guard := range batch.Guards {
		current, err := tx.get(guard.Expected.ID)
		if err != nil {
			return err
		}
		if !equalKnowledge(current, guard.Expected) ||
			guard.PendingEpisode && (current == nil || current.Layer != schema.LayerEpisodic ||
				current.Type != schema.TypeEvent || !schema.IsUsable(current) || episodeCompleted(current)) {
			return fmt.Errorf("%w: guard %s", ErrMemoryConflict, guard.Expected.ID)
		}
	}
	// Resolve against the proposed state as well as the locked persisted state,
	// allowing atomic creation of both endpoints without accepting dangling refs.
	proposed := map[string]*schema.Memory{}
	for _, w := range batch.Writes {
		proposed[w.Memory.ID] = w.Memory
	}
	resolve := func(id string) (*schema.Memory, error) {
		if m, ok := proposed[id]; ok {
			return m, nil
		}
		return tx.get(id)
	}
	for _, w := range batch.Writes {
		if !schema.IsUsable(w.Memory) {
			continue
		}
		for _, id := range legacySources(w.Memory) {
			source, err := resolve(id)
			if err != nil {
				return err
			}
			if source == nil || source.Workspace != w.Memory.Workspace ||
				source.MetaString("knowledge_status") == "stale" || source.MetaString("knowledge_status") == "rejected" || source.Meta("evidence_missing") == true {
				return fmt.Errorf("%w: unavailable evidence %s", ErrMemoryConflict, id)
			}
		}
	}
	for _, e := range batch.Edges {
		src, err := resolve(e.SrcID)
		if err != nil {
			return err
		}
		dst, err := resolve(e.DstID)
		if err != nil {
			return err
		}
		if src == nil || dst == nil {
			return fmt.Errorf("%w: missing edge endpoint", ErrMemoryConflict)
		}
		if e.ValidTo == nil && evidenceRelation(e.Relation) &&
			(src.Workspace != dst.Workspace || schema.IsUsable(src) &&
				(dst.MetaString("knowledge_status") == "stale" || dst.MetaString("knowledge_status") == "rejected" || dst.Meta("evidence_missing") == true)) {
			return fmt.Errorf("%w: unavailable edge evidence", ErrMemoryConflict)
		}
	}
	for _, w := range batch.Writes {
		if err := tx.put(w, preserveAccess); err != nil {
			return err
		}
	}
	if err := tx.closeEdges(batch.CloseEdgesFor, schema.Now()); err != nil {
		return err
	}
	for _, e := range batch.Edges {
		if err := tx.putEdge(e); err != nil {
			return err
		}
	}
	return tx.commit()
}

func evidenceReferences(memories []*schema.Memory, edges []*schema.Edge) map[string]bool {
	refs := map[string]bool{}
	byID := map[string]*schema.Memory{}
	for _, m := range memories {
		byID[m.ID] = m
	}
	for _, m := range memories {
		if schema.IsUsable(m) {
			for _, id := range legacySources(m) {
				if source := byID[id]; source != nil && id != m.ID && source.Workspace == m.Workspace {
					refs[id] = true
				}
			}
		}
	}
	for _, e := range edges {
		source, dependant := byID[e.DstID], byID[e.SrcID]
		if source != nil && dependant != nil && source.ID != dependant.ID && source.Workspace == dependant.Workspace &&
			directlyDepends(dependant, e.DstID, []*schema.Edge{e}) {
			refs[e.DstID] = true
		}
	}
	return refs
}

func deleteMemorySafely(tx memoryTransaction, id string, autoBefore float64) (bool, bool, error) {
	defer tx.rollback()
	m, err := tx.get(id)
	if err != nil || m == nil {
		return false, false, err
	}
	dependants, err := tx.dependants(id, m.Workspace)
	if err != nil {
		return false, false, err
	}
	edges, err := tx.edges(id)
	if err != nil {
		return false, false, err
	}
	var direct []*schema.Memory
	for _, dependant := range dependants {
		if dependant.ID != id && directlyDepends(dependant, id, edges) {
			direct = append(direct, dependant)
		}
	}
	if autoBefore > 0 {
		if len(direct) > 0 {
			return false, true, nil
		}
		if m.LastAccessAt > autoBefore {
			return false, false, nil
		}
	} else {
		for _, dependant := range direct {
			dependant.Metadata["knowledge_status"] = "stale"
			dependant.Metadata["evidence_missing"] = true
			dependant.UpdatedAt = schema.Now()
			if err := tx.put(MemoryWrite{Memory: dependant, PreserveEmbedding: true}, true); err != nil {
				return false, false, err
			}
		}
	}
	if err := tx.remove(id); err != nil {
		return false, false, err
	}
	if err := tx.commit(); err != nil {
		return false, false, err
	}
	return true, false, nil
}
