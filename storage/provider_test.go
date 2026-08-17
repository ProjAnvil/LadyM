package storage

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
)

func TestEmbedBatchDefaultAndHealthCheck(t *testing.T) {
	h := NewHashingEmbedding(8)
	if h.Dim() != 8 {
		t.Errorf("Dim = %d, want 8", h.Dim())
	}

	batch, err := h.EmbedBatch([]string{"hello world", "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Fatalf("EmbedBatch len = %d, want 2", len(batch))
	}
	single, err := h.Embed("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch[0], single) {
		t.Errorf("batch[0] != single Embed")
	}

	ok, msg := h.HealthCheck()
	if !ok || !strings.Contains(msg, "dim=8") {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}

	// healthCheckDefault reports the inner error.
	bad := NewCallableEmbedding(func(string) ([]float32, error) {
		return nil, fmt.Errorf("down")
	}, 4)
	ok, msg = bad.HealthCheck()
	if ok || msg != "down" {
		t.Errorf("HealthCheck on failing provider = (%v, %q)", ok, msg)
	}

	// embedBatchDefault propagates errors.
	if _, err := bad.EmbedBatch([]string{"x"}); err == nil {
		t.Error("expected EmbedBatch error to propagate")
	}
}

func TestEmbeddingErrorTypes(t *testing.T) {
	e := &EmbeddingProviderError{Msg: "provider down"}
	if e.Error() != "provider down" {
		t.Errorf("Error() = %q", e.Error())
	}

	mm := &EmbeddingDimensionMismatch{Stored: 128, Configured: 256}
	if !strings.Contains(mm.Error(), "128") || !strings.Contains(mm.Error(), "256") {
		t.Errorf("Error() = %q", mm.Error())
	}

	if err := AssertDimMatches(8, 8); err != nil {
		t.Errorf("AssertDimMatches(8,8) = %v, want nil", err)
	}
	err := AssertDimMatches(8, 16)
	if err == nil {
		t.Fatal("AssertDimMatches(8,16) = nil, want error")
	}
	got, ok := err.(*EmbeddingDimensionMismatch)
	if !ok {
		t.Fatalf("error type = %T, want *EmbeddingDimensionMismatch", err)
	}
	if got.Stored != 8 || got.Configured != 16 {
		t.Errorf("mismatch = %+v", got)
	}
}

func TestCallableEmbedding(t *testing.T) {
	c := NewCallableEmbedding(func(text string) ([]float32, error) {
		return []float32{1, 2}, nil
	}, 2)
	if c.Dim() != 2 {
		t.Errorf("Dim = %d, want 2", c.Dim())
	}
	vec, err := c.Embed("anything")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vec, []float32{1, 2}) {
		t.Errorf("Embed = %v", vec)
	}
	batch, err := c.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Errorf("EmbedBatch len = %d, want 2", len(batch))
	}
	ok, msg := c.HealthCheck()
	if !ok || !strings.Contains(msg, "dim=2") {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}
}

