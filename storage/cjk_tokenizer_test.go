package storage

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHashingDictionarySnapshot(t *testing.T) {
	dir := installTestDict(t)
	setCJKTokenizerMode(t, CJKTokenizerDict)
	first := NewHashingEmbedding(256)
	before, err := first.Embed("数据库")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := first.Identity().Fingerprint()
	// Same variant, different dictionary contents must name a different space.
	if err := os.WriteFile(filepath.Join(dir, "s_1.txt"), []byte("数据 1024 n\n库 1024 n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	SetCJKDictDir(dir)
	next := NewHashingEmbedding(256)
	changed, err := next.Embed("数据库")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, changed) || fingerprint == next.Identity().Fingerprint() {
		t.Fatal("dictionary content change was not fenced by a new fingerprint")
	}
	after, err := first.Embed("数据库")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) || fingerprint != first.Identity().Fingerprint() {
		t.Fatal("dictionary reload mutated a live provider")
	}
}

func TestHashingDictionaryVariantIdentity(t *testing.T) {
	dir := installTestDict(t)
	setCJKTokenizerMode(t, CJKTokenizerDict)
	first := NewHashingEmbedding(256)
	before, err := first.Embed("テスト")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := first.Identity().Fingerprint()
	if err := os.WriteFile(filepath.Join(dir, "jp.txt"), []byte("テスト 2048 n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"variant":"jp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	SetCJKDictDir(dir)
	next := NewHashingEmbedding(256)
	changed, err := next.Embed("テスト")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(before, changed) || fingerprint == next.Identity().Fingerprint() {
		t.Fatal("dictionary variant change was not fenced by a new fingerprint")
	}
	after, err := first.Embed("テスト")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) || fingerprint != first.Identity().Fingerprint() {
		t.Fatal("dictionary variant switch mutated a live provider")
	}
}

// setCJKTokenizerMode redirects the CJK tokenizer mode for one test.
func setCJKTokenizerMode(t *testing.T, mode string) {
	t.Helper()
	prev := CJKTokenizerMode()
	SetCJKTokenizerMode(mode)
	t.Cleanup(func() { SetCJKTokenizerMode(prev) })
}

// TestCJKTokenizerDefaultIsDict pins the default: existing databases keep the
// vectors they were written with, so the mode must not change silently.
func TestCJKTokenizerDefaultIsDict(t *testing.T) {
	if got := CJKTokenizerMode(); got != CJKTokenizerDict {
		t.Errorf("default CJK tokenizer mode = %q, want %q", got, CJKTokenizerDict)
	}
}

// TestCJKCharsModeSkipsDictionary is the whole point of the mode: in chars
// mode a Han run must never trigger the dictionary load. That load is a
// one-off ~520ms cedar build paid per process, which is what makes a Chinese
// prompt cost ~450ms per hook invocation instead of ~35ms.
func TestCJKCharsModeSkipsDictionary(t *testing.T) {
	installTestDict(t)
	setCJKTokenizerMode(t, CJKTokenizerChars)
	resetCJK(t)

	out := Tokenize("数据库")
	cjkMu.Lock()
	loaded := cjkSeg != nil
	cjkMu.Unlock()
	if loaded {
		t.Error("chars mode loaded the CJK dictionary; it must not")
	}
	if len(out) != 3 {
		t.Errorf("chars mode tokens = %v, want one token per rune", out)
	}
}

// TestCJKDictModeSegmentsWords pins the other side: dict mode still asks gse,
// so a dictionary word survives as one token.
func TestCJKDictModeSegmentsWords(t *testing.T) {
	installTestDict(t)
	setCJKTokenizerMode(t, CJKTokenizerDict)
	resetCJK(t)

	out := Tokenize("数据库")
	cjkMu.Lock()
	loaded := cjkSeg != nil
	cjkMu.Unlock()
	if !loaded {
		t.Fatal("dict mode did not load the CJK dictionary")
	}
	joined := ""
	for _, tok := range out {
		joined += tok
	}
	if joined != "数据库" {
		t.Fatalf("dict mode tokens = %v, want the run preserved", out)
	}
	if len(out) >= 3 {
		t.Errorf("dict mode tokens = %v, want fewer tokens than one per rune", out)
	}
}

// TestCJKTokenizerModeRejectsUnknown guards the config surface: a typo must
// fall back to the default rather than silently disabling word segmentation.
func TestCJKTokenizerModeRejectsUnknown(t *testing.T) {
	prev := CJKTokenizerMode()
	defer SetCJKTokenizerMode(prev)
	SetCJKTokenizerMode("bogus")
	if got := CJKTokenizerMode(); got != CJKTokenizerDict {
		t.Errorf("mode after unknown value = %q, want %q", got, CJKTokenizerDict)
	}
}
