// Package code holds the codebase indexing subsystem (ARCHITECTURE.md §7).
//
// Symbol extraction is backed by gotreesitter (a pure-Go tree-sitter runtime
// that loads the same parse tables as upstream tree-sitter), so LadyM keeps
// its no-cgo / single-binary / hermetic-build properties while producing real
// AST-level symbols. Languages without a detailed spec degrade to line-window
// chunking (matching the Python port, which also only had per-language specs
// for these languages).
package code

import (
	"path/filepath"
	"strings"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
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

// LanguageSpec carries the tree-sitter node kinds for one language (mirrors the
// Python `LanguageSpec` in languages.py).
type LanguageSpec struct {
	Name string
	// DefinitionKinds are tree-sitter node types that mark a definition.
	DefinitionKinds []string
	// NameField is the field carrying the symbol's identifier.
	NameField string
	// DocNodeKinds are comment/string node types preceding a def.
	DocNodeKinds []string
	// ParametersField / BodyField name the signature / body fields.
	ParametersField string
	BodyField       string
	// CallKinds are nodes that reference a callable.
	CallKinds []string
	// FallbackChunkLines is the line-window size for the chunk fallback.
	FallbackChunkLines int
	// Grammar lazily loads the gotreesitter grammar for this language.
	Grammar func() *gotreesitter.Language
}

var specs = map[string]*LanguageSpec{
	"python": {
		Name:               "python",
		DefinitionKinds:    []string{"function_definition", "class_definition", "decorated_definition"},
		NameField:          "name",
		DocNodeKinds:       []string{"string", "expression_statement"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.PythonLanguage,
	},
	"javascript": {
		Name:               "javascript",
		DefinitionKinds:    []string{"function_declaration", "class_declaration", "method_definition", "arrow_function", "variable_declarator"},
		NameField:          "name",
		DocNodeKinds:       []string{"comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.JavascriptLanguage,
	},
	"typescript": {
		Name:               "typescript",
		DefinitionKinds:    []string{"function_declaration", "class_declaration", "method_definition", "interface_declaration", "type_alias_declaration", "arrow_function", "variable_declarator"},
		NameField:          "name",
		DocNodeKinds:       []string{"comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.TypescriptLanguage,
	},
	"go": {
		Name:               "go",
		DefinitionKinds:    []string{"function_declaration", "method_declaration", "type_declaration"},
		NameField:          "name",
		DocNodeKinds:       []string{"comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.GoLanguage,
	},
	"rust": {
		Name:               "rust",
		DefinitionKinds:    []string{"function_item", "struct_item", "enum_item", "impl_item"},
		NameField:          "name",
		DocNodeKinds:       []string{"line_comment", "block_comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.RustLanguage,
	},
	"java": {
		Name:               "java",
		DefinitionKinds:    []string{"method_declaration", "class_declaration", "interface_declaration", "constructor_declaration"},
		NameField:          "name",
		DocNodeKinds:       []string{"block_comment", "line_comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.JavaLanguage,
	},
	"c": {
		Name:               "c",
		DefinitionKinds:    []string{"function_definition", "struct_specifier", "declaration"},
		NameField:          "declarator",
		DocNodeKinds:       []string{"comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.CLanguage,
	},
	"cpp": {
		Name:               "cpp",
		DefinitionKinds:    []string{"function_definition", "class_specifier", "struct_specifier", "declaration"},
		NameField:          "declarator",
		DocNodeKinds:       []string{"comment"},
		ParametersField:    "parameters",
		BodyField:          "body",
		CallKinds:          []string{"call", "call_expression"},
		FallbackChunkLines: 40,
		Grammar:            grammars.CppLanguage,
	},
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