func TestMakeProvider(t *testing.T) {
	// Keep any secret-store reads in a temp HOME.
	t.Setenv("HOME", t.TempDir())

	base := func() *config.Config {
		return &config.Config{EmbeddingProvider: "hashing", EmbeddingDim: 8}
	}

	// hashing
	p, err := MakeProvider(base())
	if err != nil {
		t.Fatal(err)
	}
	if p.Dim() != 8 {
		t.Errorf("hashing dim = %d, want 8", p.Dim())
	}

	// unknown
	cfg := base()
	cfg.EmbeddingProvider = "nonsense"
	if _, err := MakeProvider(cfg); err == nil {
		t.Error("expected error for unknown provider")
	}

	// sentence-transformers unsupported in the Go port
	cfg = base()
	cfg.EmbeddingProvider = "st"
	if _, err := MakeProvider(cfg); err == nil {
		t.Error("expected error for st provider")
	}

	// openai without any key → config error
	cfg = base()
	cfg.EmbeddingProvider = "openai"
	cfg.EmbeddingAPIKeyEnv = "LADYM_TEST_MISSING_KEY"
	if _, err := MakeProvider(cfg); err == nil {
		t.Error("expected config error for openai without key")
	} else if _, ok := err.(*config.ConfigError); !ok {
		t.Errorf("error type = %T, want *config.ConfigError", err)
	}

	// openai with key from the environment
	t.Setenv("LADYM_TEST_EMB_KEY", "sk-test")
	cfg = base()
	cfg.EmbeddingProvider = "openai"
	cfg.EmbeddingAPIKeyEnv = "LADYM_TEST_EMB_KEY"
	p, err = MakeProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*OpenAIEmbedding); !ok {
		t.Errorf("provider type = %T, want *OpenAIEmbedding", p)
	}

	// ollama (defaults for base URL and model)
	cfg = base()
	cfg.EmbeddingProvider = "ollama"
	p, err = MakeProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ol, ok := p.(*OllamaEmbedding)
	if !ok {
		t.Fatalf("provider type = %T, want *OllamaEmbedding", p)
	}
	if ol.baseURL != "http://localhost:11434" || ol.model != "nomic-embed-text" {
		t.Errorf("ollama defaults = %q %q", ol.baseURL, ol.model)
	}

	// generic http
	cfg = base()
	cfg.EmbeddingProvider = "http"
	cfg.EmbeddingBaseURL = "http://embed.local"
	p, err = MakeProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*HTTPEmbedding); !ok {
		t.Errorf("provider type = %T, want *HTTPEmbedding", p)
	}

	// callable: unregistered name → error
	cfg = base()
	cfg.EmbeddingProvider = "callable"
	cfg.EmbeddingModel = "not-registered"
	if _, err := MakeProvider(cfg); err == nil {
		t.Error("expected error for unregistered callable")
	}

	// callable: registered
	RegisterCallable("testfn", func(string) ([]float32, error) { return []float32{0}, nil })
	cfg = base()
	cfg.EmbeddingProvider = "callable"
	cfg.EmbeddingModel = "testfn"
	p, err = MakeProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*CallableEmbedding); !ok {
		t.Errorf("provider type = %T, want *CallableEmbedding", p)
	}

	// query cache wraps the resolved provider
	cfg = base()
	cfg.EmbeddingQueryCacheSize = 4
	p, err = MakeProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*CachedEmbedding); !ok {
		t.Errorf("provider type = %T, want *CachedEmbedding", p)
	}
}

func TestMissingEmbeddingKeyMsg(t *testing.T) {
	msg := missingEmbeddingKeyMsg("MY_KEY")
	if !strings.Contains(msg, "MY_KEY") || !strings.Contains(msg, "set-master-key") {
		t.Errorf("msg = %q", msg)
	}
}

func TestResolveEmbeddingKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Empty env name → empty key, no lookup.
	v, err := resolveEmbeddingKey(&config.Config{EmbeddingAPIKeyEnv: ""})
	if err != nil || v != "" {
		t.Errorf("empty env = (%q, %v)", v, err)
	}

	// No secret stored → fall back to environment.
	t.Setenv("LADYM_TEST_RESOLVE_KEY", "from-env")
	v, err = resolveEmbeddingKey(&config.Config{EmbeddingAPIKeyEnv: "LADYM_TEST_RESOLVE_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if v != "from-env" {
		t.Errorf("resolveEmbeddingKey = %q, want from-env", v)
	}
}

