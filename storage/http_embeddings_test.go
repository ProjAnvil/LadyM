package storage

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestOllamaTimeoutHonored(t *testing.T) {
	o := NewOllamaEmbedding("http://localhost:11434", "nomic-embed-text", 3.5, nil)
	p, ok := o.httpClient().(*RealHTTPPoster)
	if !ok {
		t.Fatalf("expected *RealHTTPPoster, got %T", o.httpClient())
	}
	if p.Timeout != time.Duration(3.5*float64(time.Second)) {
		t.Errorf("timeout = %v, want 3.5s", p.Timeout)
	}
}

func TestOpenAITimeoutHonored(t *testing.T) {
	o := NewOpenAIEmbedding("m", "", "k", 7.25)
	p, ok := o.httpClient().(*RealHTTPPoster)
	if !ok {
		t.Fatalf("expected *RealHTTPPoster, got %T", o.httpClient())
	}
	if p.Timeout != time.Duration(7.25*float64(time.Second)) {
		t.Errorf("timeout = %v, want 7.25s", p.Timeout)
	}
}

func TestOpenAIEmbedBatchSortsByIndex(t *testing.T) {
	// Server returns the data array out of order; results must still align
	// with the input order via the "index" field (Python: sorted(resp.data,
	// key=lambda x: x.index)).
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"data": []any{
			map[string]any{"index": float64(1), "embedding": []any{float64(0), float64(1)}},
			map[string]any{"index": float64(0), "embedding": []any{float64(1), float64(0)}},
		}}, nil
	}}
	emb := NewOpenAIEmbedding("m", "", "k", 10.0)
	emb.client = fake
	got, err := emb.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]float32{{1, 0}, {0, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EmbedBatch = %v, want %v (aligned to input order)", got, want)
	}
}

func TestOpenAIEmbedBatchNoIndexFallsBackToOrder(t *testing.T) {
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"data": []any{
			map[string]any{"embedding": []any{float64(9)}},
			map[string]any{"embedding": []any{float64(8)}},
		}}, nil
	}}
	emb := NewOpenAIEmbedding("m", "", "k", 10.0)
	emb.client = fake
	got, err := emb.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]float32{{9}, {8}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EmbedBatch = %v, want %v (response order preserved)", got, want)
	}
}

func TestHTTPEmbeddingWithInjectedPoster(t *testing.T) {
	var gotPayload any
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		gotPayload = payload
		return map[string]any{"data": map[string]any{"embedding": []any{float64(0.5), float64(0.5)}}}, nil
	}}
	h := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://embed.local/v1",
		Request:      `{"input": "{text}", "model": "m"}`,
		ResponsePath: "data.embedding",
		Dim:          2,
		Poster:       fake,
	})
	vec, err := h.Embed(`he said "hi"`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vec, []float32{0.5, 0.5}) {
		t.Errorf("vec = %v", vec)
	}
	// Template substitution: {text} replaced with JSON-escaped (unquoted) text.
	m, ok := gotPayload.(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want object", gotPayload)
	}
	if m["input"] != `he said "hi"` {
		t.Errorf("payload input = %v, want substituted text", m["input"])
	}
}

func TestHTTPEmbeddingErrors(t *testing.T) {
	// Missing key along response_path.
	h := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{"input": "{text}"}`,
		ResponsePath: "data.embedding",
		Dim:          2,
		Poster: &FakeHTTPPoster{Responder: func(payload any) (any, error) {
			return map[string]any{"other": true}, nil
		}},
	})
	if _, err := h.Embed("hi"); err == nil {
		t.Error("expected error for missing response_path key")
	}

	// Dim mismatch.
	h2 := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{"input": "{text}"}`,
		ResponsePath: "embedding",
		Dim:          3,
		Poster: &FakeHTTPPoster{Responder: func(payload any) (any, error) {
			return map[string]any{"embedding": []any{float64(1), float64(2)}}, nil
		}},
	})
	if _, err := h2.Embed("hi"); err == nil {
		t.Error("expected dim-mismatch error")
	}

	// Poster error propagates.
	h3 := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{"input": "{text}"}`,
		ResponsePath: "embedding",
		Dim:          1,
		Poster: &FakeHTTPPoster{Responder: func(payload any) (any, error) {
			return nil, fmt.Errorf("boom")
		}},
	})
	if _, err := h3.Embed("hi"); err == nil {
		t.Error("expected poster error to propagate")
	}
}
