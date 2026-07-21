"""Pytest fixtures shared across the LadyM test suite."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

# make src/ importable without an install step
ROOT = Path(__file__).resolve().parent.parent
SRC = ROOT / "src"
if str(SRC) not in sys.path:
    sys.path.insert(0, str(SRC))

from ladym.config import Config  # noqa: E402
from ladym.storage.embeddings import HashingEmbedding  # noqa: E402


@pytest.fixture
def tmp_db(tmp_path: Path) -> Path:
    return tmp_path / "test.ladym.db"


@pytest.fixture
def offline_config(tmp_path: Path) -> Config:
    return Config.for_testing(tmp_path)


@pytest.fixture
def hashing_provider() -> HashingEmbedding:
    return HashingEmbedding(dim=256)