func TestOllamaEmbed(t *testing.T) {
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"embedding": []any{float64(0.1), float64(0.2)}}, nil
	}}
	o := NewOllamaEmbedding("http://ollama.local/", "m", 0, fake) // timeout <= 0 → default
	if o.timeoutS != 10.0 {
		t.Errorf("default timeout = %v, want 10", o.timeoutS)
	}
	// Trailing slash trimmed.
	if o.baseURL != "http://ollama.local" {
		t.Errorf("baseURL = %q", o.baseURL)
	}
	if o.Dim() != 0 {
		t.Errorf("Dim before first embed = %d, want 0 (deferred)", o.Dim())
	}

	vec, err := o.Embed("hi")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vec, []float32{0.1, 0.2}) {
		t.Errorf("Embed = %v", vec)
	}
	if o.Dim() != 2 {
		t.Errorf("Dim after embed = %d, want 2", o.Dim())
	}
	if !strings.Contains(fake.LastURL, "/api/embeddings") {
		t.Errorf("URL = %q", fake.LastURL)
	}

	batch, err := o.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Errorf("EmbedBatch len = %d, want 2", len(batch))
	}

	ok, msg := o.HealthCheck()
	if !ok || !strings.Contains(msg, "dim=2") {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}

	// Error paths.
	errPoster := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return nil, fmt.Errorf("conn refused")
	}}
	o2 := NewOllamaEmbedding("http://x", "m", 1, errPoster)
	if _, err := o2.Embed("hi"); err == nil {
		t.Error("expected poster error to propagate")
	}
	badShape := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return []any{1}, nil // not an object
	}}
	o3 := NewOllamaEmbedding("http://x", "m", 1, badShape)
	if _, err := o3.Embed("hi"); err == nil {
		t.Error("expected error for non-object response")
	}
	badVec := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"embedding": "nope"}, nil
	}}
	o4 := NewOllamaEmbedding("http://x", "m", 1, badVec)
	if _, err := o4.Embed("hi"); err == nil {
		t.Error("expected error for non-array embedding")
	}
}

func TestOpenAIEmbed(t *testing.T) {
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"data": []any{
			map[string]any{"embedding": []any{float64(1), float64(0)}},
		}}, nil
	}}
	o := NewOpenAIEmbedding("m", "http://api.local/", "key", 0)
	o.client = fake
	if o.timeoutS != 10.0 {
		t.Errorf("default timeout = %v, want 10", o.timeoutS)
	}
	if o.baseURL != "http://api.local" {
		t.Errorf("baseURL = %q", o.baseURL)
	}
	vec, err := o.Embed("hello")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vec, []float32{1, 0}) {
		t.Errorf("Embed = %v", vec)
	}
	if o.Dim() != 2 {
		t.Errorf("Dim = %d, want 2", o.Dim())
	}
	ok, msg := o.HealthCheck()
	if !ok || !strings.Contains(msg, "dim=2") {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}

	// Error paths: poster failure, non-object response, missing data,
	// non-object data[0], bad embedding array.
	cases := []struct {
		name      string
		responder func(payload any) (any, error)
	}{
		{"poster error", func(any) (any, error) { return nil, fmt.Errorf("boom") }},
		{"non-object", func(any) (any, error) { return 42, nil }},
		{"missing data", func(any) (any, error) { return map[string]any{}, nil }},
		{"empty data", func(any) (any, error) { return map[string]any{"data": []any{}}, nil }},
		{"data[0] not object", func(any) (any, error) {
			return map[string]any{"data": []any{"x"}}, nil
		}},
		{"bad embedding", func(any) (any, error) {
			return map[string]any{"data": []any{map[string]any{"embedding": "z"}}}, nil
		}},
	}
	for _, c := range cases {
		e := NewOpenAIEmbedding("m", "", "k", 1)
		e.client = &FakeHTTPPoster{Responder: c.responder}
		if _, err := e.Embed("hi"); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}

	// EmbedBatch error paths.
	batchCases := []struct {
		name      string
		responder func(payload any) (any, error)
	}{
		{"poster error", func(any) (any, error) { return nil, fmt.Errorf("boom") }},
		{"non-object", func(any) (any, error) { return "x", nil }},
		{"missing data", func(any) (any, error) { return map[string]any{}, nil }},
		{"item not object", func(any) (any, error) {
			return map[string]any{"data": []any{5}}, nil
		}},
		{"bad embedding", func(any) (any, error) {
			return map[string]any{"data": []any{map[string]any{"embedding": true}}}, nil
		}},
	}
	for _, c := range batchCases {
		e := NewOpenAIEmbedding("m", "", "k", 1)
		e.client = &FakeHTTPPoster{Responder: c.responder}
		if _, err := e.EmbedBatch([]string{"a"}); err == nil {
			t.Errorf("batch %s: expected error", c.name)
		}
	}
}

