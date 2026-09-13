package fulldict_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/storage"
	_ "github.com/ProjAnvil/LadyM/storage/fulldict" // registers the embedded dict
)

// writeFileDictFixture provisions a minimal on-disk zh dictionary in dir:
// a manifest naming the zh variant plus the two files the registry pins
// for it (s_1.txt / t_1.txt), each holding one custom compound word in
// gse's "word freq pos" line format.
func writeFileDictFixture(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"manifest.json": `{"variant":"zh"}`,
		"s_1.txt":       "内存屏障 10000 n\n",
		"t_1.txt":       "資料屏障 10000 n\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEmbeddedDict_CompleteFileDict_FileSourceTakesPrecedence: a downloaded
// file dictionary must win over the embedded one (upgrades without a
// rebuild), so a custom word known only to the file dict segments as a
// single token.
func TestEmbeddedDict_CompleteFileDict_FileSourceTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFileDictFixture(t, dir)
	storage.SetCJKDictDir(dir)

	st := storage.CJKDictStatusNow()
	if !st.Available || st.Source != "file" || st.Variant != storage.CJKDictZH {
		t.Fatalf("status = %+v, want available file/zh source over embedded", st)
	}
	if got := storage.Tokenize("内存屏障"); len(got) != 1 || got[0] != "内存屏障" {
		t.Fatalf("Tokenize = %v, want [内存屏障] from the file dict (embedded would split it)", got)
	}
}

// TestEmbeddedDict_CorruptFileDict_FallsBackToEmbedded: a file dictionary
// whose data cannot be loaded must not disable word segmentation — the
// loader falls through to the embedded dictionary instead.
func TestEmbeddedDict_CorruptFileDict_FallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	writeFileDictFixture(t, dir)
	// os.Stat still succeeds on a 0000-mode file, so the variant looks
	// installed, but gse cannot open it and the load fails.
	if err := os.Chmod(filepath.Join(dir, "s_1.txt"), 0o000); err != nil {
		t.Fatal(err)
	}
	storage.SetCJKDictDir(dir)

	st := storage.CJKDictStatusNow()
	if !st.Available || st.Source != "embedded" {
		t.Fatalf("status = %+v, want fallback to embedded when the file dict is unreadable", st)
	}
	if got := storage.Tokenize("数据库连接池耗尽"); len(got) == 0 {
		t.Fatal("Tokenize returned empty tokens after embedded fallback")
	}
}
