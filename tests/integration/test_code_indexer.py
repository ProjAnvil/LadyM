"""Tests for the tree-sitter code indexer."""

import sys
from pathlib import Path

import pytest

from ladym.code.languages import detect_language, get_parser, get_spec
from ladym.config import Config
from ladym.engine import Engine
from ladym.schema import MemoryType

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


# ---- language detection ----

def test_detect_language_python_js_go():
    assert detect_language(Path("a.py")) == "python"
    assert detect_language(Path("b.js")) == "javascript"
    assert detect_language(Path("c.go")) == "go"
    assert detect_language(Path("README.md")) is None


def test_get_parser_returns_tree_sitter_parser():
    p = get_parser("python")
    assert p is not None
    tree = p.parse(b"def f():\n    return 1\n")
    assert tree.root_node.type == "module"


def test_get_spec_python_marks_function_and_class():
    spec = get_spec("python")
    assert "function_definition" in spec.definition_kinds
    assert "class_definition" in spec.definition_kinds


# ---- end-to-end indexing ----

def test_index_codebase_extracts_symbols(engine):
    report = engine.index_code(FIXTURE)
    assert report.files_indexed >= 2
    assert report.symbols_written >= 4  # functions + methods + classes across both files
    # auth/service.py symbols
    syms_auth = engine.store.symbols_for_file("auth/service.py")
    names = {s.qualified_name for s in syms_auth}
    assert any("hash_password" in n for n in names)
    assert any("AuthService" in n for n in names)
    assert any("login" in n for n in names)


def test_index_codebase_writes_code_file_memory(engine):
    engine.index_code(FIXTURE)
    resp = engine.recall("auth service module", types=[MemoryType.CODE_FILE])
    files = {r.memory.metadata.get("file_path") for r in resp.results}
    assert "auth/service.py" in files


def test_index_codebase_is_incremental(engine):
    r1 = engine.index_code(FIXTURE)
    assert r1.files_indexed >= 2
    r2 = engine.index_code(FIXTURE)  # nothing changed
    assert r2.files_skipped_unchanged == r1.files_indexed
    assert r2.files_indexed == 0


def test_index_codebase_force_reindexes(engine):
    engine.index_code(FIXTURE)
    r2 = engine.index_code(FIXTURE, force=True)
    assert r2.files_indexed >= 2


def test_index_codebase_respects_skip_dirs(engine, tmp_path):
    bad = tmp_path / "repo" / "__pycache__"
    bad.mkdir(parents=True)
    (bad / "junk.py").write_text("def x():\n    pass\n")
    good = tmp_path / "repo" / "real.py"
    good.write_text("def y():\n    pass\n")
    report = engine.index_code(tmp_path / "repo")
    assert report.files_seen >= 1
    assert report.files_indexed == 1
    assert all("junk" not in e for e in report.errors)


def test_indexed_symbols_are_recallable_by_keyword(engine):
    engine.index_code(FIXTURE)
    resp = engine.search_code("verify password hash")
    assert resp.results
    top = resp.results[0].memory
    assert "verify_password" in top.content or "hash_password" in top.content


def test_indexed_symbols_carry_signature_and_docstring(engine):
    engine.index_code(FIXTURE)
    syms = engine.store.symbols_for_file("auth/service.py")
    verify = next(s for s in syms if "verify_password" in s.qualified_name)
    assert verify.signature  # non-empty
    assert "Check" in verify.docstring or "plaintext" in verify.docstring.lower()


def test_intrfile_refs_extracted(engine):
    engine.index_code(FIXTURE)
    # AuthService.login calls verify_password (after self._issue_token); refs should record at least one
    # find any ref whose source is a method of AuthService
    rows = engine.store.conn.execute("SELECT * FROM code_refs").fetchall()
    assert len(rows) >= 1
    srcs = {r["src_symbol"] for r in rows}
    assert any("login" in s for s in srcs) or any("AuthService" in s for s in srcs)


def test_index_code_without_codeindex_raises_guidance(monkeypatch, tmp_path):
    """Without the [codeindex] extra, index_code raises a helpful ImportError
    instead of silently degrading every file to chunk fallback."""
    # simulate tree-sitter not installed: None in sys.modules => import raises
    monkeypatch.setitem(sys.modules, "tree_sitter", None)
    monkeypatch.setitem(sys.modules, "tree_sitter_language_pack", None)

    eng = Engine(Config.for_testing(tmp_path))
    try:
        with pytest.raises(ImportError, match=r"\[codeindex\]"):
            eng.index_code(tmp_path)  # empty dir; guard fires before the walk
    finally:
        eng.close()
