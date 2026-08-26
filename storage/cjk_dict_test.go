package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// installTestDict writes a mini zh dictionary (simplified + traditional,
// gse "word freq pos" lines) into a temp dir and points the dict resolver
// at it, keeping the suite hermetic regardless of what exists under the
// real ~/.ladyM/dict. No manifest: exercises the registry scan fallback.
func installTestDict(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	simplified := strings.Join([]string{
		"用户 1024 n",
		"登录 512 v",
		"失败 256 v",
		"数据库 1024 n",
		"连接池 256 n",
		"耗尽 64 v",
		"你好 2048 r",
		"世界 2048 n",
	}, "\n") + "\n"
	traditional := "資料庫 1024 n\n連線 512 v\n逾時 128 n\n"
	for name, body := range map[string]string{
		"s_1.txt": simplified,
		"t_1.txt": traditional,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setCJKDictDir(t, dir)
	return dir
}

// setCJKDictDir redirects the dict directory for one test and resets the
// cached segmenter so the next Tokenize reloads from the new location.
func setCJKDictDir(t *testing.T, dir string) {
	t.Helper()
	prev := cjkDictDirFn
	cjkDictDirFn = func() string { return dir }
	resetCJK(t)
	t.Cleanup(func() {
		cjkDictDirFn = prev
		resetCJK(t)
	})
}

func resetCJK(t *testing.T) {
	t.Helper()
	cjkMu.Lock()
	cjkLoaded = false
	cjkSeg = nil
	cjkSrc = ""
	cjkVariant = ""
	cjkLastProbe = time.Time{}
	cjkHadManifest = false
	cjkManifestMod = time.Time{}
	cjkMu.Unlock()
}

// TestTokenizeCJKFallbackWithoutDict covers the no-dictionary path: Han
// runs degrade to per-character tokens instead of an empty token set. The
// assertion is rune-coverage so it holds under both build tags (default
// builds have no dict; fulldict builds fall back to the embedded one).
func TestTokenizeCJKFallbackWithoutDict(t *testing.T) {
	setCJKDictDir(t, t.TempDir()) // empty dir: no file dict
	if st := CJKDictStatusNow(); st.Source == "file" {
		t.Fatalf("source = %q, want any non-file source", st.Source)
	}
	got := Tokenize("用户登录失败")
	if len(got) == 0 {
		t.Fatal("Tokenize without dict returned an empty token set")
	}
	joined := strings.Join(got, "")
	for _, r := range "用户登录失败" {
		if !strings.ContainsRune(joined, r) {
			t.Errorf("fallback tokens %v lost rune %q", got, r)
		}
	}
}

// fixtureRegistry builds a test registry whose zh and jp variants are tiny
// fixture dictionaries served by a local httptest mirror.
func fixtureRegistry(t *testing.T, zhS, zhT, jp string) map[CJKDictName]cjkDictVariant {
	t.Helper()
	spec := func(name, relPath, body string) cjkDictFileSpec {
		sum := sha256.Sum256([]byte(body))
		return cjkDictFileSpec{
			Name:    name,
			RelPath: relPath,
			SHA256:  hex.EncodeToString(sum[:]),
			Bytes:   int64(len(body)),
		}
	}
	mux := http.NewServeMux()
	serve := func(relPath, body string) {
		mux.HandleFunc("/"+relPath, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	}
	serve("data/dict/zh/s_1.txt", zhS)
	serve("data/dict/zh/t_1.txt", zhT)
	serve("data/dict/jp/dict.txt", jp)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	prevReg, prevMirror := cjkRegistryFn, cjkDictMirrorFn
	registry := map[CJKDictName]cjkDictVariant{
		CJKDictZH: {
			Name: CJKDictZH, Desc: "zh fixture", Scripts: []string{"Han"},
			Files: []cjkDictFileSpec{
				spec("s_1.txt", "data/dict/zh/s_1.txt", zhS),
				spec("t_1.txt", "data/dict/zh/t_1.txt", zhT),
			},
		},
		CJKDictJP: {
			Name: CJKDictJP, Desc: "jp fixture", Scripts: []string{"Han", "Kana"},
			Files: []cjkDictFileSpec{
				spec("jp.txt", "data/dict/jp/dict.txt", jp),
			},
		},
	}
	cjkRegistryFn = func() map[CJKDictName]cjkDictVariant { return registry }
	cjkDictMirrorFn = func() []string { return []string{srv.URL + "/"} }
	resetCJK(t)
	t.Cleanup(func() {
		cjkRegistryFn, cjkDictMirrorFn = prevReg, prevMirror
		resetCJK(t)
	})
	return registry
}

func TestDownloadCJKDict(t *testing.T) {
	fixtureRegistry(t,
		"用户 1024 n\n登录 512 v\n失败 256 v\n",
		"資料庫 64 n\n",
		"テスト 2048 n\n",
	)

	dir := t.TempDir()
	setCJKDictDir(t, dir)

	st, err := DownloadCJKDict()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Source != "file" || st.Variant != CJKDictZH {
		t.Fatalf("status after download = %+v, want file/zh source", st)
	}
	for _, name := range []string{"s_1.txt", "t_1.txt", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s in dict dir: %v", name, err)
		}
	}
	// Shared volumes are read by other instances/UIDs: files must be
	// world-readable (atomicWrite's 0644).
	if fi, err := os.Stat(filepath.Join(dir, "s_1.txt")); err == nil && fi.Mode().Perm() != 0o644 {
		t.Errorf("dict file mode = %v, want 0644", fi.Mode().Perm())
	}
	var manifest struct {
		Version string            `json:"version"`
		Variant CJKDictName       `json:"variant"`
		Files   []cjkDictFileSpec `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != cjkDictVersion || manifest.Variant != CJKDictZH || len(manifest.Files) != 2 {
		t.Errorf("manifest = %+v", manifest)
	}

	// The downloaded dict is live immediately.
	if got := Tokenize("用户登录失败"); !reflect.DeepEqual(got, []string{"用户", "登录", "失败"}) {
		t.Errorf("Tokenize after download = %v, want [用户 登录 失败]", got)
	}

	// Removal drops back to fallback tokenization and cleans the files.
	if err := RemoveCJKDict(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"s_1.txt", "t_1.txt", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after remove", name)
		}
	}
	st = CJKDictStatusNow()
	if st.Source == "file" {
		t.Errorf("source after remove = file, want non-file")
	}
}

// TestDownloadCJKDictJPAndVariantSwitch: the jp variant routes kana runs
// through the segmenter (zh variants keep them per-character), and
// switching variants cleans up the previous variant's files.
func TestDownloadCJKDictJPAndVariantSwitch(t *testing.T) {
	fixtureRegistry(t,
		"用户 1024 n\n",
		"資料庫 64 n\n",
		"テスト 2048 n\nログイン 1024 n\n",
	)

	dir := t.TempDir()
	setCJKDictDir(t, dir)

	// Sanity under zh: kana stays per-character.
	if _, err := DownloadCJKDictTo(CJKDictZH, dir, ""); err != nil {
		t.Fatal(err)
	}
	if got := Tokenize("テスト"); !reflect.DeepEqual(got, []string{"テ", "ス", "ト"}) {
		t.Fatalf("zh variant kana tokens = %v, want per-character", got)
	}

	st, err := DownloadCJKDictTo(CJKDictJP, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != "file" || st.Variant != CJKDictJP {
		t.Fatalf("status after jp download = %+v", st)
	}
	// zh files were cleaned up by the switch.
	for _, name := range []string{"s_1.txt", "t_1.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after variant switch to jp", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "jp.txt")); err != nil {
		t.Errorf("jp.txt missing: %v", err)
	}
	// Kana now segments by dictionary word.
	if got := Tokenize("テスト"); !reflect.DeepEqual(got, []string{"テスト"}) {
		t.Errorf("jp variant kana tokens = %v, want [テスト]", got)
	}
}

func TestDownloadCJKDictUnknownVariant(t *testing.T) {
	fixtureRegistry(t, "x 1 n\n", "y 1 n\n", "z 1 n\n")
	setCJKDictDir(t, t.TempDir())
	_, err := DownloadCJKDictTo(CJKDictName("klingon"), "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown dictionary") || !strings.Contains(err.Error(), "zh") {
		t.Fatalf("err = %v, want unknown-dictionary error listing variants", err)
	}
	// Empty name selects the default zh variant.
	if _, err := DownloadCJKDictTo("", t.TempDir(), ""); err != nil {
		t.Fatalf("empty dict name should default to zh: %v", err)
	}
}

// TestDownloadCJKDictChecksumMismatch pins the wrong sha256 so the mirror
// "serves" tampered content; nothing may reach the dict dir.
func TestDownloadCJKDictChecksumMismatch(t *testing.T) {
	reg := fixtureRegistry(t, "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n")
	tampered := reg[CJKDictZH]
	tampered.Files[0].SHA256 = strings.Repeat("0", 64)
	fixed := map[CJKDictName]cjkDictVariant{CJKDictZH: tampered}
	prev := cjkRegistryFn
	cjkRegistryFn = func() map[CJKDictName]cjkDictVariant { return fixed }
	t.Cleanup(func() { cjkRegistryFn = prev })

	dir := t.TempDir()
	setCJKDictDir(t, dir)
	_, err := DownloadCJKDictTo(CJKDictZH, dir, "")
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err = %v, want sha256 mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "s_1.txt")); !os.IsNotExist(statErr) {
		t.Error("tampered file must not be written into place")
	}
}

func TestDownloadCJKDictUnreachableMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // nothing listens anymore

	dir := t.TempDir()
	prevReg, prevMirror := cjkRegistryFn, cjkDictMirrorFn
	cjkRegistryFn = func() map[CJKDictName]cjkDictVariant { return cjkDictRegistry }
	cjkDictMirrorFn = func() []string { return []string{srv.URL + "/"} }
	resetCJK(t)
	t.Cleanup(func() {
		cjkRegistryFn, cjkDictMirrorFn = prevReg, prevMirror
		resetCJK(t)
	})
	setCJKDictDir(t, dir)

	_, err := DownloadCJKDict()
	if err == nil || !strings.Contains(err.Error(), "download") {
		t.Fatalf("err = %v, want download failure", err)
	}
	if st := CJKDictStatusNow(); st.Source == "file" {
		t.Error("source must not become file after a failed download")
	}
}

func TestDownloadCJKDictMirrorFallback(t *testing.T) {
	// First mirror always 500s; the second one (the fixture server) serves
	// valid content.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	zhS, zhT, jp := "用户 1024 n\n", "資料庫 64 n\n", "テスト 1 n\n"
	reg := fixtureRegistry(t, zhS, zhT, jp)
	good := cjkDictMirrorFn()[0]
	prevReg, prevMirror := cjkRegistryFn, cjkDictMirrorFn
	cjkRegistryFn = func() map[CJKDictName]cjkDictVariant { return reg }
	cjkDictMirrorFn = func() []string { return []string{bad.URL + "/", good} }
	resetCJK(t)
	t.Cleanup(func() {
		cjkRegistryFn, cjkDictMirrorFn = prevReg, prevMirror
		resetCJK(t)
	})

	dir := t.TempDir()
	setCJKDictDir(t, dir)
	st, err := DownloadCJKDict()
	if err != nil {
		t.Fatal(err)
	}
	if st.Source != "file" {
		t.Fatalf("status = %+v, want file source via mirror fallback", st)
	}
}

// TestSharedVolumeDictPickup: when the dict dir is a shared volume and
// another instance provisions (or switches) the dictionary, local
// tokenization picks it up within one probe interval — the basis of the
// microservice shared-volume deployment story.
func TestSharedVolumeDictPickup(t *testing.T) {
	prev := cjkProbeInterval
	cjkProbeInterval = 0 // probe on every call
	t.Cleanup(func() { cjkProbeInterval = prev })

	dir := t.TempDir()
	setCJKDictDir(t, dir)

	// No file dict yet: per-character fallback (default builds) or the
	// embedded dict (fulldict builds) — either way, not the shared volume.
	if st := CJKDictStatusNow(); st.Source == "file" {
		t.Fatal("file dict active before the volume is provisioned")
	}

	// Another instance downloads zh: files + manifest appear in the volume.
	writeShared := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeShared("s_1.txt", "用户 1024 n\n登录 512 v\n失败 256 v\n")
	writeShared("t_1.txt", "資料庫 64 n\n")
	manifest := func(variant string) string {
		b, _ := json.Marshal(map[string]any{"version": cjkDictVersion, "variant": variant})
		return string(b)
	}
	writeShared("manifest.json", manifest("zh"))
	if got := Tokenize("用户登录失败"); !reflect.DeepEqual(got, []string{"用户", "登录", "失败"}) {
		t.Fatalf("tokens after shared-volume provision = %v, want word segmentation", got)
	}

	// Another instance switches the variant to jp (manifest replaced):
	// local instance follows the switch without a restart.
	os.Remove(filepath.Join(dir, "s_1.txt"))
	os.Remove(filepath.Join(dir, "t_1.txt"))
	writeShared("jp.txt", "テスト 2048 n\n")
	// Force a manifest mtime change even on coarse-mtime filesystems.
	future := time.Now().Add(2 * time.Second)
	writeShared("manifest.json", manifest("jp"))
	if err := os.Chtimes(filepath.Join(dir, "manifest.json"), future, future); err != nil {
		t.Fatal(err)
	}

	if got := Tokenize("テスト"); !reflect.DeepEqual(got, []string{"テスト"}) {
		t.Fatalf("tokens after shared variant switch = %v, want [テスト]", got)
	}
	if st := CJKDictStatusNow(); st.Variant != CJKDictJP || st.Source != "file" {
		t.Errorf("status = %+v, want file/jp after switch", st)
	}
}

// TestCJKDictVariantsListing: the registry enumerates in canonical order
// with sizes.
func TestCJKDictVariantsListing(t *testing.T) {
	variants := CJKDictVariants()
	if len(variants) != 4 {
		t.Fatalf("variants = %+v, want 4 entries", variants)
	}
	if variants[0].Name != CJKDictZH || variants[3].Name != CJKDictJP {
		t.Errorf("order = %v,%v want zh first, jp last", variants[0].Name, variants[3].Name)
	}
	if variants[3].Bytes < 20_000_000 {
		t.Errorf("jp variant bytes = %d, want the real ~23.7MB dict", variants[3].Bytes)
	}
}

// TestTokenizeNeverDownloads locks the no-auto-download contract: LadyM
// never fetches the dictionary on its own. Tokenization (including probe
// cycles) must work fully offline and never touch a mirror — downloads
// happen only via the admin-triggered API / console.
func TestTokenizeNeverDownloads(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	prevInterval := cjkProbeInterval
	cjkProbeInterval = 0 // force a probe cycle on every call
	prevReg, prevMirror := cjkRegistryFn, cjkDictMirrorFn
	cjkRegistryFn = func() map[CJKDictName]cjkDictVariant { return cjkDictRegistry }
	cjkDictMirrorFn = func() []string { return []string{srv.URL + "/"} }
	resetCJK(t)
	t.Cleanup(func() {
		cjkProbeInterval = prevInterval
		cjkRegistryFn, cjkDictMirrorFn = prevReg, prevMirror
		resetCJK(t)
	})
	setCJKDictDir(t, t.TempDir()) // empty: no local dict anywhere

	// Tokenize repeatedly so lazy load AND probe cycles both run.
	for i := 0; i < 3; i++ {
		if got := Tokenize("数据库连接池耗尽"); len(got) == 0 {
			t.Fatal("offline tokenization returned empty tokens")
		}
	}
	if hits != 0 {
		t.Fatalf("tokenization attempted %d network download(s); LadyM must never auto-download", hits)
	}
}
