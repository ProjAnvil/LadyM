// Package code holds the codebase indexing subsystem (ARCHITECTURE.md §7).
//
// The Python port parsed source with tree-sitter (a C library). To keep the Go
// port pure-Go (no cgo, no native toolchain), symbol extraction uses a
// language-aware, line-and-regex based parser that produces the same RawSymbol
// records (qualified_name, kind, signature, docstring, line span, body text,
// calls). Languages without a dedicated extractor degrade to line-window
// chunking.
package code

import (
	"path/filepath"
	"strings"
)

// extMap maps a file extension to a language id.
var extMap = map[string]string{
	".py": "python", ".pyi": "python",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "typescript", ".tsx": "typescript",
	".go":   "go",
	".rs":   "rust",
	".java": "java",
	".kt":   "kotlin",
	".c":    "c", ".h": "c",
	".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp",
	".cs":    "csharp",
	".rb":    "ruby",
	".php":   "php",
	".swift": "swift",
	".scala": "scala",
	".sh":    "bash", ".bash": "bash",
	".lua":  "lua",
	".sql":  "sql",
	".html": "html",
	".css":  "css",
}

// LanguageSpec carries the minimal per-language configuration.
type LanguageSpec struct {
	Name               string
	FallbackChunkLines int
}

// specs lists languages with a dedicated extractor; all others use chunking.
var specs = map[string]*LanguageSpec{
	"python":     {Name: "python", FallbackChunkLines: 40},
	"javascript": {Name: "javascript", FallbackChunkLines: 40},
	"typescript": {Name: "typescript", FallbackChunkLines: 40},
	"go":         {Name: "go", FallbackChunkLines: 40},
	"rust":       {Name: "rust", FallbackChunkLines: 40},
	"java":       {Name: "java", FallbackChunkLines: 40},
	"c":          {Name: "c", FallbackChunkLines: 40},
	"cpp":        {Name: "cpp", FallbackChunkLines: 40},
}

// DetectLanguage returns the language id for a path, or "" when unsupported.
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return extMap[ext]
}

// GetSpec returns the language spec, or a permissive chunk-only spec for
// unknown languages.
func GetSpec(language string) *LanguageSpec {
	if s, ok := specs[language]; ok {
		return s
	}
	return &LanguageSpec{Name: language, FallbackChunkLines: 40}
}
