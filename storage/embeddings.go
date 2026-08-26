// Package storage holds LadyM's persistence layer: the SQLite store, pluggable
// embedding providers, and the vector index.
package storage

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/secrets"
	"golang.org/x/crypto/blake2b"
)

// tokenRe splits text into word runs, single punctuation chars (matching
// Python's _TOKEN_RE), or CJK script runs that segmentCJK breaks into words.
// Without the CJK alternatives, Chinese/Japanese/Korean text would tokenize
// to an empty set and every CJK query would score similarity 0.
var tokenRe = regexp.MustCompile(
	`[A-Za-z0-9_]+|[.,;:()\[\]{}]|[\p{Han}]+|[\p{Hiragana}\p{Katakana}]+|[\p{Hangul}]+`)

// cjkRunRe matches the CJK alternatives of tokenRe (everything after the
// ASCII word and punctuation branches).
var cjkRunRe = regexp.MustCompile(`[\p{Han}]+|[\p{Hiragana}\p{Katakana}]+|[\p{Hangul}]+`)

// segmentCJK splits a CJK script run into word tokens. Runs whose script
// the active dictionary variant covers (Han for zh variants; Han + Kana
// for jp) are segmented by gse; everything else — Hangul always, kana under
// zh dictionaries, and any script without a dictionary — falls back to
// per-character tokens. features() adds adjacent-pair bigrams on top, so
// the fallback still yields non-empty token sets and working similarity.
func segmentCJK(run string) []string {
	if seg := cjkSegmenterFor(runScript(run)); seg != nil {
		if words := seg.Cut(run, true); len(words) > 0 {
			return words
		}
	}
	var out []string
	for _, r := range run {
		out = append(out, string(r))
	}
	return out
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// splitCamel splits a word token into camelCase/snake_case parts, replicating
// Python's re.findall(r"[A-Z]+(?=[A-Z][a-z])|[A-Z]?[a-z]+|[A-Z]+|\d+", raw).
func splitCamel(raw string) []string {
	var out []string
	i, n := 0, len(raw)
	for i < n {
		c := raw[i]
		if isUpper(c) {
			j := i
			for j < n && isUpper(raw[j]) {
				j++
			}
			matched := false
			for k := j; k > i; k-- {
				if k < n && isUpper(raw[k]) && k+1 < n && isLower(raw[k+1]) {
					out = append(out, raw[i:k])
					i = k
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			if i+1 < n && isLower(raw[i+1]) {
				k := i + 1
				for k < n && isLower(raw[k]) {
					k++
				}
				out = append(out, raw[i:k])
				i = k
				continue
			}
			k := i
			for k < n && isUpper(raw[k]) {
				k++
			}
			out = append(out, raw[i:k])
			i = k
			continue
		}
		if isLower(c) {
			k := i
			for k < n && isLower(raw[k]) {
				k++
			}
			out = append(out, raw[i:k])
			i = k
			continue
		}
		if isDigit(c) {
			k := i
			for k < n && isDigit(raw[k]) {
				k++
			}
			out = append(out, raw[i:k])
			i = k
			continue
		}
		i++
	}
	return out
}

// Tokenize is the lightweight tokenizer: words + punctuation as separate
// tokens, with camelCase and snake_case splitting so getUserName and
// get_user_name tokenize similarly. CJK script runs are segmented into words
// by gse (jieba dictionary) for Chinese and per character for kana/hangul.
func Tokenize(text string) []string {
	var out []string
	for _, raw := range tokenRe.FindAllString(text, -1) {
		if cjkRunRe.MatchString(raw) {
			for _, p := range segmentCJK(raw) {
				out = append(out, strings.ToLower(p))
			}
			continue
		}
		parts := splitCamel(raw)
		if len(parts) > 0 {
			for _, p := range parts {
				out = append(out, strings.ToLower(p))
			}
		} else {
			out = append(out, strings.ToLower(raw))
		}
	}
	return out
}

// EmbeddingProvider is the contract every provider implements.
type EmbeddingProvider interface {
	// Embed returns a float32 vector for text.
	Embed(text string) ([]float32, error)
	// EmbedBatch embeds a batch of texts (defaults to looping Embed).
	EmbedBatch(texts []string) ([][]float32, error)
	// Dim returns the vector dimension, or 0 when unknown until the first
	// embed call (deferred-dim providers such as Ollama).
	Dim() int
	// HealthCheck performs a one-shot probe for the web UI "test embedding".
	HealthCheck() (bool, string)
}

// embedBatchDefault loops Embed for providers without a batch endpoint.
func embedBatchDefault(p EmbeddingProvider, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		v, err := p.Embed(t)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func healthCheckDefault(p EmbeddingProvider) (bool, string) {
	v, err := p.Embed("dimensionality probe")
	if err != nil {
		return false, err.Error()
	}
	return true, fmt.Sprintf("ok dim=%d", len(v))
}

// HashingEmbedding is the deterministic, offline embedding via feature
// hashing (unigram + bigram features, L2-normalised). Tokenization covers
// ASCII plus CJK scripts (dictionary-backed word segmentation for Chinese;
// per-character for kana/hangul), so Chinese, Japanese, and Korean text
// embed without any network or model download.
type HashingEmbedding struct {
	dim int
}

// NewHashingEmbedding returns a HashingEmbedding with the given dim.
func NewHashingEmbedding(dim int) *HashingEmbedding {
	return &HashingEmbedding{dim: dim}
}

func (h *HashingEmbedding) Dim() int { return h.dim }

func (h *HashingEmbedding) features(text string) []string {
	toks := Tokenize(text)
	feats := make([]string, 0, len(toks)*2)
	feats = append(feats, toks...)
	for i := 0; i+1 < len(toks); i++ {
		feats = append(feats, toks[i]+"_"+toks[i+1])
	}
	return feats
}

func (h *HashingEmbedding) Embed(text string) ([]float32, error) {
	vec := make([]float64, h.dim)
	for _, feat := range h.features(text) {
		hh, _ := blake2b.New(8, nil)
		hh.Write([]byte(feat))
		d := hh.Sum(nil)
		bucket := int(binary.LittleEndian.Uint32(d[0:4])) % h.dim
		sign := 1.0
		if d[4]&1 == 1 {
			sign = -1.0
		}
		vec[bucket] += sign
	}
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		norm = 1.0
	}
	out := make([]float32, h.dim)
	for i, v := range vec {
		out[i] = float32(v / norm)
	}
	return out, nil
}

func (h *HashingEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	return embedBatchDefault(h, texts)
}

func (h *HashingEmbedding) HealthCheck() (bool, string) { return healthCheckDefault(h) }

// CosineSimilarity returns the cosine of two vectors (assumed or forced to be
// normalised). Returns 0 when dimensions differ or either vector is zero.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0.0
	}
	var dot, na, nb float64
	for i := range a {
		x := float64(a[i])
		y := float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0.0 || nb == 0.0 {
		return 0.0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// EmbeddingProviderError is the base error for provider-side failures.
type EmbeddingProviderError struct {
	Msg string
}

func (e *EmbeddingProviderError) Error() string { return e.Msg }

// EmbeddingDimensionMismatch is raised when a reopened DB holds vectors of a
// different dim than the live provider.
type EmbeddingDimensionMismatch struct {
	Stored     int
	Configured int
}

func (e *EmbeddingDimensionMismatch) Error() string {
	return fmt.Sprintf(
		"embedding dim mismatch: DB has %d-dim vectors but provider returns %d-dim. Set embedding.allow_dim_change=true to wipe and re-embed.",
		e.Stored, e.Configured)
}

// AssertDimMatches raises EmbeddingDimensionMismatch when dims differ.
func AssertDimMatches(stored, configured int) error {
	if stored != configured {
		return &EmbeddingDimensionMismatch{Stored: stored, Configured: configured}
	}
	return nil
}

// callableRegistry holds user-registered callable embedding providers.
var callableRegistry = map[string]func(string) ([]float32, error){}

// RegisterCallable registers a Go function as a named embedding provider.
func RegisterCallable(name string, fn func(string) ([]float32, error)) {
	callableRegistry[name] = fn
}

func missingEmbeddingKeyMsg(envName string) string {
	return fmt.Sprintf(
		`embedding provider "openai" needs an API key but "%s" is neither registered in the secret store nor set as an environment variable. Run `+"`ladym config set-master-key`"+` then `+"`ladym config set %s <value>`"+`, or switch embedding.provider in ladym.toml to an offline option (hashing/ollama) if you don't need hosted embeddings.`,
		envName, envName)
}

func resolveEmbeddingKey(cfg *config.Config) (string, error) {
	envName := cfg.EmbeddingAPIKeyEnv
	if envName == "" {
		return "", nil
	}
	store := secrets.NewStore("")
	v, err := store.Get(envName)
	if err != nil {
		return "", err
	}
	if v != "" {
		return v, nil
	}
	return os.Getenv(envName), nil
}

// MakeProvider resolves the configured embedding provider.
func MakeProvider(cfg *config.Config) (EmbeddingProvider, error) {
	name := strings.ToLower(cfg.EmbeddingProvider)
	var provider EmbeddingProvider
	switch name {
	case "hashing":
		provider = NewHashingEmbedding(cfg.EmbeddingDim)
	case "st", "sentence-transformer", "sentence_transformers":
		return nil, fmt.Errorf(
			"sentence-transformers embeddings are not available in the Go port; use provider \"http\" or \"ollama\" to point at a local embedding endpoint")
	case "openai":
		apiKey, err := resolveEmbeddingKey(cfg)
		if err != nil {
			return nil, err
		}
		if apiKey == "" && os.Getenv(cfg.EmbeddingAPIKeyEnv) == "" {
			return nil, configError(missingEmbeddingKeyMsg(cfg.EmbeddingAPIKeyEnv))
		}
		model := cfg.EmbeddingModel
		if model == "" {
			model = "text-embedding-3-small"
		}
		provider = NewOpenAIEmbedding(model, cfg.EmbeddingBaseURL, apiKey, cfg.EmbeddingTimeoutS)
	case "ollama":
		base := cfg.EmbeddingBaseURL
		if base == "" {
			base = "http://localhost:11434"
		}
		model := cfg.EmbeddingModel
		if model == "" {
			model = "nomic-embed-text"
		}
		provider = NewOllamaEmbedding(base, model, cfg.EmbeddingTimeoutS, nil)
	case "http":
		provider = NewHTTPEmbedding(HTTPEmbeddingOptions{
			BaseURL:      cfg.EmbeddingBaseURL,
			Request:      cfg.EmbeddingHTTPRequest,
			ResponsePath: cfg.EmbeddingHTTPResponsePath,
			Dim:          cfg.EmbeddingDim,
			Model:        cfg.EmbeddingModel,
			TimeoutS:     cfg.EmbeddingTimeoutS,
		})
	case "callable":
		fn := callableRegistry[cfg.EmbeddingModel]
		if fn == nil {
			return nil, fmt.Errorf("no callable embedding registered under %q (call storage.RegisterCallable first)", cfg.EmbeddingModel)
		}
		provider = NewCallableEmbedding(fn, cfg.EmbeddingDim)
	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", name)
	}

	if cfg.EmbeddingQueryCacheSize > 0 {
		provider = NewCachedEmbedding(provider, cfg.EmbeddingQueryCacheSize)
	}
	return provider, nil
}

func configError(msg string) error { return &config.ConfigError{Msg: msg} }
