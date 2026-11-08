package storage

import (
	"encoding/json"
	"math"

	"github.com/ProjAnvil/LadyM/schema"
)

// IsFresh checks persisted provenance and transitive TTL without external I/O.
// Store.GetMemory deliberately remains a raw audit read.
func IsFresh(store Store, m *schema.Memory, now float64) (bool, error) {
	if schema.ManagedRevisionKey(m) != "" {
		current, err := store.GetMemory(m.ID)
		if err != nil {
			return false, err
		}
		if current == nil || !schema.IsUsable(current) || schema.ManagedRevision(current) != schema.ManagedRevision(m) {
			return false, nil
		}
	}
	deps := func(id string) ([]string, error) {
		edges, err := store.Neighbors(id, "")
		if err != nil {
			return nil, err
		}
		return outgoingSources(id, edges), nil
	}
	return freshMemory(m, now, store.GetMemory, deps, map[string]bool{})
}

func outgoingSources(id string, edges []*schema.Edge) []string {
	var ids []string
	for _, e := range edges {
		if e.SrcID == id && e.ValidTo == nil && evidenceRelation(e.Relation) {
			ids = append(ids, e.DstID)
		}
	}
	return ids
}

func freshMemory(m *schema.Memory, now float64, get func(string) (*schema.Memory, error), deps func(string) ([]string, error), visiting map[string]bool) (bool, error) {
	if !schema.IsUsable(m) || math.IsNaN(now) || math.IsInf(now, 0) {
		return false, nil
	}
	if until, ok := m.MetaFloat("valid_until"); ok && (math.IsNaN(until) || math.IsInf(until, 0) || until < 0 || until > 0 && now >= until) {
		return false, nil
	}
	if visiting[m.ID] {
		return false, nil
	}
	visiting[m.ID] = true
	defer delete(visiting, m.ID)
	var refs []schema.EvidenceRef
	if m.Type == schema.TypeClaim {
		c, err := schema.ClaimFromMemory(m)
		if err != nil {
			return false, nil
		}
		if c.ValidUntil > 0 && now >= c.ValidUntil {
			return false, nil
		}
		refs = append(refs, c.Evidence...)
		if c.Verification != nil {
			refs = append(refs, c.Verification.Evidence...)
		}
	}
	var revisions map[string]int64
	raw, err := json.Marshal(m.Meta("source_revisions"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, &revisions); err != nil {
		return false, err
	}
	ids := legacySources(m)
	if m.Type != schema.TypeForwardIntent && m.Type != schema.TypeMaintenanceJob && m.Type != schema.TypeTaskState {
		edges, err := deps(m.ID)
		if err != nil {
			return false, err
		}
		ids = append(ids, edges...)
	}
	for _, ref := range refs {
		ids = append(ids, ref.MemoryID)
	}
	for id := range revisions {
		ids = append(ids, id)
	}
	for _, id := range ids {
		source, err := get(id)
		if err != nil {
			return false, err
		}
		if source == nil || source.Workspace != m.Workspace {
			return false, nil
		}
		for _, ref := range refs {
			if ref.MemoryID == id && schema.ValidateEvidenceRef(ref, source) != nil {
				return false, nil
			}
		}
		if revision, ok := revisions[id]; ok && revision != schema.ManagedRevision(source) {
			return false, nil
		}
		fresh, err := freshMemory(source, now, get, deps, visiting)
		if err != nil || !fresh {
			return false, err
		}
	}
	return true, nil
}

// PublicMemory removes managed stale bodies, including nested claim data.
func PublicMemory(store Store, m *schema.Memory, now float64) (*schema.Memory, error) {
	if m == nil || schema.ManagedRevisionKey(m) == "" {
		return m, nil
	}
	fresh, err := IsFresh(store, m, now)
	if err != nil || fresh {
		return m, err
	}
	safe := m.Clone()
	safe.Content, safe.Summary = "", ""
	delete(safe.Metadata, "claim_data")
	delete(safe.Metadata, "original")
	if safe.MetaString("knowledge_status") != "rejected" {
		safe.Metadata["knowledge_status"] = "stale"
	}
	return safe, nil
}
