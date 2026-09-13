package storage

// Global-state entry points of cjk_dict.go: SetEmbeddedCJKDict,
// SetCJKDictDir, the default dir/mirror resolvers, cjkSegmenterFor's
// unknown-variant branch, and runScript classification. Every test that
// mutates the package-level CJK state saves and restores it.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-ego/gse"
)

// TestCJKDictDefaultResolvers pins the default dir/mirror resolver bodies
// (tests usually swap them out, so the defaults never execute otherwise).
func TestCJKDictDefaultResolvers(t *testing.T) {
	if dir := cjkDictDirFn(); !strings.HasSuffix(dir, filepath.Join(".ladyM", "dict")) {
		t.Errorf("default dict dir = %q, want <secrets dir>/dict", dir)
	}
	mirrors := cjkDictMirrorFn()
	if len(mirrors) != 2 || !strings.Contains(mirrors[0], "jsdelivr") || !strings.Contains(mirrors[1], "githubusercontent") {
		t.Errorf("default mirrors = %v, want jsDelivr + GitHub raw", mirrors)
	}
}

// TestSetEmbeddedCJKDictProviderLifecycle: a registered provider wins over
// the build-tag embedded dict and yields source "embedded"; a nil-returning
// provider and unregistering both fall through to the next source.
func TestSetEmbeddedCJKDictProviderLifecycle(t *testing.T) {
	setCJKDictDir(t, t.TempDir()) // no file dict on the "shared volume"
	cjkMu.Lock()
	prevProvider := cjkEmbeddedProvider
	cjkMu.Unlock()
	t.Cleanup(func() {
		SetEmbeddedCJKDict(prevProvider)
		resetCJK(t)
	})

	SetEmbeddedCJKDict(func() *gse.Segmenter { return &gse.Segmenter{} })
	st := CJKDictStatusNow()
	if !st.Available || st.Source != "embedded" || st.Variant != CJKDictZH {
		t.Errorf("status with provider = %+v, want available embedded/zh", st)
	}

	// A provider returning nil is ignored; the next source takes over
	// (none in default builds, the build-tag dict in fulldict builds).
	SetEmbeddedCJKDict(func() *gse.Segmenter { return nil })
	if st := CJKDictStatusNow(); st.Source == "file" {
		t.Errorf("status with nil-returning provider = %+v, want non-file", st)
	}

	SetEmbeddedCJKDict(nil)
	if st := CJKDictStatusNow(); st.Source == "file" {
		t.Errorf("status after unregister = %+v, want non-file", st)
	}
}

// TestSetEmbeddedCJKDictFileDictWins: a file dictionary on disk keeps its
// precedence over the registered embedded provider.
func TestSetEmbeddedCJKDictFileDictWins(t *testing.T) {
	dir := installTestDict(t)
	cjkMu.Lock()
	prevProvider := cjkEmbeddedProvider
	cjkMu.Unlock()
	t.Cleanup(func() {
		SetEmbeddedCJKDict(prevProvider)
		resetCJK(t)
	})

	SetEmbeddedCJKDict(func() *gse.Segmenter { return &gse.Segmenter{} })
	st := CJKDictStatusNow()
	if st.Source != "file" || st.Dir != dir {
		t.Errorf("status = %+v, want the file dict to keep precedence", st)
	}
}

// TestSetCJKDictDirReloadsImmediately: the override swaps the resolver and
// reloads the segmenter from the new directory in one call.
func TestSetCJKDictDirReloadsImmediately(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"s_1.txt": "用户 1024 n\n登录 512 v\n失败 256 v\n",
		"t_1.txt": "資料庫 64 n\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prevFn := cjkDictDirFn
	t.Cleanup(func() {
		cjkDictDirFn = prevFn
		resetCJK(t)
	})

	SetCJKDictDir(dir)
	st := CJKDictStatusNow()
	if st.Source != "file" || st.Variant != CJKDictZH || st.Dir != dir {
		t.Errorf("status after SetCJKDictDir = %+v, want file/zh in the new dir", st)
	}
	if got := Tokenize("用户登录失败"); !reflect.DeepEqual(got, []string{"用户", "登录", "失败"}) {
		t.Errorf("Tokenize after SetCJKDictDir = %v, want [用户 登录 失败]", got)
	}
}

// TestCJKSegmenterForUnknownActiveVariant: a segmenter left active under a
// variant the (swapped) registry no longer knows is treated as absent —
// callers fall back to per-character tokens.
func TestCJKSegmenterForUnknownActiveVariant(t *testing.T) {
	setCJKDictDir(t, t.TempDir())
	cjkMu.Lock()
	cjkSeg = &gse.Segmenter{}
	cjkVariant = CJKDictName("klingon")
	cjkLoaded = true
	cjkLastProbe = time.Now() // suppress the shared-volume reprobe
	cjkMu.Unlock()

	if got := cjkSegmenterFor("Han"); got != nil {
		t.Errorf("cjkSegmenterFor with unknown active variant = %v, want nil", got)
	}
}

// TestRunScriptClassification: script runs are classified by their first
// rune; non-CJK runs (and the empty string) yield "".
func TestRunScriptClassification(t *testing.T) {
	cases := []struct {
		name string
		run  string
		want string
	}{
		{"han ideograph", "中文字", "Han"},
		{"hiragana", "あいう", "Kana"},
		{"katakana", "アイウ", "Kana"},
		{"hangul", "한글", "Hangul"},
		{"latin", "abc", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runScript(tc.run); got != tc.want {
				t.Errorf("runScript(%q) = %q, want %q", tc.run, got, tc.want)
			}
		})
	}
}
