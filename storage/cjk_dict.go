// CJK dictionary management for gse-based tokenization.
//
// LadyM NEVER downloads the dictionary on its own: startup, tokenization,
// and background workers only ever read the local directory or the
// embedded dictionary. Downloads are strictly user-triggered — the admin
// console (Settings → Memory) or POST /api/cjk_dict/download — and the
// exported Download* functions exist for that handler and for embedding
// applications that explicitly opt in.
//
// Dictionary sources, in precedence order:
//
//  1. the file dict downloaded to ~/.ladyM/dict (highest — upgrades and
//     variant switches without a rebuild; see DownloadCJKDictTo)
//  2. the dict embedded via the storage/fulldict side-effect package or a
//     `-tags fulldict` build (zero-download CJK support, ~+31MB binary)
//  3. none: covered-script runs degrade to per-character tokens, which
//     still yields non-empty token sets and working similarity
//
// Downloadable dictionaries are a registry of named variants (zh, zh_s,
// zh_t, jp) — callers pick one and may override the target directory.
//
// The default build carries no dictionary data: gse's embedded dicts are
// only linked in when the fulldict code paths are compiled or imported.

package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ProjAnvil/LadyM/secrets"
	"github.com/go-ego/gse"
)

// CJKDictName identifies a downloadable dictionary variant.
type CJKDictName string

const (
	// CJKDictZH is Chinese simplified + traditional (the default).
	CJKDictZH CJKDictName = "zh"
	// CJKDictZHS is Chinese simplified only (~40% smaller download).
	CJKDictZHS CJKDictName = "zh_s"
	// CJKDictZHT is Chinese traditional only.
	CJKDictZHT CJKDictName = "zh_t"
	// CJKDictJP is Japanese (kanji + kana; ~23.7MB).
	CJKDictJP CJKDictName = "jp"
)

// cjkDictVersion tags which gse release the pinned files come from; it is
// written into the manifest so a future bump can migrate cleanly.
const cjkDictVersion = "gse-v1.0.2"

