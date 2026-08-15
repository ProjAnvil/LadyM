package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// HTTPPoster is the minimal HTTP contract used by embedding providers so tests
// can inject a fake and never touch the network.
type HTTPPoster interface {
	Post(url string, payload any, headers map[string]string) (any, error)
}

// RealHTTPPoster is the net/http-backed client.
type RealHTTPPoster struct {
	Timeout time.Duration
}

// NewRealHTTPPoster returns a RealHTTPPoster with the given timeout.
func NewRealHTTPPoster(timeoutS float64) *RealHTTPPoster {
	return &RealHTTPPoster{Timeout: time.Duration(timeoutS * float64(time.Second))}
}

func (c *RealHTTPPoster) Post(url string, payload any, headers map[string]string) (any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: c.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("embedding endpoint returned %s: %s", resp.Status, string(respBody))
	}
	var out any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FakeHTTPPoster is a test double. Responder maps payload → JSON-able result.
type FakeHTTPPoster struct {
	Responder    func(payload any) (any, error)
	ExpectedPath string
	LastPayload  any
	LastURL      string
}

func (f *FakeHTTPPoster) Post(url string, payload any, headers map[string]string) (any, error) {
	f.LastURL = url
	f.LastPayload = payload
	if f.ExpectedPath != "" && !strings.Contains(url, f.ExpectedPath) {
		return nil, fmt.Errorf("expected path %q in %q", f.ExpectedPath, url)
	}
	return f.Responder(payload)
}

func extractPath(obj any, path string) (any, error) {
	cur := obj
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot navigate %q into %T", part, cur)
		}
		cur, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("missing key %q in embedding response", part)
		}
	}
	return cur, nil
}

func toFloat32Slice(v any) ([]float32, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON array, got %T", v)
	}
	out := make([]float32, 0, len(arr))
	for _, x := range arr {
		f, ok := x.(float64)
		if !ok {
			return nil, fmt.Errorf("expected a number in embedding array, got %T", x)
		}
		out = append(out, float32(f))
	}
	return out, nil
}

// OllamaEmbedding targets the Ollama /api/embeddings endpoint. Dim is deferred
// until the first embed call.
type OllamaEmbedding struct {
	baseURL  string
	model    string
	timeoutS float64
	client   HTTPPoster
	dim      int
}

// NewOllamaEmbedding builds an OllamaEmbedding (client may be nil for real HTTP).
func NewOllamaEmbedding(baseURL, model string, timeoutS float64, client HTTPPoster) *OllamaEmbedding {
	if timeoutS <= 0 {
		timeoutS = 10.0
	}
	return &OllamaEmbedding{
		baseURL:  strings.TrimRight(baseURL, "/"),
		model:    model,
		timeoutS: timeoutS,
		client:   client,
	}
}

func (o *OllamaEmbedding) Dim() int { return o.dim }

func (o *OllamaEmbedding) httpClient() HTTPPoster {
	if o.client != nil {
		return o.client
	}
	return NewRealHTTPPoster(o.timeoutS)
}

func (o *OllamaEmbedding) post(prompt string) ([]float32, error) {
	client := o.httpClient()
	resp, err := client.Post(o.baseURL+"/api/embeddings", map[string]any{"model": o.model, "prompt": prompt}, nil)
	if err != nil {
		return nil, err
	}
	m, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected ollama response: %T", resp)
	}
	return toFloat32Slice(m["embedding"])
}

func (o *OllamaEmbedding) Embed(text string) ([]float32, error) {
	vec, err := o.post(text)
	if err != nil {
		return nil, err
	}
	if o.dim == 0 {
		o.dim = len(vec)
	}
	return vec, nil
}

func (o *OllamaEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	return embedBatchDefault(o, texts)
}

func (o *OllamaEmbedding) HealthCheck() (bool, string) { return healthCheckDefault(o) }

// OpenAIEmbedding targets the OpenAI (or OpenAI-compatible) embeddings API.
type OpenAIEmbedding struct {
	model    string
	baseURL  string
	apiKey   string
	timeoutS float64
	client   HTTPPoster
	dim      int
}

// NewOpenAIEmbedding builds an OpenAIEmbedding. Empty baseURL defaults to
// https://api.openai.com/v1.
func NewOpenAIEmbedding(model, baseURL, apiKey string, timeoutS float64) *OpenAIEmbedding {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if timeoutS <= 0 {
		timeoutS = 10.0
	}
	return &OpenAIEmbedding{
		model:    model,
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		timeoutS: timeoutS,
	}
}

func (o *OpenAIEmbedding) Dim() int { return o.dim }

func (o *OpenAIEmbedding) httpClient() HTTPPoster {
	if o.client != nil {
		return o.client
	}
	return NewRealHTTPPoster(o.timeoutS)
}