func TestHTTPEmbeddingBatchAndHealth(t *testing.T) {
	fake := &FakeHTTPPoster{Responder: func(payload any) (any, error) {
		return map[string]any{"embedding": []any{float64(1), float64(2)}}, nil
	}}
	h := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{"input": "{text}"}`,
		ResponsePath: "embedding",
		Dim:          2,
		Poster:       fake,
	})
	if h.Dim() != 2 {
		t.Errorf("Dim = %d, want 2", h.Dim())
	}
	batch, err := h.EmbedBatch([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 {
		t.Errorf("EmbedBatch len = %d, want 2", len(batch))
	}
	ok, msg := h.HealthCheck()
	if !ok || !strings.Contains(msg, "dim=2") {
		t.Errorf("HealthCheck = (%v, %q)", ok, msg)
	}

	// Nil poster → real HTTP client.
	h2 := NewHTTPEmbedding(HTTPEmbeddingOptions{BaseURL: "http://x", TimeoutS: 5})
	if _, ok := h2.client.(*RealHTTPPoster); !ok {
		t.Errorf("client type = %T, want *RealHTTPPoster", h2.client)
	}

	// Invalid template after substitution.
	h3 := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{not-json {text}}`,
		ResponsePath: "embedding",
		Dim:          1,
		Poster:       fake,
	})
	if _, err := h3.Embed("hi"); err == nil {
		t.Error("expected template error")
	}
}

func TestFakeHTTPPosterExpectedPath(t *testing.T) {
	f := &FakeHTTPPoster{
		ExpectedPath: "/v1/embed",
		Responder:    func(payload any) (any, error) { return nil, nil },
	}
	if _, err := f.Post("http://x/other", nil, nil); err == nil {
		t.Error("expected path-mismatch error")
	}
	if _, err := f.Post("http://x/v1/embed", nil, nil); err != nil {
		t.Errorf("matching path: %v", err)
	}
}

func TestExtractPathAndToFloat32Slice(t *testing.T) {
	// Navigating into a non-map fails.
	if _, err := extractPath(map[string]any{"a": 5}, "a.b"); err == nil {
		t.Error("expected navigation error into non-map")
	}
	// Non-array input.
	if _, err := toFloat32Slice("x"); err == nil {
		t.Error("expected error for non-array")
	}
	// Non-number element.
	if _, err := toFloat32Slice([]any{1.0, "x"}); err == nil {
		t.Error("expected error for non-number element")
	}
	got, err := toFloat32Slice([]any{1.5, -2.0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []float32{1.5, -2}) {
		t.Errorf("toFloat32Slice = %v", got)
	}
}

func TestRealHTTPPoster(t *testing.T) {
	// Success roundtrip.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("custom header missing")
		}
		w.Write([]byte(`{"embedding": [1, 2]}`))
	}))
	defer srv.Close()

	p := NewRealHTTPPoster(5)
	out, err := p.Post(srv.URL, map[string]any{"input": "hi"}, map[string]string{"X-Test": "yes"})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["embedding"] == nil {
		t.Errorf("Post = %v", out)
	}

	// HTTP >= 400 surfaces status and body.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer errSrv.Close()
	if _, err := p.Post(errSrv.URL, nil, nil); err == nil {
		t.Error("expected error for 400 response")
	} else if !strings.Contains(err.Error(), "400") {
		t.Errorf("error = %v, want status in message", err)
	}

	// Invalid JSON body.
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{oops`))
	}))
	defer badSrv.Close()
	if _, err := p.Post(badSrv.URL, nil, nil); err == nil {
		t.Error("expected JSON unmarshal error")
	}

	// Unmarshalable payload.
	if _, err := p.Post(srv.URL, func() {}, nil); err == nil {
		t.Error("expected marshal error")
	}

	// Malformed URL.
	if _, err := p.Post("http://exa mple.com/x", nil, nil); err == nil {
		t.Error("expected request construction error")
	}

	// Unreachable server.
	unreachable := NewRealHTTPPoster(0.5)
	if _, err := unreachable.Post("http://127.0.0.1:1/x", nil, nil); err == nil {
		t.Error("expected connection error")
	}
}
