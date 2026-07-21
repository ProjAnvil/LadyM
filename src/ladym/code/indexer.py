"""The codebase indexing driver (ARCHITECTURE.md §7).

Walks a repo, hashes each source file (incremental skip on unchanged), parses with
tree-sitter, extracts symbols + cross-refs, and stores them as L2 semantic memories +
L4-style code_refs. Files without a grammar degrade to sliding-window line chunking.
"""

from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass, field
from pathlib import Path

from ..config import Config
from ..schema import CodeSymbol, Layer, Memory, MemoryType
from ..storage.embeddings import EmbeddingProvider
from ..storage.store import SQLiteStore
from .languages import detect_language, get_parser, get_spec
from .symbol_graph import RawSymbol, build_refs, extract_symbols

# cheap gitignore-style skip: directory basenames to never descend into
_SKIP_DIRS = {
    ".git", ".hg", ".svn", ".venv", "venv", "env", "__pycache__", ".mypy_cache",
    ".ruff_cache", ".pytest_cache", "node_modules", "dist", "build", ".tox",
    ".eggs", "target", "Pods", "DerivedData",
}


@dataclass
class IndexReport:
    files_seen: int = 0
    files_indexed: int = 0
    files_skipped_unchanged: int = 0
    files_skipped_unsupported: int = 0
    symbols_written: int = 0
    refs_written: int = 0
    elapsed_ms: float = 0.0
    errors: list[str] = field(default_factory=list)


def _hash_bytes(data: bytes) -> str:
    return hashlib.blake2b(data, digest_size=16).hexdigest()


def _should_ignore(path: Path, ignore_globs: list[str]) -> bool:
    if any(part in _SKIP_DIRS for part in path.parts):
        return True
    name = path.name
    return any(pat.rstrip("*").rstrip("/") in name for pat in ignore_globs)


def index_codebase(
    root: Path,
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    force: bool = False,
    language_filter: list[str] | None = None,
) -> IndexReport:
    """Walk ``root`` and index every supported source file."""
    start = time.time()
    ws = workspace or cfg.workspace
    root = Path(root).resolve()
    report = IndexReport()
    icfg = cfg.code_index

    for path in _walk(root, icfg.extra_ignore_globs):
        report.files_seen += 1
        lang = detect_language(path)
        if lang is None:
            report.files_skipped_unsupported += 1
            continue
        if language_filter is not None and lang not in language_filter:
            continue
        try:
            data = path.read_bytes()
        except OSError as e:
            report.errors.append(f"{path}: {e}")
            continue
        h = _hash_bytes(data)
        rel = str(path.relative_to(root))
        if not force:
            prev = store.get_indexed_hash(rel)
            if prev == h:
                report.files_skipped_unchanged += 1
                continue

        module_name = _module_name(rel, lang)
        spec = get_spec(lang)
        syms: list[RawSymbol] = []
        try:
            parser = get_parser(lang)
            if parser is not None and spec.definition_kinds:
                tree = parser.parse(data)
                syms = extract_symbols(
                    tree, data, spec,
                    module_name=module_name,
                    file_path=rel,
                    max_body_lines=icfg.max_body_lines_per_symbol,
                )
        except Exception as e:  # parse error ⇒ graceful fallback
            report.errors.append(f"{path}: parse failed ({e}); using chunk fallback")
            syms = []

        if not syms and spec.fallback_chunk_lines:
            # grammar-less / parse-failed: chunk by line windows
            syms = _chunk_fallback(data, module_name, rel, lang, spec.fallback_chunk_lines)

        # write file-level memory (always)
        file_summary = _file_summary(rel, lang, syms)
        _put_file_memory(store, embedder, ws, rel, lang, file_summary)

        # write per-symbol memories + projections
        for sym in syms:
            _put_symbol_memory(store, embedder, ws, rel, lang, sym, module_name)

        # write intra-file refs (resolved to qualified names where possible)
        refs = build_refs(syms, rel)
        store.put_code_refs(refs)

        store.set_indexed(rel, h)
        report.files_indexed += 1
        report.symbols_written += len(syms)
        report.refs_written += len(refs)

    report.elapsed_ms = (time.time() - start) * 1000.0
    return report


def _walk(root: Path, ignore_globs: list[str]):
    for p in sorted(root.rglob("*")):
        if not p.is_file():
            continue
        if _should_ignore(p, ignore_globs):
            continue
        yield p


