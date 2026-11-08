package storage

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestArchiveQuotaBatchAtomicity(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		marked := func(text string) *schema.Memory {
			m := contractMemory(schema.TypeEvidence)
			m.Content = text
			m.Metadata["archive_marker"] = "test.archive"
			return m
		}
		quota := &MemoryQuota{Workspace: "contract", MaxBytes: 8, Marker: "test.archive"}
		m := marked("1234")
		m.Metadata["valid_until"], m.Metadata["pinned"] = 1, true
		suitePutMemory(t, s, m, nil)
		other := marked("outside quota")
		other.Workspace = "other"
		suitePutMemory(t, s, other, nil)
		a, b := marked("12"), marked("345")
		batch := MemoryBatch{Quota: quota, Writes: []MemoryWrite{{Memory: a}, {Memory: b}}, Edges: []*schema.Edge{schema.NewEdge(a.ID, "produced_from", b.ID)}}
		if err := s.ApplyMemoryBatch(batch); !errors.Is(err, ErrMemoryQuota) {
			t.Fatalf("batch cap=%v", err)
		}
		for _, id := range []string{a.ID, b.ID} {
			if got, err := s.GetMemory(id); err != nil || got != nil {
				t.Fatalf("partial batch %s: %+v %v", id, got, err)
			}
		}
		if count, err := s.CountEdges(); err != nil || count != 0 {
			t.Fatalf("partial edge=%d %v", count, err)
		}
		m.Content = "1"
		batch.Writes = append(batch.Writes, MemoryWrite{Memory: m})
		if err := s.ApplyMemoryBatch(batch); err != nil {
			t.Fatal(err)
		}
		b.Content = "1234567"
		if err := s.ApplyMemoryBatch(MemoryBatch{Quota: quota, Writes: []MemoryWrite{{Memory: b}}}); !errors.Is(err, ErrMemoryQuota) {
			t.Fatalf("update cap=%v", err)
		}
		got, err := s.GetMemory(b.ID)
		if err != nil || got.Content != "345" {
			t.Fatalf("failed update persisted=%+v %v", got, err)
		}
		for _, invalid := range []*MemoryQuota{{MaxBytes: 8, Marker: "m"}, {Workspace: "w", MaxBytes: 8}, {Workspace: "w", Marker: "m", MaxBytes: 0}} {
			if err := s.ApplyMemoryBatch(MemoryBatch{Quota: invalid}); err == nil {
				t.Fatal("invalid quota accepted")
			}
		}
	})
}
