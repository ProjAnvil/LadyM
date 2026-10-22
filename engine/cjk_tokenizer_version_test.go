package engine

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// The CJK tokenizer mode reaches the engine through config, and engine.New
// pushes it into the storage package, so these tests drive cfg rather than the
// storage global. That is the path a real `ladym` process takes.

// TestEnforceEmbeddingTokenizerRecordsVersion pins that a fresh database
// records which tokenizer produced its vectors, so a later switch is
// detectable rather than silent.
func TestEnforceEmbeddingTokenizerRecordsVersion(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerDict
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	got, err := eng.Store.GetMeta("embedding_tokenizer")
	if err != nil {
		t.Fatal(err)
	}
	if want := storage.CJKTokenizerVersion(); got != want {
		t.Errorf("embedding_tokenizer = %q, want %q", got, want)
	}
}

// TestReopenWithChangedTokenizerFails is the guard: switching the CJK
// tokenizer invalidates every stored CJK vector, so without an explicit
// opt-in the reopen must fail loudly instead of quietly degrading recall.
func TestReopenWithChangedTokenizerFails(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerDict
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Remember("数据库连接池耗尽", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerChars
	_, err = New(cfg)
	if err == nil {
		t.Fatal("reopen with a changed CJK tokenizer should fail")
	}
	if _, ok := errors.AsType[*storage.EmbeddingTokenizerMismatch](err); !ok {
		t.Fatalf("err = %v, want EmbeddingTokenizerMismatch", err)
	}
}

// TestReopenWithChangedTokenizerAllowedReembeds pins the opt-in path: with
// allow_dim_change the reopen re-embeds and records the new version.
func TestReopenWithChangedTokenizerAllowedReembeds(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerDict
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Remember("数据库连接池耗尽", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	eng.Close()

	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerChars
	cfg.EmbeddingAllowDimChange = true
	eng2, err := New(cfg)
	if err != nil {
		t.Fatalf("reopen with allow_dim_change: %v", err)
	}
	defer eng2.Close()
	got, err := eng2.Store.GetMeta("embedding_tokenizer")
	if err != nil {
		t.Fatal(err)
	}
	if want := storage.CJKTokenizerVersion(); got != want {
		t.Errorf("embedding_tokenizer after re-embed = %q, want %q", got, want)
	}
}

// TestHostedProviderRecordsNoTokenizerVersion pins that providers which
// tokenize server-side are untouched by the CJK mode: recording a version for
// them would force a pointless re-embed the first time someone switches modes.
func TestHostedProviderRecordsNoTokenizerVersion(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	cfg.EmbeddingProvider = "callable"
	cfg.EmbeddingModel = "tok-probe"
	cfg.EmbeddingCJKTokenizer = storage.CJKTokenizerChars
	storage.RegisterCallable("tok-probe", func(string) ([]float32, error) {
		return make([]float32, 256), nil
	})
	t.Cleanup(func() { storage.RegisterCallable("tok-probe", nil) })
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if got, err := eng.Store.GetMeta("embedding_tokenizer"); err != nil {
		t.Fatal(err)
	} else if got != "" {
		t.Errorf("embedding_tokenizer = %q, want empty for a non-hashing provider", got)
	}
}