def _module_name(rel: str, lang: str) -> str:
    base = rel.replace("/", ".").replace("\\", ".")
    for ext in (".py", ".js", ".ts", ".go", ".rs", ".java", ".c", ".cpp", ".rb", ".cs"):
        if base.endswith(ext):
            base = base[: -len(ext)]
            break
    if base.endswith(".index"):
        base = base[: -len(".index")]
    return base or "module"


def _file_summary(rel: str, lang: str, syms: list[RawSymbol]) -> str:
    if not syms:
        return f"{lang} source file: {rel} (no symbols extracted)"
    kinds: dict[str, int] = {}
    for s in syms:
        kinds[s.kind] = kinds.get(s.kind, 0) + 1
    kind_str = ", ".join(f"{n} {k}" for k, n in sorted(kinds.items()))
    top = ", ".join(s.name for s in syms[:8])
    return f"{rel} ({lang}): {kind_str}. Top symbols: {top}."


def _put_file_memory(
    store: SQLiteStore, embedder: EmbeddingProvider, ws: str,
    rel: str, lang: str, summary: str,
) -> None:
    # delete prior file memory for this path before re-inserting
    store.conn.execute(
        "DELETE FROM memories WHERE type = ? AND source = ? AND workspace = ?",
        (MemoryType.CODE_FILE.value, rel, ws),
    )
    mem = Memory(
        layer=Layer.SEMANTIC,
        type=MemoryType.CODE_FILE,
        content=summary,
        summary=summary[:120],
        tags=["code", lang],
        metadata={"file_path": rel, "language": lang},
        source=rel,
        workspace=ws,
        content_hash=_hash_bytes(summary.encode()),
    )
    store.put_memory(mem, vector=embedder.embed(summary))


def _put_symbol_memory(
    store: SQLiteStore, embedder: EmbeddingProvider, ws: str,
    rel: str, lang: str, sym: RawSymbol, module_name: str,
) -> None:
    # delete prior symbol memory with the same qualified_name in this workspace
    store.conn.execute(
        "DELETE FROM memories WHERE type = ? AND workspace = ? "
        "AND id IN (SELECT memory_id FROM code_symbols WHERE qualified_name = ?)",
        (MemoryType.CODE_SYMBOL.value, ws, sym.qualified_name),
    )
    content = _render_symbol_content(sym)
    mem = Memory(
        layer=Layer.SEMANTIC,
        type=MemoryType.CODE_SYMBOL,
        content=content,
        summary=f"{sym.kind} {sym.qualified_name}",
        tags=["code", lang, sym.kind],
        metadata={
            "file_path": rel,
            "language": lang,
            "qualified_name": sym.qualified_name,
            "kind": sym.kind,
        },
        source=rel,
        workspace=ws,
        content_hash=_hash_bytes(content.encode()),
    )
    vec = embedder.embed(content)
    store.put_memory(mem, vector=vec)
    store.put_code_symbol(
        CodeSymbol(
            memory_id=mem.id,
            file_path=rel,
            symbol_kind=sym.kind,
            qualified_name=sym.qualified_name,
            signature=sym.signature,
            docstring=sym.docstring,
            line_start=sym.line_start,
            line_end=sym.line_end,
            language=lang,
        )
    )


def _render_symbol_content(sym: RawSymbol) -> str:
    parts = [f"{sym.kind} {sym.qualified_name}"]
    if sym.signature:
        parts.append(f"signature: {sym.signature}")
    if sym.docstring:
        parts.append(f"doc: {sym.docstring}")
    if sym.body:
        parts.append("body:\n" + sym.body)
    return "\n".join(parts)


def _chunk_fallback(
    data: bytes, module_name: str, rel: str, lang: str, chunk_lines: int,
) -> list[RawSymbol]:
    """No-grammar fallback: split file into ``chunk_lines``-line windows."""
    try:
        text = data.decode("utf-8", errors="replace")
    except Exception:
        return []
    lines = text.split("\n")
    out: list[RawSymbol] = []
    for i in range(0, len(lines), chunk_lines):
        chunk = lines[i: i + chunk_lines]
        out.append(
            RawSymbol(
                kind="chunk",
                name=f"lines_{i + 1}",
                qualified_name=f"{module_name}.lines_{i + 1}",
                signature="",
                docstring="",
                line_start=i + 1,
                line_end=i + len(chunk),
                body="\n".join(chunk),
                calls=[],
            )
        )
    return out
