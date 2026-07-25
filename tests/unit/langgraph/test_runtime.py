"""Tests for ladym.langgraph._runtime — no langgraph import required."""
from __future__ import annotations

from pathlib import Path

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.langgraph._runtime import resolve_engine, resolve_workspace


@pytest.fixture
def offline_config(tmp_path: Path) -> Config:
    return Config.for_testing(tmp_path)


def test_resolve_engine_accepts_engine(offline_config):
    eng = Engine(offline_config)
    try:
        assert resolve_engine(eng) is eng
    finally:
        eng.close()


def test_resolve_engine_accepts_config(offline_config):
    eng = resolve_engine(offline_config)
    try:
        assert isinstance(eng, Engine)
        assert eng.config.workspace == offline_config.workspace
    finally:
        eng.close()


def test_resolve_engine_accepts_path(tmp_path):
    db = tmp_path / "x.db"
    eng = resolve_engine(db)
    try:
        assert isinstance(eng, Engine)
        assert eng.config.db_path == db
    finally:
        eng.close()


def test_resolve_engine_accepts_none():
    eng = resolve_engine(None)
    try:
        assert isinstance(eng, Engine)
    finally:
        eng.close()


def test_resolve_engine_workspace_override_on_config(tmp_path):
    cfg = Config.for_testing(tmp_path)
    eng = resolve_engine(cfg, workspace="user-123")
    try:
        assert eng.config.workspace == "user-123"
    finally:
        eng.close()


def test_resolve_engine_ignores_workspace_when_engine_passed(tmp_path):
    eng = Engine(Config.for_testing(tmp_path))
    try:
        out = resolve_engine(eng, workspace="ignored")
        assert out is eng
        assert out.config.workspace == eng.config.workspace
    finally:
        eng.close()


def test_resolve_workspace_from_user_id(offline_config):
    eng = Engine(offline_config)
    try:
        cfg = {"configurable": {"user_id": "u-456"}}
        assert resolve_workspace(cfg, eng) == "u-456"
    finally:
        eng.close()


def test_resolve_workspace_fallback_to_engine(offline_config):
    eng = Engine(offline_config)
    try:
        assert resolve_workspace({}, eng) == eng.config.workspace
        assert resolve_workspace(None, eng) == eng.config.workspace
    finally:
        eng.close()