// cjkDictFileSpec pins one downloadable dictionary file. RelPath is its
// location under every mirror (the gse repo layout); Name is the local
// filename inside the dict dir.
type cjkDictFileSpec struct {
	Name    string `json:"name"`
	RelPath string `json:"rel_path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
}

// cjkDictVariant is one registry entry: which files to download and which
// Unicode scripts its dictionary covers (drives segmentCJK routing).
type cjkDictVariant struct {
	Name    CJKDictName
	Desc    string
	Scripts []string // "Han", "Kana", "Hangul"
	Files   []cjkDictFileSpec
}

// CJKVariantInfo is the console/API view of one registry entry.
type CJKVariantInfo struct {
	Name  CJKDictName `json:"name"`
	Desc  string      `json:"desc"`
	Bytes int64       `json:"bytes"`
}

// cjkDictOrder is the canonical registry iteration order; zh precedes its
// subset variants so the scan fallback in detectInstalledVariant prefers
// the fuller dictionary when both are fully present on disk.
var cjkDictOrder = []CJKDictName{CJKDictZH, CJKDictZHS, CJKDictZHT, CJKDictJP}

// cjkDictRegistry is pinned to go-ego/gse v1.0.2. Content is sha256-verified
// at download time, so mirrors cannot silently serve different data.
//
// Note: jsDelivr refuses files over 20MB (403), so the jp dictionary always
// falls through to the GitHub raw mirror — the per-file mirror loop handles
// that transparently.
var cjkDictRegistry = map[CJKDictName]cjkDictVariant{
	CJKDictZH: {
		Name:    CJKDictZH,
		Desc:    "Chinese, simplified + traditional (8.2MB)",
		Scripts: []string{"Han"},
		Files: []cjkDictFileSpec{
			{Name: "s_1.txt", RelPath: "data/dict/zh/s_1.txt", SHA256: "2b3063ec552327520bee3c0c5819d6e131ab3db50a60b94641ec90f611c24bcd", Bytes: 5117886},
			{Name: "t_1.txt", RelPath: "data/dict/zh/t_1.txt", SHA256: "2c84cef353d2daac62cc62bbeabab6b6a8866cfee8f9f88901e00ed66ed208c6", Bytes: 3525862},
		},
	},
	CJKDictZHS: {
		Name:    CJKDictZHS,
		Desc:    "Chinese, simplified only (4.9MB)",
		Scripts: []string{"Han"},
		Files: []cjkDictFileSpec{
			{Name: "s_1.txt", RelPath: "data/dict/zh/s_1.txt", SHA256: "2b3063ec552327520bee3c0c5819d6e131ab3db50a60b94641ec90f611c24bcd", Bytes: 5117886},
		},
	},
	CJKDictZHT: {
		Name:    CJKDictZHT,
		Desc:    "Chinese, traditional only (3.4MB)",
		Scripts: []string{"Han"},
		Files: []cjkDictFileSpec{
			{Name: "t_1.txt", RelPath: "data/dict/zh/t_1.txt", SHA256: "2c84cef353d2daac62cc62bbeabab6b6a8866cfee8f9f88901e00ed66ed208c6", Bytes: 3525862},
		},
	},
	CJKDictJP: {
		Name:    CJKDictJP,
		Desc:    "Japanese, kanji + kana (22.6MB)",
		Scripts: []string{"Han", "Kana"},
		Files: []cjkDictFileSpec{
			{Name: "jp.txt", RelPath: "data/dict/jp/dict.txt", SHA256: "b7de28abfda94ed009f1e7fc67333393a449825ca653a7eb101fee61dfeda4ed", Bytes: 23713176},
		},
	},
}

// cjkDictMirrors are tried in order per file. jsDelivr first because it is
// reliably reachable from mainland China; GitHub raw as fallback.
var cjkDictMirrors = []string{
	"https://cdn.jsdelivr.net/gh/go-ego/gse@v1.0.2/",
	"https://raw.githubusercontent.com/go-ego/gse/v1.0.2/",
}

// cjkDictDirFn resolves the file-dict directory; SetCJKDictDir overrides,
// tests swap it directly. Kept as a func so both share one mechanism.
var cjkDictDirFn = func() string {
	return filepath.Join(secrets.Dir(), "dict")
}

// cjkRegistryFn / cjkDictMirrorFn let tests pin fixtures and local
// httptest servers instead of the real downloads.
var (
	cjkRegistryFn   = func() map[CJKDictName]cjkDictVariant { return cjkDictRegistry }
	cjkDictMirrorFn = func() []string { return cjkDictMirrors }
)

var (
	cjkMu      sync.Mutex // guards the fields below
	cjkSeg     *gse.Segmenter
	cjkSrc     string      // "file" | "embedded" | "none"
	cjkVariant CJKDictName // active variant, "" when none
	cjkLoaded  bool
	// Shared-volume bookkeeping (guarded by cjkMu): when the dict dir is a
	// volume shared across microservice instances, another instance may
	// provision or switch the dictionary under us. Tokenization and status
	// reads re-probe the manifest at most every cjkProbeInterval.
	cjkLastProbe   time.Time
	cjkHadManifest bool
	cjkManifestMod time.Time
	// cjkEmbeddedProvider is registered by the storage/fulldict side-effect
	// package; guarded by cjkMu.
	cjkEmbeddedProvider func() *gse.Segmenter
)

// cjkProbeInterval bounds how often the dict dir is re-checked for a
// dictionary another instance provisioned on a shared volume. Var (not
// const) so tests can zero it.
var cjkProbeInterval = 30 * time.Second

// SetEmbeddedCJKDict registers a segmenter provider used when no file
// dictionary is present (pass nil to unregister). It exists for the
// storage/fulldict side-effect package — importing that package is the
// import-path equivalent of building with -tags fulldict, letting library
// consumers embed the dictionary without touching their build scripts.
// The active segmenter is reloaded immediately.
func SetEmbeddedCJKDict(fn func() *gse.Segmenter) {
	cjkMu.Lock()
	defer cjkMu.Unlock()
	cjkEmbeddedProvider = fn
	reloadCJKLocked()
}

// SetCJKDictDir overrides where the downloadable dictionary lives (default
// ~/.ladyM/dict). Intended for startup-time configuration by tools and
// tests; the active segmenter is reloaded immediately.
func SetCJKDictDir(dir string) {
	cjkMu.Lock()
	defer cjkMu.Unlock()
	cjkDictDirFn = func() string { return dir }
	reloadCJKLocked()
}

// reloadCJKLocked re-resolves the dictionary source. Callers hold cjkMu.
func reloadCJKLocked() {
	resolveCJKLocked()
}

// resolveCJKLocked loads the dictionary state and records the shared-volume
// probe bookkeeping. Callers hold cjkMu.
func resolveCJKLocked() {
	cjkSeg, cjkSrc, cjkVariant = loadCJKDict()
	cjkLoaded = true
	cjkLastProbe = time.Now()
	fi, err := os.Stat(filepath.Join(cjkDictDirFn(), "manifest.json"))
	cjkHadManifest = err == nil
	if err == nil {
		cjkManifestMod = fi.ModTime()
	}
}

// maybeReprobeLocked lazily resolves on first use and, at most every
// cjkProbeInterval, re-checks the dict dir so dictionaries provisioned or
// switched by OTHER instances on a shared volume are picked up: none →
// file transitions, and manifest changes (variant switch / upgrade) while
// a file dict is active. Callers hold cjkMu.
func maybeReprobeLocked() {
	if !cjkLoaded {
		resolveCJKLocked()
		return
	}
	if time.Since(cjkLastProbe) < cjkProbeInterval {
		return
	}
	cjkLastProbe = time.Now()
	dir := cjkDictDirFn()
	fi, err := os.Stat(filepath.Join(dir, "manifest.json"))
	manifestExists := err == nil
	if cjkSrc == "file" {
		if manifestExists != cjkHadManifest || (manifestExists && !fi.ModTime().Equal(cjkManifestMod)) {
			resolveCJKLocked()
		}
		return
	}
	// embedded/none: a dictionary appearing on the shared volume wins by
	// precedence.
	if detectInstalledVariant(dir) != nil {
		resolveCJKLocked()
	}
}

// loadCJKDict resolves the best available dictionary source and variant.
// The provider registered via SetEmbeddedCJKDict wins over the build-tag
// variant; both embed the same zh dictionary.
func loadCJKDict() (*gse.Segmenter, string, CJKDictName) {
	dir := cjkDictDirFn()
	if v := detectInstalledVariant(dir); v != nil {
		var seg gse.Segmenter
		seg.SkipLog = true
		paths := make([]string, 0, len(v.Files))
		for _, spec := range v.Files {
			paths = append(paths, filepath.Join(dir, spec.Name))
		}
		if err := seg.LoadDict(paths...); err == nil {
			return &seg, "file", v.Name
		}
		// A corrupt file dict falls through to the embedded dict (if any)
		// rather than disabling word segmentation entirely.
	}
	if cjkEmbeddedProvider != nil {
		if seg := cjkEmbeddedProvider(); seg != nil {
			return seg, "embedded", CJKDictZH
		}
	}
	if seg := newEmbeddedCJKSegmenter(); seg != nil {
		return seg, "embedded", CJKDictZH
	}
	return nil, "none", ""
}

// cjkSegmenterFor returns the active segmenter when the active variant
// covers the given script ("Han", "Kana", "Hangul"), else nil (the caller
// falls back to per-character tokens).
func cjkSegmenterFor(script string) *gse.Segmenter {
	cjkMu.Lock()
	defer cjkMu.Unlock()
	maybeReprobeLocked()
	if cjkSeg == nil || script == "" || cjkVariant == "" {
		return nil
	}
	v, ok := cjkRegistryFn()[cjkVariant]
	if !ok {
		return nil
	}
	for _, s := range v.Scripts {
		if s == script {
			return cjkSeg
		}
	}
	return nil
}

// detectInstalledVariant returns the fully-present variant in dir: the
// manifest's variant when its files all exist, else the first registry
// entry (in cjkDictOrder) whose files are all present.
func detectInstalledVariant(dir string) *cjkDictVariant {
	reg := cjkRegistryFn()
	var m struct {
		Variant CJKDictName `json:"variant"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := reg[m.Variant]; ok && variantFilesPresent(dir, &v) {
				return &v
			}
		}
	}
	for _, name := range cjkDictOrder {
		if v, ok := reg[name]; ok && variantFilesPresent(dir, &v) {
			return &v
		}
	}
	return nil
}

