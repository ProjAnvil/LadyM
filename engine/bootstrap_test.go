package engine

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/schema"
)

type testCounter struct{ count func(string) (int, error) }

func (c testCounter) Name() string                { return "host-exact" }
func (c testCounter) Count(s string) (int, error) { return c.count(s) }

func verifiedBootstrapClaim(t *testing.T, s *Scope, content string, scope schema.ClaimScope) *schema.Claim {
	t.Helper()
	m, err := s.RecordEvidence("user_confirmation", "confirmed", "", "host")
	if err != nil {
		t.Fatal(err)
	}
	ref := schema.EvidenceRef{MemoryID: m.ID, Fingerprint: schema.KnowledgeFingerprint(m), Kind: "user_confirmation"}
	c, err := s.CreateClaim(content, []schema.EvidenceRef{ref}, scope, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.ApplyClaimFeedback(c.Memory.ID, ClaimFeedback{ExpectedRevision: 1, Action: "verify", Actor: "host", Method: "attested", Evidence: []schema.EvidenceRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBootstrapCounterBudget(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("bootstrap")
		c := verifiedBootstrapClaim(t, s, strings.Repeat("知识", 4096), schema.ClaimScope{Conditions: []string{"TestsPass"}})
		verifiedBootstrapClaim(t, e.Scope("foreign"), "FOREIGN", schema.ClaimScope{})
		_, inferred, _ := claimFixture(t, s)
		req := BootstrapRequest{Conditions: []string{"TestsPass"}}
		b, err := s.Bootstrap(req, nil)
		if err != nil || b.Unit != "bytes" || b.Used != len(b.Content) || b.Used > 4096 || !utf8.ValidString(b.Content) || !strings.Contains(b.Content, c.Memory.ID) {
			t.Fatalf("bytes: %+v %v", b, err)
		}
		if strings.Contains(b.Content, inferred.Memory.ID) || strings.Contains(b.Content, "FOREIGN") {
			t.Fatal("inferred/foreign injected")
		}
		counter := testCounter{func(text string) (int, error) { return len(text)*2 + len(text)*len(text)/257, nil }}
		req.Unit = "tokens"
		b, err = s.Bootstrap(req, counter)
		if err != nil {
			t.Fatal(err)
		}
		used, _ := counter.Count(b.Content)
		if used != b.Used || used > 512 || b.Model != counter.Name() || !utf8.ValidString(b.Content) {
			t.Fatalf("full count: %+v", b)
		}
		req.Limit = 1024
		if b, err = s.Bootstrap(req, counter); err != nil || b.Used > 1024 {
			t.Fatalf("max: %v", err)
		}
		req.Limit = 1025
		if _, err := s.Bootstrap(req, counter); err == nil {
			t.Fatal("oversized tokens accepted")
		}
		req.Limit = 0
		for _, broken := range []TokenCounter{nil, testCounter{func(string) (int, error) { return -1, nil }}, testCounter{func(string) (int, error) { return 0, errors.New("broken") }}, testCounter{func(string) (int, error) { panic("malicious") }}, testCounter{func(string) (int, error) { return 0, nil }}} {
			if _, err := s.Bootstrap(req, broken); err == nil {
				t.Fatal("broken counter accepted")
			}
		}
	})
}

func TestBootstrapConditions(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("conditions")
		wild := verifiedBootstrapClaim(t, s, "wildcard", schema.ClaimScope{Conditions: []string{"A", "B"}})
		exact := verifiedBootstrapClaim(t, s, "exact", schema.ClaimScope{Environment: "prod", CodeVersion: "v1"})
		for _, tc := range []struct {
			req         BootstrapRequest
			wild, exact bool
		}{
			{BootstrapRequest{}, false, false},
			{BootstrapRequest{Conditions: []string{"A", "b"}}, false, false},
			{BootstrapRequest{Environment: "dev", CodeVersion: "v1", Conditions: []string{"A", "B", "extra"}}, true, false},
			{BootstrapRequest{Environment: "prod", CodeVersion: "v1", Conditions: []string{"A", "B"}}, true, true},
		} {
			b, err := s.Bootstrap(tc.req, nil)
			if err != nil || strings.Contains(b.Content, wild.Memory.ID) != tc.wild || strings.Contains(b.Content, exact.Memory.ID) != tc.exact {
				t.Fatalf("scope: %+v %v", b, err)
			}
		}
	})
}

func TestTaskStateTTLAndBootstrap(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("task")
		m, err := s.SetTaskState("explicit", "user goal", "next step", 60)
		if err != nil || m.Type != schema.TypeTaskState {
			t.Fatalf("task: %v", err)
		}
		b, err := s.Bootstrap(BootstrapRequest{TaskID: "explicit"}, nil)
		if err != nil || !strings.Contains(b.Content, "user goal") || !strings.Contains(b.Content, m.ID) {
			t.Fatalf("task injection: %+v %v", b, err)
		}
		m.Metadata["valid_until"] = schema.Now() - 1
		if err := e.Store.PutMemory(m, nil); err != nil {
			t.Fatal(err)
		}
		b, err = s.Bootstrap(BootstrapRequest{TaskID: "explicit"}, nil)
		if err != nil || strings.Contains(b.Content, "user goal") {
			t.Fatalf("expired task: %+v %v", b, err)
		}
		for _, ttl := range []float64{-1, math.NaN(), math.Inf(1)} {
			if _, err := s.SetTaskState("bad", "goal", "next", ttl); err == nil {
				t.Fatal("invalid TTL")
			}
		}
		if _, err := s.SetTaskState("bad", string([]byte{255}), "next", 60); err == nil {
			t.Fatal("invalid UTF8")
		}
	})
}

func TestHybridSourceRanking(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		s := e.Scope("hybrid")
		exact := topicSource(t, e, s.Workspace(), "ExactSymbol src/module.go")
		for range 200 {
			m := topicSource(t, e, s.Workspace(), "ExactSymbol system")
			m.Type = schema.TypeForwardIntent
			vec, err := e.Provider.Embed("ExactSymbol")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Store.PutMemory(m, vec); err != nil {
				t.Fatal(err)
			}
		}
		topicSource(t, e, "foreign", "ExactSymbol")
		a, err := s.HybridRecall("ExactSymbol", 1)
		if err != nil || len(a.Results) != 1 || a.Results[0].Memory.ID != exact.ID {
			t.Fatalf("hybrid: %+v %v", a, err)
		}
		b, err := s.HybridRecall("ExactSymbol", 1)
		if err != nil || b.Results[0].Memory.ID != exact.ID {
			t.Fatal("unstable ranking")
		}
		for _, query := range []string{"", " "} {
			if _, err := s.HybridRecall(query, 1); err == nil {
				t.Fatal("empty query")
			}
		}
		for _, limit := range []int{0, 201} {
			if _, err := s.HybridRecall("query", limit); err == nil {
				t.Fatal("invalid limit")
			}
		}
	})
}
