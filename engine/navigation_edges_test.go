package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type failingNavigationEmbedder struct {
	storage.EmbeddingProvider
	err error
}

func (p failingNavigationEmbedder) Embed(string) ([]float32, error) { return nil, p.err }

type navigationStore struct {
	storage.Store
	expanded int
	failure  error
}

func (s *navigationStore) Neighbors(id, relation string) ([]*schema.Edge, error) {
	s.expanded++
	if s.failure != nil {
		return nil, s.failure
	}
	return s.Store.Neighbors(id, relation)
}

func TestHybridGraphBoundAndFailures(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("graph")
		seed := topicSource(t, e, s.Workspace(), "ExactGraphSeed")
		for i := range 140 {
			m := topicSource(t, e, s.Workspace(), fmt.Sprintf("related%d", i))
			if err := e.Store.PutEdge(schema.NewEdge(seed.ID, "related_to", m.ID)); err != nil {
				t.Fatal(err)
			}
		}
		wrapped := &navigationStore{Store: e.Store}
		e.Store = wrapped
		result, err := s.HybridRecall("ExactGraphSeed", 200)
		if err != nil || wrapped.expanded > 128+141 {
			t.Fatalf("graph: %v expansions=%d", err, wrapped.expanded)
		}
		graphCount := 0
		for _, r := range result.Results {
			for _, channel := range r.Via {
				if channel == "graph" {
					graphCount++
				}
			}
		}
		if graphCount > 128 || graphCount < 2 {
			t.Fatalf("graph rank count=%d", graphCount)
		}
		if result.Results[0].Memory.ID != seed.ID || result.Results[0].Score != 2.0/61.0 {
			t.Fatalf("RRF60 seed: %+v", result.Results[0])
		}
		providerErr := errors.New("provider unavailable")
		e.Provider = failingNavigationEmbedder{EmbeddingProvider: e.Provider, err: providerErr}
		if _, err := s.HybridRecall("ExactGraphSeed", 1); !errors.Is(err, providerErr) {
			t.Fatalf("provider error: %v", err)
		}
		wrapped.failure = errors.New("SQL unavailable")
		if _, err := s.HybridRecall("ExactGraphSeed", 1); !errors.Is(err, wrapped.failure) {
			t.Fatalf("storage error: %v", err)
		}
	})
}

func TestHybridEvidenceOnlyFromExplicitReferences(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("explicit-evidence")
		c := verifiedBootstrapClaim(t, s, "ExactAttestedSymbol", schema.ClaimScope{})
		unrelated, err := s.RecordEvidence("observation", "ExactAttestedSymbol unrelated decoy", "", "host")
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.HybridRecall("ExactAttestedSymbol", 10)
		if err != nil {
			t.Fatal(err)
		}
		seenEvidence := false
		for _, r := range result.Results {
			if r.Memory.ID == unrelated.ID {
				t.Fatal("unrelated evidence injected")
			}
			if r.Memory.ID == c.Evidence[0].MemoryID {
				seenEvidence = true
				if len(r.Via) != 1 || r.Via[0] != "graph" {
					t.Fatal("evidence entered ordinary candidates")
				}
			}
		}
		if !seenEvidence {
			t.Fatal("explicit source evidence missing")
		}
	})
}

func TestBootstrapTopicAndTaskScope(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("entry-scopes")
		source := topicSource(t, e, s.Workspace(), "scoped code")
		source.Metadata["scope"] = schema.ClaimScope{Environment: "prod", CodeVersion: "v1", Conditions: []string{"A"}}
		if err := e.Store.PutMemory(source, nil); err != nil {
			t.Fatal(err)
		}
		topic, err := s.BuildTopic("scope", []string{source.ID}, 0)
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.SetTaskState("task", "explicit scoped goal", "next", 60)
		if err != nil {
			t.Fatal(err)
		}
		task.Metadata["scope"] = schema.ClaimScope{Environment: "prod", CodeVersion: "v1", Conditions: []string{"A"}}
		if err := e.Store.PutMemory(task, nil); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			req   BootstrapRequest
			match bool
		}{
			{BootstrapRequest{Environment: "prod", CodeVersion: "v1"}, false},
			{BootstrapRequest{Environment: "prod", CodeVersion: "v1", Conditions: []string{"a"}}, false},
			{BootstrapRequest{Environment: "dev", CodeVersion: "v1", Conditions: []string{"A"}}, false},
			{BootstrapRequest{Environment: "prod", CodeVersion: "v1", Conditions: []string{"A", "extra"}}, true},
		} {
			tc.req.TopicIDs = []string{topic.ID}
			tc.req.TaskID = "task"
			b, err := s.Bootstrap(tc.req, nil)
			if err != nil || strings.Contains(b.Content, topic.ID) != tc.match || strings.Contains(b.Content, task.ID) != tc.match {
				t.Fatalf("topic/task scope: %+v %v", b, err)
			}
		}
	})
}
func TestBootstrapTransitiveTTLAndInvalidVerification(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("transitive")
		c := verifiedBootstrapClaim(t, s, "verified navigation", schema.ClaimScope{})
		topic, err := s.BuildTopic("topic", []string{c.Memory.ID}, 0)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := e.Store.GetMemory(c.Evidence[0].MemoryID)
		if err != nil {
			t.Fatal(err)
		}
		evidence.Metadata["valid_until"] = schema.Now() - 1
		if err := e.Store.PutMemory(evidence, nil); err != nil {
			t.Fatal(err)
		}
		b, err := s.Bootstrap(BootstrapRequest{TopicIDs: []string{topic.ID}}, nil)
		if err != nil || strings.Contains(b.Content, c.Memory.ID) || strings.Contains(b.Content, topic.ID) {
			t.Fatalf("transitive expiry: %+v %v", b, err)
		}
		// A legacy malformed "verified" row without attested evidence is not
		// made true by a status string or a non-nil verification object.
		forged := schema.NewMemory(schema.LayerSemantic, schema.TypeClaim)
		forged.Workspace, forged.Content = s.Workspace(), "forged verified"
		claim := &schema.Claim{Memory: forged, Revision: 1, Status: "verified", Verification: &schema.Verification{}}
		schema.SetClaimData(claim)
		if err := e.Store.PutMemory(forged, nil); err != nil && !errors.Is(err, storage.ErrMemoryConflict) {
			t.Fatal(err)
		}
		b, err = s.Bootstrap(BootstrapRequest{}, nil)
		if err != nil || strings.Contains(b.Content, forged.ID) {
			t.Fatalf("invalid verification injected: %+v %v", b, err)
		}
		if _, err := s.BuildTopic("forged", []string{forged.ID}, 0); err == nil {
			t.Fatal("invalid verification topic")
		}
	})
}

func TestHybridOrdinaryRecallExcludesSystems(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("ordinary")
		source := topicSource(t, e, s.Workspace(), "OrdinarySymbol")
		vec, err := e.Provider.Embed(source.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.PutMemory(source, vec); err != nil {
			t.Fatal(err)
		}
		for _, typ := range []schema.MemoryType{schema.TypeMaintenanceJob, schema.TypeTaskState, schema.TypeEvidence, schema.TypeForwardIntent} {
			m := topicSource(t, e, s.Workspace(), "OrdinarySymbol")
			m.Type = typ
			if err := e.Store.PutMemory(m, vec); err != nil {
				t.Fatal(err)
			}
		}
		result, err := e.Recall("OrdinarySymbol", s.Workspace(), 20, nil, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range result.Results {
			if schema.IsSystemType(r.Memory.Type) {
				t.Fatalf("system injected %s", r.Memory.Type)
			}
		}
	})
}
