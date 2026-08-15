package code

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"golang.org/x/crypto/blake2b"
)

// IndexReport reports the outcome of an indexing pass.
type IndexReport struct {
	FilesSeen               int
	FilesIndexed            int
	FilesSkippedUnchanged   int
	FilesSkippedUnsupported int
	SymbolsWritten          int
	RefsWritten             int
	ElapsedMs               float64
	Errors                  []string
}

var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".venv": true, "venv": true, "env": true,
	"__pycache__": true, ".mypy_cache": true, ".ruff_cache": true, ".pytest_cache": true,
	"node_modules": true, "dist": true, "build": true, ".tox": true, ".eggs": true,
	"target": true, "Pods": true, "DerivedData": true,
}

func hashBytes(data []byte) string {
	h, _ := blake2b.New(16, nil)
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func shouldIgnore(path string, ignoreGlobs []string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, p := range parts {
		if skipDirs[p] {
			return true
		}
	}
	name := filepath.Base(path)
	for _, pat := range ignoreGlobs {
		stripped := strings.TrimRight(strings.TrimRight(pat, "*"), "/")
		if strings.Contains(name, stripped) {
			return true
		}
	}
	return false
}

// IndexCodebase walks root and indexes every supported source file.
//
// The whole pass runs under a cross-process flock on <db>.index.lock (mirrors
// the Python indexer): a second concurrent index — from another CLI, MCP
// server, or worker — fails fast with IndexInProgressError instead of
// interleaving writes.
func IndexCodebase(root string, store *storage.SQLiteStore, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, force bool, languageFilter []string) (*IndexReport, error) {
	release, err := acquireIndexLock(store.DBPath)
	if err != nil {
		return nil, err
	}
	defer release()

	start := time.Now()
	ws := workspace
	if ws == "" {
		ws = cfg.Workspace
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	report := &IndexReport{}
	icfg := cfg.CodeIndex

	paths, err := walkFiles(absRoot, icfg.ExtraIgnoreGlobs)
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	for _, path := range paths {
		report.FilesSeen++
		lang := DetectLanguage(path)
		if lang == "" {
			report.FilesSkippedUnsupported++
			continue
		}
		if languageFilter != nil && !contains(languageFilter, lang) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			report.Errors = append(report.Errors, path+": "+err.Error())
			continue
		}
		h := hashBytes(data)
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if !force {
			prev, err := store.GetIndexedHash(rel)
			if err != nil {
				return nil, err
			}
			if prev == h {
				report.FilesSkippedUnchanged++
				continue
			}
		}

		moduleName := moduleName(rel, lang)
		spec := GetSpec(lang)
		syms, err := ExtractSymbols(data, lang, moduleName, rel, icfg.MaxBodyLinesPerSymbol)
		if err != nil { // parse error ⇒ graceful fallback (mirrors Python)
			report.Errors = append(report.Errors, fmt.Sprintf("%s: parse failed (%v); using chunk fallback", path, err))
		}

		if len(syms) == 0 && spec.FallbackChunkLines > 0 {
			syms = chunkFallback(data, moduleName, rel, lang, spec.FallbackChunkLines)
		}

		fileSummary := fileSummary(rel, lang, syms)
		if err := putFileMemory(store, embedder, ws, rel, lang, fileSummary); err != nil {
			return nil, err
		}
		for _, sym := range syms {
			if err := putSymbolMemory(store, embedder, ws, rel, lang, sym); err != nil {
				return nil, err
			}
		}
		refs := BuildRefs(syms, rel)
		codeRefs := make([]*schema.CodeRef, 0, len(refs))
		for _, r := range refs {
			codeRefs = append(codeRefs, &schema.CodeRef{SrcSymbol: r.SrcSymbol, DstSymbol: r.DstSymbol, RefKind: r.RefKind})
		}
		if err := store.PutCodeRefs(codeRefs); err != nil {
			return nil, err
		}

		if err := store.SetIndexed(rel, h, 0); err != nil {
			return nil, err
		}
		report.FilesIndexed++
		report.SymbolsWritten += len(syms)
		report.RefsWritten += len(refs)
	}

	report.ElapsedMs = float64(time.Since(start).Nanoseconds()) / 1e6
	return report, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func walkFiles(root string, ignoreGlobs []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			// Directories are pruned only via the built-in skipDirs set,
			// mirroring Python's `_walk` (extra globs match file basenames only).
			if shouldIgnore(path, nil) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnore(path, ignoreGlobs) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func moduleName(rel, lang string) string {
	base := strings.ReplaceAll(strings.ReplaceAll(rel, "/", "."), "\\", ".")
	for _, ext := range []string{".py", ".js", ".ts", ".go", ".rs", ".java", ".c", ".cpp", ".rb", ".cs"} {
		if strings.HasSuffix(base, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	if strings.HasSuffix(base, ".index") {
		base = base[:len(base)-len(".index")]
	}
	if base == "" {
		return "module"
	}
	return base
}

func fileSummary(rel, lang string, syms []RawSymbol) string {
	if len(syms) == 0 {
		return lang + " source file: " + rel + " (no symbols extracted)"
	}
	kinds := map[string]int{}
	for _, s := range syms {
		kinds[s.Kind]++
	}
	kindKeys := make([]string, 0, len(kinds))
	for k := range kinds {
		kindKeys = append(kindKeys, k)
	}
	sort.Strings(kindKeys)
	var kindParts []string
	for _, k := range kindKeys {
		kindParts = append(kindParts, itoa(kinds[k])+" "+k)
	}
	top := make([]string, 0, 8)
	for i, s := range syms {
		if i >= 8 {
			break
		}
		top = append(top, s.Name)
	}
	return rel + " (" + lang + "): " + strings.Join(kindParts, ", ") + ". Top symbols: " + strings.Join(top, ", ") + "."
}

func putFileMemory(store *storage.SQLiteStore, embedder storage.EmbeddingProvider, ws, rel, lang, summary string) error {
	if _, err := store.DB().Exec(
		"DELETE FROM memories WHERE type = ? AND source = ? AND workspace = ?",
		string(schema.TypeCodeFile), rel, ws); err != nil {
		return err
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeFile)
	m.Content = summary
	m.Summary = truncateStr(summary, 120)
	m.Tags = []string{"code", lang}
	m.Metadata = map[string]any{"file_path": rel, "language": lang}
	m.Source = rel
	m.Workspace = ws
	m.ContentHash = hashBytes([]byte(summary))
	vec, err := embedder.Embed(summary)
	if err != nil {
		return err
	}
	return store.PutMemory(m, vec)
}

func putSymbolMemory(store *storage.SQLiteStore, embedder storage.EmbeddingProvider, ws, rel, lang string, sym RawSymbol) error {
	if _, err := store.DB().Exec(
		"DELETE FROM memories WHERE type = ? AND workspace = ? AND id IN (SELECT memory_id FROM code_symbols WHERE qualified_name = ?)",
		string(schema.TypeCodeSymbol), ws, sym.QualifiedName); err != nil {
		return err
	}
	content := renderSymbolContent(sym)
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	m.Content = content
	m.Summary = sym.Kind + " " + sym.QualifiedName
	m.Tags = []string{"code", lang, sym.Kind}
	m.Metadata = map[string]any{
		"file_path": rel, "language": lang,
		"qualified_name": sym.QualifiedName, "kind": sym.Kind,
	}
	m.Source = rel
	m.Workspace = ws
	m.ContentHash = hashBytes([]byte(content))
	vec, err := embedder.Embed(content)
	if err != nil {
		return err
	}
	if err := store.PutMemory(m, vec); err != nil {
		return err
	}
	return store.PutCodeSymbol(&schema.CodeSymbol{
		MemoryID: m.ID, FilePath: rel, SymbolKind: sym.Kind,
		QualifiedName: sym.QualifiedName, Signature: sym.Signature,
		Docstring: sym.Docstring, LineStart: sym.LineStart, LineEnd: sym.LineEnd,
		Language: lang,
	})
}

func renderSymbolContent(sym RawSymbol) string {
	parts := []string{sym.Kind + " " + sym.QualifiedName}
	if sym.Signature != "" {
		parts = append(parts, "signature: "+sym.Signature)
	}
	if sym.Docstring != "" {
		parts = append(parts, "doc: "+sym.Docstring)
	}
	if sym.Body != "" {
		parts = append(parts, "body:\n"+sym.Body)
	}
	return strings.Join(parts, "\n")
}

func chunkFallback(data []byte, moduleName, rel, lang string, chunkLines int) []RawSymbol {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var out []RawSymbol
	for i := 0; i < len(lines); i += chunkLines {
		end := i + chunkLines
		if end > len(lines) {
			end = len(lines)
		}
		chunk := lines[i:end]
		out = append(out, RawSymbol{
			Kind: "chunk", Name: "lines_" + itoa(i+1),
			QualifiedName: moduleName + ".lines_" + itoa(i+1),
			LineStart:     i + 1, LineEnd: end,
			Body: strings.Join(chunk, "\n"),
			// Calls stays nil: Python's `_chunk_fallback` sets calls=[],
			// so chunked files produce no code_refs.
		})
	}
	return out
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