func variantFilesPresent(dir string, v *cjkDictVariant) bool {
	for _, spec := range v.Files {
		if _, err := os.Stat(filepath.Join(dir, spec.Name)); err != nil {
			return false
		}
	}
	return true
}

// CJKDictStatus is the console/API view of the dictionary state.
type CJKDictStatus struct {
	Available bool        `json:"available"`
	Source    string      `json:"source"`  // file | embedded | none
	Variant   CJKDictName `json:"variant"` // active variant, "" when none
	Dir       string      `json:"dir"`
	Version   string      `json:"version"`
	Bytes     int64       `json:"bytes"` // on-disk size when source == file
}

// CJKDictStatusNow reports the active dictionary state, triggering the
// lazy load if tokenization has not run yet.
func CJKDictStatusNow() CJKDictStatus {
	cjkMu.Lock()
	defer cjkMu.Unlock()
	maybeReprobeLocked()
	st := CJKDictStatus{
		Source:  cjkSrc,
		Variant: cjkVariant,
		Dir:     cjkDictDirFn(),
		Version: cjkDictVersion,
	}
	switch cjkSrc {
	case "file":
		st.Available = true
		if v, ok := cjkRegistryFn()[cjkVariant]; ok {
			for _, f := range v.Files {
				if fi, err := os.Stat(filepath.Join(st.Dir, f.Name)); err == nil {
					st.Bytes += fi.Size()
				}
			}
		}
	case "embedded":
		st.Available = true
	}
	return st
}

