"""Smoke tests for ladym-wrapper against the real Go binary."""

from __future__ import annotations

import pytest

from ladym_wrapper import AsyncLadymClient, LadymClient, find_ladym_binary


def test_find_ladym_binary() -> None:
    assert find_ladym_binary().endswith("ladym")


def test_sync_stats_roundtrip(tmp_path) -> None:
    with LadymClient(db=tmp_path / "sync.ladym.db") as client:
        result = client.stats()
    assert result is not None


@pytest.mark.anyio
async def test_async_remember_recall_roundtrip(tmp_path) -> None:
    db = tmp_path / "smoke.ladym.db"
    async with AsyncLadymClient(db=db) as client:
        await client.remember("wrapper smoke test fact", source="pytest")
        hits = await client.recall("wrapper smoke test")
    assert hits is not None
