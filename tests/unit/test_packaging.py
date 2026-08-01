"""Guards the dependency structure: tree-sitter must stay optional ([codeindex]),
never leak back into core dependencies."""

import tomllib
from pathlib import Path

PYPROJECT = Path(__file__).resolve().parents[2] / "pyproject.toml"


def _load() -> dict:
    with PYPROJECT.open("rb") as f:
        return tomllib.load(f)


def test_tree_sitter_not_in_core_dependencies():
    """tree-sitter must be optional, never a hard dependency."""
    core = _load()["project"]["dependencies"]
    leaked = [d for d in core if "tree-sitter" in d]
    assert not leaked, f"tree-sitter leaked into core deps: {leaked}"


def test_codeindex_extra_holds_tree_sitter():
    opt = _load()["project"]["optional-dependencies"]
    codeindex = " ".join(opt.get("codeindex", []))
    assert "tree-sitter" in codeindex
    assert "tree-sitter-language-pack" in codeindex


def test_all_and_dev_aggregate_codeindex():
    opt = _load()["project"]["optional-dependencies"]
    assert any("codeindex" in s for s in opt["all"]), "[all] must include codeindex"
    assert any("codeindex" in s for s in opt["dev"]), "[dev] must include codeindex"