func (o *OpenAIEmbedding) Embed(text string) ([]float32, error) {
	client := o.httpClient()
	headers := map[string]string{}
	if o.apiKey != "" {
		headers["Authorization"] = "Bearer " + o.apiKey
	}
	resp, err := client.Post(o.baseURL+"/embeddings", map[string]any{"model": o.model, "input": text}, headers)
	if err != nil {
		return nil, err
	}
	m, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected openai embeddings response: %T", resp)
	}
	data, ok := m["data"].([]any)
	if !ok || len(data) == 0 {
		return nil, fmt.Errorf("openai embeddings response missing data")
	}
	first, ok := data[0].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openai embeddings data[0] is not an object")
	}
	vec, err := toFloat32Slice(first["embedding"])
	if err != nil {
		return nil, err
	}
	if o.dim == 0 {
		o.dim = len(vec)
	}
	return vec, nil
}

func (o *OpenAIEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	client := o.httpClient()
	headers := map[string]string{}
	if o.apiKey != "" {
		headers["Authorization"] = "Bearer " + o.apiKey
	}
	resp, err := client.Post(o.baseURL+"/embeddings", map[string]any{"model": o.model, "input": texts}, headers)
	if err != nil {
		return nil, err
	}
	m, ok := resp.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected openai embeddings response: %T", resp)
	}
	data, ok := m["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("openai embeddings response missing data")
	}
	objs := make([]map[string]any, 0, len(data))
	allIndexed := len(data) > 0
	for _, d := range data {
		obj, ok := d.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("openai embeddings item is not an object")
		}
		if _, ok := obj["index"].(float64); !ok {
			allIndexed = false
		}
		objs = append(objs, obj)
	}
	// Align results to input order via the "index" field (Python sorts
	// resp.data by index); fall back to response order when index is absent.
	if allIndexed {
		sort.SliceStable(objs, func(i, j int) bool {
			return objs[i]["index"].(float64) < objs[j]["index"].(float64)
		})
	}
	out := make([][]float32, 0, len(objs))
	for _, obj := range objs {
		vec, err := toFloat32Slice(obj["embedding"])
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	if o.dim == 0 && len(out) > 0 {
		o.dim = len(out[0])
	}
	return out, nil
}

func (o *OpenAIEmbedding) HealthCheck() (bool, string) { return healthCheckDefault(o) }

// HTTPEmbeddingOptions configures the generic HTTP embedding provider.
type HTTPEmbeddingOptions struct {
	BaseURL      string
	Request      string
	ResponsePath string
	Dim          int
	Model        string
	TimeoutS     float64
	// Poster, when non-nil, overrides the default real HTTP client (tests).
	Poster HTTPPoster
}

// HTTPEmbedding is a generic, template-driven embedding provider.
type HTTPEmbedding struct {
	baseURL      string
	requestTmpl  string
	responsePath string
	dim          int
	model        string
	client       HTTPPoster
	headers      map[string]string
}

// NewHTTPEmbedding builds a generic HTTP embedding provider.
func NewHTTPEmbedding(opts HTTPEmbeddingOptions) *HTTPEmbedding {
	client := opts.Poster
	if client == nil {
		client = NewRealHTTPPoster(opts.TimeoutS)
	}
	return &HTTPEmbedding{
		baseURL:      opts.BaseURL,
		requestTmpl:  opts.Request,
		responsePath: opts.ResponsePath,
		dim:          opts.Dim,
		model:        opts.Model,
		client:       client,
	}
}

func (h *HTTPEmbedding) Dim() int { return h.dim }

func (h *HTTPEmbedding) Embed(text string) ([]float32, error) {
	quoted := jsonBytes(text) // JSON-string-escaped, wrapped in quotes
	escaped := string(quoted[1 : len(quoted)-1])
	body := strings.ReplaceAll(h.requestTmpl, "{text}", escaped)
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, fmt.Errorf("invalid request template after substitution: %w", err)
	}
	resp, err := h.client.Post(h.baseURL, payload, h.headers)
	if err != nil {
		return nil, err
	}
	raw, err := extractPath(resp, h.responsePath)
	if err != nil {
		return nil, err
	}
	vec, err := toFloat32Slice(raw)
	if err != nil {
		return nil, err
	}
	if len(vec) != h.dim {
		return nil, fmt.Errorf("dim mismatch: declared %d, got %d", h.dim, len(vec))
	}
	return vec, nil
}

func jsonBytes(v string) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (h *HTTPEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	return embedBatchDefault(h, texts)
}

func (h *HTTPEmbedding) HealthCheck() (bool, string) { return healthCheckDefault(h) }

// CallableEmbedding wraps a Go function as an embedding provider.
type CallableEmbedding struct {
	fn  func(string) ([]float32, error)
	dim int
}

// NewCallableEmbedding wraps fn as an embedding provider.
func NewCallableEmbedding(fn func(string) ([]float32, error), dim int) *CallableEmbedding {
	return &CallableEmbedding{fn: fn, dim: dim}
}

func (c *CallableEmbedding) Dim() int { return c.dim }

func (c *CallableEmbedding) Embed(text string) ([]float32, error) { return c.fn(text) }

func (c *CallableEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	return embedBatchDefault(c, texts)
}

func (c *CallableEmbedding) HealthCheck() (bool, string) { return healthCheckDefault(c) }
