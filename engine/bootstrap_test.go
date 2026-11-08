package engine

import (
	"errors"
	"math"
	"slices"
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

func TestTaskStateTopicIdentity(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		for _, order := range []string{"topic-first", "task-first"} {
			t.Run(order, func(t *testing.T) {
				s := e.Scope(order)
				source := topicSource(t, e, s.Workspace(), "topic source")
				var topic, task *schema.Memory
				buildTopic := func() {
					t.Helper()
					var err error
					topic, err = s.BuildTopic("task:review", []string{source.ID}, 0)
					if err != nil {
						t.Fatalf("create topic: %v", err)
					}
				}
				setTask := func() {
					t.Helper()
					var err error
					task, err = s.SetTaskState("review", "review goal", "review next", 60)
					if err != nil {
						t.Fatalf("create task: %v", err)
					}
				}
				if order == "topic-first" {
					buildTopic()
					setTask()
				} else {
					setTask()
					buildTopic()
				}
				if topic.ID == task.ID {
					t.Fatal("topic and task share a storage identity")
				}
				updatedTask, err := s.SetTaskState("review", "updated goal", "updated next", 120)
				if err != nil || updatedTask.ID != task.ID || schema.ManagedRevision(updatedTask) != 2 || updatedTask.CreatedAt != task.CreatedAt {
					t.Fatalf("task update: %+v %v", updatedTask, err)
				}
				gotTopic, err := s.GetTopic("task:review")
				if err != nil || gotTopic.ID != topic.ID || gotTopic.Type != schema.TypeTopicView || gotTopic.Content != topic.Content || schema.ManagedRevision(gotTopic) != 1 {
					t.Fatalf("task update changed topic: %+v %v", gotTopic, err)
				}
				replacement := topicSource(t, e, s.Workspace(), "replacement topic source")
				updatedTopic, err := s.BuildTopic("task:review", []string{replacement.ID}, 1)
				if err != nil || updatedTopic.ID != topic.ID || schema.ManagedRevision(updatedTopic) != 2 || updatedTopic.CreatedAt != topic.CreatedAt {
					t.Fatalf("topic update: %+v %v", updatedTopic, err)
				}
				gotTask, err := e.Store.GetMemory(task.ID)
				if err != nil || gotTask == nil || gotTask.Type != schema.TypeTaskState || gotTask.Content != updatedTask.Content || gotTask.MetaString("task_id") != "review" || schema.ManagedRevision(gotTask) != 2 {
					t.Fatalf("topic update changed task: %+v %v", gotTask, err)
				}
				gotTopic, err = s.GetTopic("task:review")
				if err != nil || gotTopic.ID != updatedTopic.ID || gotTopic.Content != updatedTopic.Content {
					t.Fatalf("updated topic lookup: %+v %v", gotTopic, err)
				}
				if _, err := s.GetTopic("review"); !errors.Is(err, ErrMemoryNotFound) {
					t.Fatalf("task name resolved as a topic: %v", err)
				}
				for _, tc := range []struct {
					name string
					req  BootstrapRequest
					ids  []string
				}{
					{"both", BootstrapRequest{TopicIDs: []string{topic.ID}, TaskID: "review"}, []string{topic.ID, task.ID}},
					{"topic-only", BootstrapRequest{TopicIDs: []string{topic.ID}}, []string{topic.ID}},
					{"task-only", BootstrapRequest{TaskID: "review"}, []string{task.ID}},
					{"task-id-is-not-topic-id", BootstrapRequest{TopicIDs: []string{task.ID}}, nil},
					{"topic-name-is-not-task-name", BootstrapRequest{TaskID: "task:review"}, nil},
					{"storage-id-is-not-task-name", BootstrapRequest{TaskID: task.ID}, nil},
					{"none", BootstrapRequest{}, nil},
				} {
					t.Run(tc.name, func(t *testing.T) {
						b, err := s.Bootstrap(tc.req, nil)
						if err != nil {
							t.Fatal(err)
						}
						want := slices.Clone(tc.ids)
						slices.Sort(want)
						if !slices.Equal(b.SourceIDs, want) || strings.Contains(b.Content, "explicit task: ") != slices.Contains(want, task.ID) || strings.Contains(b.Content, "topic index: ") != slices.Contains(want, topic.ID) {
							t.Fatalf("cross-resource bootstrap: %+v; want IDs %v", b, want)
						}
						if slices.Contains(want, task.ID) && (!strings.Contains(b.Content, "updated goal") || strings.Contains(b.Content, "review goal")) {
							t.Fatalf("bootstrap used stale task content: %+v", b)
						}
					})
				}
				foreign := e.Scope("foreign-" + order)
				foreignTask, err := foreign.SetTaskState("review", "foreign goal", "", 60)
				if err != nil || foreignTask.ID == task.ID {
					t.Fatalf("foreign task identity: %+v %v", foreignTask, err)
				}
				b, err := foreign.Bootstrap(BootstrapRequest{TopicIDs: []string{topic.ID}, TaskID: "review"}, nil)
				if err != nil || !slices.Equal(b.SourceIDs, []string{foreignTask.ID}) || strings.Contains(b.Content, "updated goal") {
					t.Fatalf("cross-workspace bootstrap: %+v %v", b, err)
				}
			})
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