// CJKDictVariants lists the downloadable dictionary variants for UI/API
// enumeration, in canonical order.
func CJKDictVariants() []CJKVariantInfo {
	reg := cjkRegistryFn()
	out := make([]CJKVariantInfo, 0, len(cjkDictOrder))
	for _, name := range cjkDictOrder {
		if v, ok := reg[name]; ok {
			var bytes int64
			for _, f := range v.Files {
				bytes += f.Bytes
			}
			out = append(out, CJKVariantInfo{Name: v.Name, Desc: v.Desc, Bytes: bytes})
		}
	}
	return out
}

// DownloadCJKDict downloads the default (zh) dictionary to the default dir.
func DownloadCJKDict() (CJKDictStatus, error) {
	return DownloadCJKDictTo(CJKDictZH, "", "")
}

// DownloadCJKDictTo downloads the named dictionary variant into dir
// (defaults to ~/.ladyM/dict) and reloads the segmenter so the new dict
// takes effect immediately. It is the ONE place that touches the network —
// called only by the admin-triggered download endpoint; no LadyM command,
// startup path, or background loop ever invokes it. dict "" selects the
// default (zh); mirrorBase, when non-empty, replaces the default mirror
// list so air-gapped installs can point at an internal mirror serving the
// same layout (<base>/<RelPath>).
//
// Every file is fully downloaded and sha256-verified before anything is
// written into place, and files of the previously installed variant that
// the new one does not use are cleaned up, so a failed or variant-switching
// download leaves a coherent dictionary behind.
func DownloadCJKDictTo(dict CJKDictName, dir string, mirrorBase string) (CJKDictStatus, error) {
	if dict == "" {
		dict = CJKDictZH
	}
	reg := cjkRegistryFn()
	v, ok := reg[dict]
	if !ok {
		return CJKDictStatus{}, fmt.Errorf("unknown dictionary %q (available: %s)",
			dict, strings.Join(cjkDictNames(reg), ", "))
	}
	if dir == "" {
		dir = cjkDictDirFn()
	}
	mirrors := cjkDictMirrorFn()
	if mirrorBase != "" {
		mirrors = []string{mirrorBase}
	}

	// Fetch and verify everything before touching the destination dir, so
	// a failed download leaves no dict dir behind.
	bodies := make([][]byte, len(v.Files))
	for i, spec := range v.Files {
		body, err := downloadDictFile(spec, mirrors)
		if err != nil {
			return CJKDictStatus{}, err
		}
		bodies[i] = body
	}

	previous := detectInstalledVariant(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return CJKDictStatus{}, fmt.Errorf("create dict dir %s: %w", dir, err)
	}
	// Drop the previous variant's files the new variant does not use.
	if previous != nil && previous.Name != v.Name {
		newFiles := map[string]bool{}
		for _, f := range v.Files {
			newFiles[f.Name] = true
		}
		for _, f := range previous.Files {
			if !newFiles[f.Name] {
				os.Remove(filepath.Join(dir, f.Name))
			}
		}
	}
	for i, spec := range v.Files {
		if err := atomicWrite(filepath.Join(dir, spec.Name), bodies[i]); err != nil {
			return CJKDictStatus{}, err
		}
	}
	manifest, _ := json.Marshal(map[string]any{
		"version": cjkDictVersion,
		"variant": v.Name,
		"files":   v.Files,
	})
	if err := atomicWrite(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return CJKDictStatus{}, err
	}

	cjkMu.Lock()
	reloadCJKLocked()
	cjkMu.Unlock()
	return CJKDictStatusNow(), nil
}

