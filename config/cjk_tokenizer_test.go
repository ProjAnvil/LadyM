package config

import "testing"

// TestEmbeddingCJKTokenizerDefault pins the default: every database written so
// far used dictionary segmentation, so the default must not change what stored
// vectors mean.
func TestEmbeddingCJKTokenizerDefault(t *testing.T) {
	if got := Default().EmbeddingCJKTokenizer; got != "dict" {
		t.Errorf("default embedding cjk_tokenizer = %q, want \"dict\"", got)
	}
}

// TestEmbeddingCJKTokenizerFromToml covers the [embedding] cjk_tokenizer key
// reaching the flat field the engine reads.
func TestEmbeddingCJKTokenizerFromToml(t *testing.T) {
	c := Default()
	applyToml(c, map[string]any{"embedding": map[string]any{"cjk_tokenizer": "chars"}})
	if c.EmbeddingCJKTokenizer != "chars" {
		t.Errorf("toml cjk_tokenizer = %q, want \"chars\"", c.EmbeddingCJKTokenizer)
	}
}

// TestEmbeddingCJKTokenizerFromFlatKey covers the CLI's flat form
// (`ladym config set embedding_cjk_tokenizer chars`).
func TestEmbeddingCJKTokenizerFromFlatKey(t *testing.T) {
	c := Default()
	applyToml(c, map[string]any{"embedding_cjk_tokenizer": "chars"})
	if c.EmbeddingCJKTokenizer != "chars" {
		t.Errorf("flat cjk_tokenizer = %q, want \"chars\"", c.EmbeddingCJKTokenizer)
	}
}
