package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/storage"
	"github.com/odvcencio/gotreesitter"
)

func newTestStore(t *testing.T) (*storage.SQLiteStore, storage.EmbeddingProvider) {
	t.Helper()
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 256, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, storage.NewHashingEmbedding(256)
}

// Fix 1: cfg.CodeIndex.ExtraIgnoreGlobs must be wired into the file walk,
// using Python `_should_ignore` semantics (substring of the file basename
// after stripping trailing "*" and "/").
func TestIndexCodebaseExtraIgnoreGlobs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.py"), []byte("def keep():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated_api.py"), []byte("def generated():\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	cfg.CodeIndex.ExtraIgnoreGlobs = []string{"generated_*"}

	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesSeen != 1 {
		t.Errorf("files_seen = %d, want 1 (generated_api.py ignored)", report.FilesSeen)
	}
	if report.FilesIndexed != 1 {
		t.Errorf("files_indexed = %d, want 1", report.FilesIndexed)
	}
	syms, err := store.SymbolsForFile("generated_api.py")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 0 {
		t.Errorf("ignored file produced %d symbol memories, want 0", len(syms))
	}
}

// Fix 2: a parse failure must be recorded in report.Errors (Python format)
// while still degrading to the chunk fallback.
func TestIndexCodebaseParseErrorRecorded(t *testing.T) {
	// Register a language whose grammar has no DFA lexer tables, so
	// gotreesitter's Parse genuinely returns an error.
	specs["badlang"] = &LanguageSpec{
		Name:               "badlang",
		DefinitionKinds:    []string{"function_definition"},
		NameField:          "name",
		CallKinds:          []string{"call"},
		FallbackChunkLines: 40,
		Grammar:            func() *gotreesitter.Language { return &gotreesitter.Language{} },
	}
	extMap[".bad"] = "badlang"
	t.Cleanup(func() {
		delete(specs, "badlang")
		delete(extMap, ".bad")
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.bad"), []byte("not valid anything\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("len(errors) = %d, want 1; errors=%v", len(report.Errors), report.Errors)
	}
	if !strings.Contains(report.Errors[0], "broken.bad: parse failed (") ||
		!strings.HasSuffix(report.Errors[0], "); using chunk fallback") {
		t.Errorf("error format = %q, want \"<path>: parse failed (<err>); using chunk fallback\"", report.Errors[0])
	}
	// chunk fallback still produced a chunk memory
	syms, err := store.SymbolsForFile("broken.bad")
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 1 || syms[0].SymbolKind != "chunk" {
		t.Errorf("symbols = %+v, want one chunk symbol", syms)
	}
}

// Fix 3: call extraction must be AST-based — comments and string literals
// produce no calls, qualified calls keep only the tail segment, and calls
// are not deduplicated (BuildRefs dedups via seen).
func TestExtractCallsASTBased(t *testing.T) {
	t.Run("python", func(t *testing.T) {
		src := `def foo():
    pass


def bar():
    pass


def caller(obj):
    # commented_out()
    text = "str_call("
    obj.baz()
    foo()
    foo()
`
		syms, err := ExtractSymbols([]byte(src), "python", "m", "m.py", 40)
		if err != nil {
			t.Fatal(err)
		}
		var caller *RawSymbol
		for i := range syms {
			if syms[i].QualifiedName == "m.caller" {
				caller = &syms[i]
			}
		}
		if caller == nil {
			t.Fatal("m.caller not found")
		}
		want := []string{"baz", "foo", "foo"}
		if strings.Join(caller.Calls, ",") != strings.Join(want, ",") {
			t.Errorf("caller.Calls = %v, want %v", caller.Calls, want)
		}
		// BuildRefs dedups: exactly one caller->foo ref, none to bar.
		refs := BuildRefs(syms, "m.py")
		count := 0
		for _, r := range refs {
			if r.SrcSymbol == "m.caller" && r.DstSymbol == "m.foo" {
				count++
			}
			if r.DstSymbol == "m.bar" {
				t.Errorf("unexpected ref to m.bar: %+v", r)
			}
		}
		if count != 1 {
			t.Errorf("caller->foo refs = %d, want 1 (deduped)", count)
		}
	})

	t.Run("go", func(t *testing.T) {
		src := `package demo

func foo() {}

func caller() {
	// commented_out()
	s := "str_call("
	_ = s
	obj.baz()
	foo()
	foo()
}
`
		syms, err := ExtractSymbols([]byte(src), "go", "demo", "demo.go", 40)
		if err != nil {
			t.Fatal(err)
		}
		var caller *RawSymbol
		for i := range syms {
			if syms[i].QualifiedName == "demo.caller" {
				caller = &syms[i]
			}
		}
		if caller == nil {
			t.Fatal("demo.caller not found")
		}
		want := []string{"baz", "foo", "foo"}
		if strings.Join(caller.Calls, ",") != strings.Join(want, ",") {
			t.Errorf("caller.Calls = %v, want %v", caller.Calls, want)
		}
	})
}

// Fix 4: chunk fallback carries no calls (Python `_chunk_fallback` sets
// calls=[]), so BuildRefs produces no refs for chunked files.
func TestChunkFallbackHasNoCalls(t *testing.T) {
	syms := chunkFallback([]byte("alpha()\nbeta()\n"), "mod", "mod.rb", "ruby", 40)
	if len(syms) != 1 {
		t.Fatalf("chunks = %d, want 1", len(syms))
	}
	if syms[0].Calls != nil {
		t.Errorf("chunk calls = %v, want nil", syms[0].Calls)
	}

	// end-to-end: a chunked file whose first chunk mentions the second
	// chunk's name must not produce refs.
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("lines_2()\n")
	for i := 0; i < 44; i++ {
		sb.WriteString("nope\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "data.rb"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	report, err := IndexCodebase(dir, store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.RefsWritten != 0 {
		t.Errorf("refs_written = %d, want 0 (chunks carry no calls)", report.RefsWritten)
	}
}

// Fix 5: ElapsedMs must be fractional milliseconds (Python float), not
// integer-truncated. An empty-root index run finishes in well under 1ms, so
// integer truncation yields exactly 0.
func TestIndexCodebaseElapsedMsFractional(t *testing.T) {
	store, emb := newTestStore(t)
	cfg := config.ForTesting(t.TempDir())
	report, err := IndexCodebase(t.TempDir(), store, emb, cfg, "test", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.ElapsedMs <= 0 {
		t.Errorf("elapsed_ms = %v, want > 0 (fractional)", report.ElapsedMs)
	}
}
