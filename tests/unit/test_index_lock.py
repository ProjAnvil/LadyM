"""Tests for the cross-process single-flight lock around code indexing.

Two concurrent ``index_code`` runs on the same database previously thrash the
same SQLite store (a production hang). The fix is an advisory ``flock`` on a
``<db>.index.lock`` file: a second indexer fails fast with
:class:`IndexInProgressError` instead of running a second full reindex.
"""

from __future__ import annotations

import fcntl
import os

import pytest

from ladym.code.indexer import _exclusive_index_lock
from ladym.errors import IndexInProgressError


def _hold_lock(db_path) -> int:
    """Acquire the raw flock and return the fd (caller must unlock + close)."""
    fd = os.open(f"{db_path}.index.lock", os.O_CREAT | os.O_RDWR, 0o644)
    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    return fd


def _release_lock(fd: int) -> None:
    fcntl.flock(fd, fcntl.LOCK_UN)
    os.close(fd)


def test_lock_blocks_second_holder(tmp_db):
    held = _hold_lock(tmp_db)
    try:
        with pytest.raises(IndexInProgressError), _exclusive_index_lock(tmp_db):
            pass
    finally:
        _release_lock(held)


def test_lock_releases_on_exit(tmp_db):
    with _exclusive_index_lock(tmp_db):
        pass
    # A second acquisition succeeds now that the first has released.
    with _exclusive_index_lock(tmp_db):
        pass


def test_lock_releases_on_exception(tmp_db):
    with pytest.raises(RuntimeError), _exclusive_index_lock(tmp_db):
        raise RuntimeError("boom")
    with _exclusive_index_lock(tmp_db):
        pass


def test_index_codebase_fails_fast_when_already_indexing(tmp_path):
    """Engine.index_code must honor the lock: a held lock raises before any work."""
    from ladym.config import Config
    from ladym.engine import Engine

    cfg = Config.for_testing(tmp_path)
    eng = Engine(cfg)
    try:
        root = tmp_path / "repo"
        root.mkdir()
        (root / "a.py").write_text("def foo():\n    return 1\n")

        held = _hold_lock(eng.store.db_path)
        try:
            with pytest.raises(IndexInProgressError):
                eng.index_code(root)
        finally:
            _release_lock(held)

        # Once released, the same index call succeeds.
        report = eng.index_code(root)
        assert report.files_indexed == 1
    finally:
        eng.close()