func cjkDictNames(reg map[CJKDictName]cjkDictVariant) []string {
	names := make([]string, 0, len(cjkDictOrder))
	for _, n := range cjkDictOrder {
		if _, ok := reg[n]; ok {
			names = append(names, string(n))
		}
	}
	return names
}

// downloadDictFile fetches one spec from the first mirror that serves
// sha256-verified content.
func downloadDictFile(spec cjkDictFileSpec, mirrors []string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	var lastErr error
	for _, base := range mirrors {
		url := base + spec.RelPath
		resp, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("mirror %s: %w", base, err)
			continue
		}
		// Cap reads slightly above the pinned size to reject anything
		// wildly larger before buffering it.
		body, err := io.ReadAll(io.LimitReader(resp.Body, spec.Bytes*2))
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("mirror %s: read body: %w", base, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("mirror %s: status %d", base, resp.StatusCode)
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != spec.SHA256 {
			lastErr = fmt.Errorf("mirror %s: sha256 mismatch for %s", base, spec.Name)
			continue
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no mirrors configured for %s", spec.Name)
	}
	return nil, fmt.Errorf("download %s: %w", spec.Name, lastErr)
}

// atomicWrite writes data to path via a temp file + rename so concurrent
// readers never observe a partial dict. Files end 0644-readable because
// the dict dir is often a volume shared by containers.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// RemoveCJKDict deletes the downloaded file dict and reloads, falling back
// to the embedded dict (fulldict builds) or per-character tokenization.
// It is a no-op when nothing was downloaded.
func RemoveCJKDict() error {
	dir := cjkDictDirFn()
	os.Remove(filepath.Join(dir, "manifest.json"))
	reg := cjkRegistryFn()
	removed := map[string]bool{}
	for _, name := range cjkDictOrder {
		if v, ok := reg[name]; ok {
			for _, f := range v.Files {
				if !removed[f.Name] {
					os.Remove(filepath.Join(dir, f.Name))
					removed[f.Name] = true
				}
			}
		}
	}
	cjkMu.Lock()
	reloadCJKLocked()
	cjkMu.Unlock()
	return nil
}

// runScript classifies a CJK script run by its first rune. Kana bundles
// Hiragana + Katakana because variants cover them together.
func runScript(run string) string {
	r, _ := utf8.DecodeRuneInString(run)
	switch {
	case unicode.Is(unicode.Han, r):
		return "Han"
	case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
		return "Kana"
	case unicode.Is(unicode.Hangul, r):
		return "Hangul"
	}
	return ""
}
