"""Tree-sitter grammar registry with graceful fallback.

Auto-detects a language from a file extension, returns a parser and a tiny
language-specific spec (node kinds that mark definitions, the docstring node name, etc.).
Languages without a grammar degrade to line-window chunking handled in ``indexer.py``.
"""

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path

# Language id → file extensions
_EXT_MAP: dict[str, str] = {
    ".py": "python", ".pyi": "python",
    ".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
    ".ts": "typescript", ".tsx": "typescript",
    ".go": "go",
    ".rs": "rust",
    ".java": "java",
    ".kt": "kotlin",
    ".c": "c", ".h": "c",
    ".cpp": "cpp", ".cc": "cpp", ".cxx": "cpp", ".hpp": "cpp",
    ".cs": "csharp",
    ".rb": "ruby",
    ".php": "php",
    ".swift": "swift",
    ".scala": "scala",
    ".sh": "bash", ".bash": "bash",
    ".lua": "lua",
    ".sql": "sql",
    ".html": "html",
    ".css": "css",
}

# Per-language spec: tree-sitter node kinds that define named symbols, and the
# canonical field name of the doc/comment string if any.
@dataclass
class LanguageSpec:
    name: str
    definition_kinds: tuple[str, ...]                  # AST node types that mark a definition
    name_field: str = "name"                           # field carrying the symbol's identifier
    doc_node_kinds: tuple[str, ...] = ()               # comment / string node types preceding a def
    parameters_field: str = "parameters"
    body_field: str = "body"
    call_kinds: tuple[str, ...] = ("call", "call_expression")  # nodes that reference a callable
    fallback_chunk_lines: int = 40


_SPECS: dict[str, LanguageSpec] = {
    "python": LanguageSpec(
        name="python",
        definition_kinds=(
            "function_definition", "class_definition", "decorated_definition",
        ),
        name_field="name",
        doc_node_kinds=("string", "expression_statement"),
        parameters_field="parameters",
        body_field="body",
    ),
    "javascript": LanguageSpec(
        name="javascript",
        definition_kinds=(
            "function_declaration", "class_declaration", "method_definition",
            "arrow_function", "variable_declarator",
        ),
        name_field="name",
        doc_node_kinds=("comment",),
    ),
    "typescript": LanguageSpec(
        name="typescript",
        definition_kinds=(
            "function_declaration", "class_declaration", "method_definition",
            "interface_declaration", "type_alias_declaration",
            "arrow_function", "variable_declarator",
        ),
        name_field="name",
        doc_node_kinds=("comment",),
    ),
    "go": LanguageSpec(
        name="go",
        definition_kinds=(
            "function_declaration", "method_declaration", "type_declaration",
        ),
        name_field="name",
        doc_node_kinds=("comment",),
    ),
    "rust": LanguageSpec(
        name="rust",
        definition_kinds=("function_item", "struct_item", "enum_item", "impl_item"),
        name_field="name",
        doc_node_kinds=("line_comment", "block_comment"),
    ),
    "java": LanguageSpec(
        name="java",
        definition_kinds=(
            "method_declaration", "class_declaration", "interface_declaration",
            "constructor_declaration",
        ),
        name_field="name",
        doc_node_kinds=("block_comment", "line_comment"),
    ),
    "c": LanguageSpec(
        name="c",
        definition_kinds=("function_definition", "struct_specifier", "declaration"),
        name_field="declarator",
        doc_node_kinds=("comment",),
    ),
    "cpp": LanguageSpec(
        name="cpp",
        definition_kinds=(
            "function_definition", "class_specifier", "struct_specifier", "declaration",
        ),
        name_field="declarator",
        doc_node_kinds=("comment",),
    ),
}


def detect_language(path: Path) -> str | None:
    return _EXT_MAP.get(path.suffix.lower())


def get_spec(language: str) -> LanguageSpec | None:
    if language in _SPECS:
        return _SPECS[language]
    # Unknown language: return a permissive spec used only for line-chunking fallback.
    return LanguageSpec(
        name=language,
        definition_kinds=(),
        fallback_chunk_lines=40,
    )


_PARSER_CACHE: dict[str, object] = {}


def get_parser(language: str):  # type: ignore[no-untyped-def]
    """Return a tree-sitter Parser for ``language`` or ``None`` if no grammar is available."""
    if language in _PARSER_CACHE:
        return _PARSER_CACHE[language]
    try:
        import tree_sitter  # type: ignore
        from tree_sitter_language_pack import get_parser as _get_parser  # type: ignore
        try:
            parser = _get_parser(language)
        except Exception:
            # older API shape: Language + Parser
            from tree_sitter_language_pack import get_language  # type: ignore
            lang = get_language(language)
            parser = tree_sitter.Parser()
            try:
                parser.language = lang  # tree-sitter >=0.22
            except AttributeError:  # pragma: no cover - very old API
                parser.set_language(lang)  # type: ignore[attr-defined]
        _PARSER_CACHE[language] = parser
        return parser
    except Exception:
        _PARSER_CACHE[language] = None
        return None
