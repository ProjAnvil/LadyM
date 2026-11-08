package storage

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
)

// IsFresh checks persisted provenance and transitive TTL without external I/O.
// Source reads and node evaluations are memoized only within this call.
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
	eval := freshnessEvaluation{
		now: now, get: get, deps: deps, visiting: visiting,
		memories: map[string]*schema.Memory{}, evaluated: map[string]bool{},
	}
	if m != nil {
		eval.memories[m.ID] = m
	}
	return eval.fresh(m)
}

type freshnessEvaluation struct {
	now       float64
	get       func(string) (*schema.Memory, error)
	deps      func(string) ([]string, error)
	visiting  map[string]bool
	memories  map[string]*schema.Memory
	evaluated map[string]bool
}

func (eval *freshnessEvaluation) memory(id string) (*schema.Memory, error) {
	if m, ok := eval.memories[id]; ok {
		return m, nil
	}
	m, err := eval.get(id)
	if err == nil {
		eval.memories[id] = m
	}
	return m, err
}

func (eval *freshnessEvaluation) fresh(m *schema.Memory) (fresh bool, err error) {
	now := eval.now
	if !schema.IsUsable(m) || math.IsNaN(now) || math.IsInf(now, 0) {
		return false, nil
	}
	if until, ok := m.MetaFloat("valid_until"); ok && (math.IsNaN(until) || math.IsInf(until, 0) || until < 0 || until > 0 && now >= until) {
		return false, nil
	}
	if eval.visiting[m.ID] {
		return false, nil
	}
	if fresh, ok := eval.evaluated[m.ID]; ok {
		return fresh, nil
	}
	eval.visiting[m.ID] = true
	defer func() {
		delete(eval.visiting, m.ID)
		if err == nil {
			eval.evaluated[m.ID] = fresh
		}
	}()
	var refs []schema.EvidenceRef
	var verificationRefs []schema.EvidenceRef
	if m.Type == schema.TypeClaim {
		c, err := schema.ClaimFromMemory(m)
		if err != nil {
			return false, nil
		}
		if c.ValidUntil > 0 && now >= c.ValidUntil {
			return false, nil
		}
		if c.Status == "verified" {
			v := c.Verification
			if v == nil || strings.TrimSpace(v.Actor) == "" || strings.TrimSpace(v.Method) == "" || v.CheckedAt <= 0 ||
				math.IsNaN(v.CheckedAt) || math.IsInf(v.CheckedAt, 0) || len(v.Evidence) == 0 || len(v.Evidence) > 64 {
				return false, nil
			}
			verificationRefs = v.Evidence
		}
		refs = append(refs, c.Evidence...)
		if c.Verification != nil {
			refs = append(refs, c.Verification.Evidence...)
		}
	}
	var revisions map[string]int64
	var fingerprints map[string]string
	rawFingerprints, err := json.Marshal(m.Meta("source_fingerprints"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(rawFingerprints, &fingerprints); err != nil {
		return false, err
	}
	raw, err := json.Marshal(m.Meta("source_revisions"))
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(raw, &revisions); err != nil {
		return false, err
	}
	ids := legacySources(m)
	if m.Type != schema.TypeForwardIntent && m.Type != schema.TypeMaintenanceJob && m.Type != schema.TypeTaskState {
		edges, err := eval.deps(m.ID)
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
	for id := range fingerprints {
		ids = append(ids, id)
	}
	seen := map[string]bool{}
	attested := false
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		source, err := eval.memory(id)
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
			for _, ref := range verificationRefs {
				if ref.MemoryID == id && source.Type == schema.TypeEvidence &&
					(ref.Kind == "user_confirmation" || (ref.Kind == "test_result" || ref.Kind == "tool_result") && source.MetaString("outcome") == "success") {
					attested = true
				}
			}
		}
		if revision, ok := revisions[id]; ok && revision != schema.ManagedRevision(source) {
			return false, nil
		}
		if expected, ok := fingerprints[id]; ok {
			actual, err := schema.SourceFingerprint(source)
			if err != nil || actual != expected {
				return false, err
			}
		}
		fresh, err := eval.fresh(source)
		if err != nil || !fresh {
			return false, err
		}
	}
	if len(verificationRefs) > 0 && !attested {
		return false, nil
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
