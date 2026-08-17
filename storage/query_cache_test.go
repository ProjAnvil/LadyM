package storage

import (
	"fmt"
	"reflect"
	"testing"
)

// stubProvider is a minimal EmbeddingProvider for cache tests.
type stubProvider struct {
	dim      int
	calls    int
	batch    int
	failOn   string
	healthOK bool
	healthMS string
}

func (p *stubProvider) Dim() int { return p.dim }

func (p *stubProvider) Embed(text string) ([]float32, error) {
	p.calls++
	if text == p.failOn {
		return nil, fmt.Errorf("embed failed for %q", text)
	}
	return []float32{float32(len(text))}, nil
}

func (p *stubProvider) EmbedBatch(texts []string) ([][]float32, error) {
	p.batch++
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		out = append(out, []float32{float32(len(t))})
	}
	return out, nil
}

func (p *stubProvider) HealthCheck() (bool, string) { return p.healthOK, p.healthMS }

func TestCachedEmbeddingBasics(t *testing.T) {
	inner := &stubProvider{dim: 7, healthOK: true, healthMS: "ok dim=7"}
	c := NewCachedEmbedding(inner, 2)
	if c.Dim() != 7 {
		t.Errorf("Dim = %d, want 7", c.Dim())
	}
	ok, msg := c.HealthCheck()
	if !ok || msg != "ok dim=7" {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}

	// EmbedBatch delegates straight through without caching.
	batch, err := c.EmbedBatch([]string{"ab", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch, [][]float32{{2}, {1}}) {
		t.Errorf("EmbedBatch = %v", batch)
	}
	if inner.batch != 1 {
		t.Errorf("inner batch calls = %d, want 1", inner.batch)
	}
}

func TestCachedEmbeddingHitMissEvict(t *testing.T) {
	inner := &stubProvider{dim: 1}
	c := NewCachedEmbedding(inner, 2)

	v1, err := c.Embed("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1", inner.calls)
	}
	// Returned vector is a copy — mutating it must not poison the cache.
	v1[0] = -99
	v2, err := c.Embed("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Errorf("calls after cache hit = %d, want 1", inner.calls)
	}
	if v2[0] != 5 {
		t.Errorf("cached vec = %v, want [5]", v2)
	}

	// Fill to capacity, touch alpha (making it MRU), then overflow: beta is
	// the LRU entry and gets evicted, alpha survives.
	if _, err := c.Embed("beta"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Embed("alpha"); err != nil { // hit → touch → order [beta, alpha]
		t.Fatal(err)
	}
	if _, err := c.Embed("gamma"); err != nil { // overflow → evict beta
		t.Fatal(err)
	}
	if inner.calls != 3 {
		t.Fatalf("calls = %d, want 3", inner.calls)
	}
	if _, err := c.Embed("alpha"); err != nil { // still cached
		t.Fatal(err)
	}
	if inner.calls != 3 {
		t.Errorf("calls = %d, want 3 (alpha should still be cached)", inner.calls)
	}
	if _, err := c.Embed("beta"); err != nil { // evicted → re-embed
		t.Fatal(err)
	}
	if inner.calls != 4 {
		t.Errorf("calls = %d, want 4 (beta should have been evicted)", inner.calls)
	}

	// Errors are not cached.
	inner.failOn = "boom"
	if _, err := c.Embed("boom"); err == nil {
		t.Fatal("expected embed error")
	}
	if _, err := c.Embed("boom"); err == nil {
		t.Fatal("expected embed error again (errors must not be cached)")
	}
	if inner.calls != 6 {
		t.Errorf("calls = %d, want 6", inner.calls)
	}
}
