"""High-level SDK facade — the recommended Python entry point.

Hides Engine lifecycle from the user. The functions open a short-lived Engine per call,
which is fine for CLI/MCP-style use; long-running processes should hold an Engine directly.
"""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path

from .config import Config
from .engine import Engine


@contextmanager
def open_engine(config: Config | None = None, *,
                db_path: str | Path | None = None,
                workspace: str | None = None) -> Iterator[Engine]:
    """Context manager that yields an Engine and closes it on exit."""
    cfg = config or Config()
    if db_path is not None:
        cfg.db_path = Path(db_path)
    if workspace is not None:
        cfg.workspace = workspace
    eng = Engine(cfg)
    try:
        yield eng
    finally:
        eng.close()


def recall(query: str, *, db_path: str | Path | None = None,
           workspace: str | None = None, top_k: int | None = None):
    """One-shot recall. Returns a RecallResponse."""
    with open_engine(db_path=db_path, workspace=workspace) as eng:
        return eng.recall(query, top_k=top_k)


def remember(content: str, *, db_path: str | Path | None = None,
             workspace: str | None = None, tags: list[str] | None = None,
             source: str = ""):
    """One-shot write of a semantic fact."""
    with open_engine(db_path=db_path, workspace=workspace) as eng:
        return eng.semantic.put_fact(content, tags=tags or [], source=source)


def index_code(root: str | Path, *, db_path: str | Path | None = None,
               workspace: str | None = None, force: bool = False):
    """One-shot codebase index."""
    with open_engine(db_path=db_path, workspace=workspace) as eng:
        return eng.index_code(root, force=force)
