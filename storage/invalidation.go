package storage

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/ProjAnvil/LadyM/schema"
)

func sameMeta(a, b *schema.Memory, key string) bool {
	canonical := func(value any) ([]byte, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, err
		}
		return json.Marshal(decoded)
	}
	x, xe := canonical(a.Meta(key))
	y, ye := canonical(b.Meta(key))
	return xe == nil && ye == nil && string(x) == string(y)
}

func invalidatingChange(old, next *schema.Memory) bool {
	if old.Content != next.Content || schema.IsUsable(old) && !schema.IsUsable(next) ||
		old.MetaString("knowledge_status") == "verified" && next.MetaString("knowledge_status") != "verified" {
		return true
	}
	for _, key := range []string{"verification_kind", "outcome", "scope", "environment", "code_version", "conditions", "claim_data", "source_revisions"} {
		if !sameMeta(old, next, key) {
			return true
		}
	}
	return schema.ManagedRevision(old) != schema.ManagedRevision(next)
}

func bumpManagedRevision(old, next *schema.Memory) {
	key := schema.ManagedRevisionKey(next)
	if key == "" || old == nil {
		return
	}
	if invalidatingChange(old, next) || old.Summary != next.Summary ||
		!sameMeta(old, next, "valid_until") || !sameMeta(old, next, "knowledge_status") {
		next.Metadata[key] = schema.ManagedRevision(old) + 1
	} else {
		next.Metadata[key] = schema.ManagedRevision(old)
	}
}

// Walk before any edge closures/deletes. Nonusable intermediaries still carry
// provenance; the visited set both breaks cycles and prevents double revisions.
func invalidateDependants(tx memoryTransaction, root *schema.Memory, seen map[string]bool, proposed map[string]*schema.Memory) (int, error) {
	edges, err := tx.edges("")
	if err != nil {
		return 0, err
	}
	queue := []*schema.Memory{root}
	count := 0
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		children, err := tx.dependants(parent.ID, root.Workspace)
		if err != nil {
			return 0, err
		}
		for _, child := range children {
			if seen[child.ID] || child.Type == schema.TypeForwardIntent || child.Type == schema.TypeMaintenanceJob || child.Type == schema.TypeTaskState {
				continue
			}
			seen[child.ID] = true
			queue = append(queue, child)
			if next, ok := proposed[child.ID]; ok {
				child = next
			}
			before := child.Clone()
			if child.Metadata == nil {
				child.Metadata = map[string]any{}
			}
			ids := legacySources(child)
			// Capture every outgoing source before bulk root deletions cascade
			// their edges, even when several roots share this visited child.
			for _, edge := range edges {
				if edge.SrcID != child.ID || slices.Contains(ids, edge.DstID) {
					continue
				}
				source, err := tx.get(edge.DstID)
				if err != nil {
					return 0, err
				}
				if source != nil && source.Workspace == child.Workspace {
					ids = append(ids, edge.DstID)
				}
			}
			if !slices.Contains(ids, parent.ID) {
				ids = append(ids, parent.ID)
			}
			slices.Sort(ids)
			child.Metadata["source_episodes"] = slices.Compact(ids)
			if child.MetaString("knowledge_status") != "rejected" {
				child.Metadata["knowledge_status"] = "stale"
			}
			child.Metadata["evidence_missing"] = true
			if !equalKnowledge(before, child) {
				if key := schema.ManagedRevisionKey(child); key != "" {
					child.Metadata[key] = schema.ManagedRevision(before) + 1
				}
				child.UpdatedAt = schema.Now()
				if err := tx.put(MemoryWrite{Memory: child, PreserveEmbedding: true}, true); err != nil {
					return 0, err
				}
				count++
			}
		}
	}
	return count, nil
}

func updateMemoryContent(tx memoryTransaction, id, content, summary string, tags []string, vector []float32, now float64) error {
	defer tx.rollback()
	m, err := tx.get(id)
	if err != nil || m == nil {
		return err
	}
	if schema.ManagedRevisionKey(m) != "" {
		return schema.ErrInvalidClaim
	}
	m.Content, m.Summary, m.Tags, m.UpdatedAt = content, summary, tags, now
	if vector != nil {
		m.ContentHash = schema.ContentHash(content)
	}
	return applyMemoryBatch(tx, MemoryBatch{Writes: []MemoryWrite{{Memory: m, Vector: vector, PreserveEmbedding: vector == nil}}}, true)
}

func invalidateRoot(tx memoryTransaction, id string) (int, error) {
	defer tx.rollback()
	m, err := tx.get(id)
	if err != nil || m == nil {
		return 0, err
	}
	n, err := invalidateDependants(tx, m, map[string]bool{id: true}, nil)
	if err != nil {
		return 0, err
	}
	return n, tx.commit()
}

func deleteRoots(tx memoryTransaction, memories []*schema.Memory) error {
	if len(memories) > 0 {
		if err := migrationPrepareDelete(tx.(migrationTransaction)); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, m := range memories {
		seen[m.ID] = true
	}
	for _, m := range memories {
		if _, err := invalidateDependants(tx, m, seen, nil); err != nil {
			return err
		}
	}
	for _, m := range memories {
		if err := tx.remove(m.ID); err != nil {
			return fmt.Errorf("delete source: %w", err)
		}
	}
	return nil
}
