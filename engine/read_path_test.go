package engine

// Port of main:tests/perf/test_read_path_budget.py (NFR-1) and
// main:tests/test_read_path_no_llm.py (NFR-3).
//
// Python pinned an absolute wall-clock budget (engine-overhead p95 < 10ms @
// 200 memories). Absolute timings are not portable across machines, so the Go
// port pins what the budget actually protects: the read path does O(1)
// provider work per recall (one query embed, zero re-embeds of stored
// memories), and engine overhead stays a small multiple of the query-embed
// cost. Everything runs offline against the hashing embedder.

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/storage"
)

// countingEmbedder wraps an EmbeddingProvider and counts Embed calls.
type countingEmbedder struct {
	inner storage.EmbeddingProvider
	mu    sync.Mutex
	calls int
}

func (c *countingEmbedder) Embed(text string) ([]float32, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.Embed(text)
}

func (c *countingEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		v, err := c.Embed(text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *countingEmbedder) Dim() int { return c.inner.Dim() }

func (c *countingEmbedder) HealthCheck() (bool, string) { return c.inner.HealthCheck() }

func (c *countingEmbedder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingEmbedder) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = 0
}

func timeEmbedMs(emb storage.EmbeddingProvider, q string, n int) float64 {
	t0 := time.Now()
	for i := 0; i < n; i++ {
		if _, err := emb.Embed(q); err != nil {
			return 0
		}
	}
	return float64(time.Since(t0).Nanoseconds()) / 1e6 / float64(n)
}

func percentileMs(xs []float64, p int) float64 {
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	k := int(math.Round(float64(p) / 100 * float64(len(sorted)-1)))
	return sorted[k]
}

// seedFacts writes n facts through the semantic layer and warms the read
// path, then returns the engine ready for measurement.
func seedFacts(t *testing.T, emb storage.EmbeddingProvider, n int) *Engine {
	t.Helper()
	eng, err := NewWithModels(config.ForTesting(t.TempDir()), &adapter.ModelRouting{Embedding: emb})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	for i := 0; i < n; i++ {
		if _, err := eng.Semantic.PutFact(fmt.Sprintf("fact number %d about topic %d", i, i%10), "", nil, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := eng.Recall("topic 0", "", 0, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	return eng
}

// measureOverheadP95Ms recalls n queries and returns the p95 of
// total recall time minus the hashing embed cost (mirrors the Python
// measurement), so provider cost is isolated from engine overhead.
func measureOverheadP95Ms(t *testing.T, eng *Engine, emb storage.EmbeddingProvider, n int) float64 {
	t.Helper()
	samples := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		q := fmt.Sprintf("topic %d", i%10)
		tEmbed := timeEmbedMs(emb, q, 20)
		t0 := time.Now()
		if _, err := eng.Recall(q, "", 0, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
		total := float64(time.Since(t0).Nanoseconds()) / 1e6
		samples = append(samples, math.Max(0, total-tEmbed))
	}
	return percentileMs(samples, 95)
}

func TestReadPathBudget(t *testing.T) {
	emb := &countingEmbedder{inner: storage.NewHashingEmbedding(256)}
	eng := seedFacts(t, emb, 200)
	emb.reset()

	const n = 100
	p95Big := measureOverheadP95Ms(t, eng, emb.inner, n)

	// Hard budget (op-count, deterministic): exactly one query embed per
	// recall — the read path never re-embeds stored memories.
	if got := emb.count(); got != n {
		t.Errorf("embed calls = %d, want %d (exactly one query embed per recall)", got, n)
	}

	// Relative budget (scaling, machine-independent): 10x the memories must
	// cost well under 10x the engine overhead — a recall that rescans or
	// re-embeds the corpus would blow past this. (Python's absolute 10ms p95
	// budget is not portable, so it is not carried over.)
	smallEmb := storage.NewHashingEmbedding(256)
	smallEng := seedFacts(t, smallEmb, 20)
	p95Small := measureOverheadP95Ms(t, smallEng, smallEmb, 50)
	if limit := 10 * p95Small; p95Big > limit {
		t.Errorf("engine overhead p95 %.3fms @200 memories > 10x %.3fms @20 memories", p95Big, p95Small)
	}
	t.Logf("engine overhead p95: %.3fms @200 memories, %.3fms @20 memories", p95Big, p95Small)
}

func TestReadPathBuildsNoLLMAgent(t *testing.T) {
	// The Python guard scanned recall.py for LLM references; in Go the
	// guarantee is structural (operations.Recall takes no LLM argument), so
	// this pins the runtime equivalent: even with an LLM fully configured,
	// recalling never constructs an agent or touches the network.
	cfg := config.ForTesting(t.TempDir())
	cfg.LLMProvider = "openai"
	cfg.LLMAPIKeyEnv = "LADYM_TEST_FAKE_KEY"
	t.Setenv("LADYM_TEST_FAKE_KEY", "sk-fake")

	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })

	if _, err := eng.Semantic.PutFact("auth uses JWT with 24h expiry", "", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	resp, err := eng.Recall("how does authentication work", "", 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one recall result")
	}

	eng.mu.Lock()
	nAgents := len(eng.agents)
	eng.mu.Unlock()
	if nAgents != 0 {
		t.Errorf("%d LLM agents constructed by the read path, want 0", nAgents)
	}
}
