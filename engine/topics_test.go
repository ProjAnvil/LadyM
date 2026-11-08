package engine

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func topicSource(t *testing.T, e *Engine, ws, content string) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	m.Workspace, m.Content = ws, content
	if err := e.Store.PutMemory(m, nil); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTopicCASAndProvenance(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("topics")
		var ids []string
		for i := range 64 {
			ids = append(ids, topicSource(t, e, s.Workspace(), fmt.Sprintf("symbol%d %s", i, strings.Repeat("知识", 4096))).ID)
		}
		var wg sync.WaitGroup
		out := make(chan error, 2)
		for range 2 {
			wg.Go(func() { _, err := s.BuildTopic("navigation", ids, 0); out <- err })
		}
		wg.Wait()
		close(out)
		winners, conflicts := 0, 0
		for err := range out {
			if err == nil {
				winners++
			} else if errors.Is(err, storage.ErrMemoryConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if winners != 1 || conflicts != 1 {
			t.Fatalf("CAS winners=%d conflicts=%d", winners, conflicts)
		}
		m, err := s.GetTopic("navigation")
		if err != nil || schema.ManagedRevision(m) != 1 || len(m.Content) > 16384 || !utf8.ValidString(m.Content) {
			t.Fatalf("topic: %+v %v", m, err)
		}
		for _, id := range ids {
			if !strings.Contains(m.Content, id) {
				t.Fatalf("missing ID %s", id)
			}
		}
		if !strings.Contains(m.Content, "unverified") {
			t.Fatal("legacy truth marker missing")
		}
		slices.Reverse(ids)
		updated, err := s.BuildTopic("navigation", ids, 1)
		if err != nil || schema.ManagedRevision(updated) != 2 || updated.Content != m.Content {
			t.Fatalf("deterministic update: %v", err)
		}
		if _, err := s.BuildTopic("navigation", ids, 1); !errors.Is(err, ErrMemoryConflict) {
			t.Fatalf("stale CAS: %v", err)
		}
		_, c, ref := claimFixture(t, s)
		if _, err := s.BuildTopic("unverified", []string{c.Memory.ID}, 0); err == nil {
			t.Fatal("inferred claim accepted")
		}
		c, err = s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Actor: "host", Method: "test", Evidence: []schema.EvidenceRef{ref}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.BuildTopic("verified", []string{c.Memory.ID}, 0); err != nil {
			t.Fatal(err)
		}
		for _, typ := range []schema.MemoryType{schema.TypeForwardIntent, schema.TypeTaskState, schema.TypeMaintenanceJob, schema.TypeEvidence, schema.TypeTopicView} {
			bad := topicSource(t, e, s.Workspace(), "not knowledge")
			bad.Type = typ
			if err := e.Store.PutMemory(bad, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BuildTopic("bad", []string{bad.ID}, 0); err == nil {
				t.Fatalf("accepted %s", typ)
			}
		}
	})
}

func TestTopicSourceFreshness(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		for _, change := range []string{"content", "status", "scope", "ttl", "delete"} {
			t.Run(change, func(t *testing.T) {
				s := e.Scope("fresh-" + change)
				source := topicSource(t, e, s.Workspace(), "exact source")
				m, err := s.BuildTopic("fresh", []string{source.ID}, 0)
				if err != nil {
					t.Fatal(err)
				}
				switch change {
				case "content":
					source.Content = "changed"
				case "status":
					source.Metadata["knowledge_status"] = "rejected"
				case "scope":
					source.Metadata["environment"] = "prod"
				case "ttl":
					source.Metadata["valid_until"] = schema.Now() - 1
				case "delete":
					if err := e.Store.DeleteMemory(source.ID); err != nil {
						t.Fatal(err)
					}
				}
				if change != "delete" {
					if err := e.Store.PutMemory(source, nil); err != nil {
						t.Fatal(err)
					}
				}
				got, err := s.GetTopic("fresh")
				if err != nil || got.Content != "" || schema.IsUsable(got) {
					t.Fatalf("stale exposed: %+v %v", got, err)
				}
				if fresh, err := storage.IsFresh(e.Store, m, schema.Now()); err != nil || fresh {
					t.Fatalf("fresh=%v %v", fresh, err)
				}
			})
		}
	})
}

func TestTopicLegacyIdentity(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("topic-task-identity")
		source := topicSource(t, e, s.Workspace(), "legacy topic source")
		legacy := schema.NewMemory(schema.LayerSemantic, schema.TypeTopicView)
		legacy.ID = "69e0dda5f62ddb5581ff830c612f885bc5194aaa7095c390c079b0583450af04"
		legacy.Workspace, legacy.Content = s.Workspace(), "legacy navigation"
		legacy.Metadata = map[string]any{"topic": "navigation", "topic_revision": 1, "knowledge_status": "usable"}
		if err := e.Store.PutMemory(legacy, nil); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTopic("navigation")
		if err != nil || got.ID != legacy.ID || got.Content != legacy.Content {
			t.Fatalf("legacy topic lookup: %+v %v", got, err)
		}
		updated, err := s.BuildTopic("navigation", []string{source.ID}, 1)
		if err != nil || updated.ID != legacy.ID || updated.CreatedAt != legacy.CreatedAt || schema.ManagedRevision(updated) != 2 {
			t.Fatalf("legacy topic update: %+v %v", updated, err)
		}
		prefixed, err := s.BuildTopic("task:review", []string{source.ID}, 0)
		if err != nil || prefixed.ID != "e69f709653fc06bf62511343349fd8841378afec7e7e184fd56af8b58d26e69b" {
			t.Fatalf("task-prefixed topic identity changed: %+v %v", prefixed, err)
		}
	})
}
