"""End-to-end smoke test against the real ``ladym`` Go binary.

Skipped when ``bin/ladym`` has not been built (run
``go build -o bin/ladym ./cmd/ladym`` from the repo root first).
"""

from __future__ import annotations

import json
import os
from pathlib import Path

import pytest

from ladym_hermes.ladym_client import LadymClient

REPO_ROOT = Path(__file__).resolve().parents[3]
LADYM_BIN = REPO_ROOT / "bin" / "ladym"

pytestmark = pytest.mark.skipif(
    not LADYM_BIN.is_file(),
    reason="bin/ladym not built; run `go build -o bin/ladym ./cmd/ladym`",
)


@pytest.fixture
def client(tmp_path):
    # Isolate the server from the operator's environment: the repo-root
    # ./ladym.toml (loaded when cwd is the repo root) and ~/.ladym/config.toml
    # can wire an LLM-backed attention gate / embeddings, which makes
    # remember/recall nondeterministic. An empty HOME + cwd forces the
    # offline heuristic mode.
    env = {
        "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
        "HOME": str(tmp_path),
    }
    c = LadymClient(
        binary=str(LADYM_BIN), db=tmp_path / "ladym.db", workspace="e2e",
        env=env, cwd=str(tmp_path),
    )
    c.start()
    yield c
    c.close()


def test_remember_recall_forget_roundtrip(client):
    token = "zephyr-quokka-42"
    written = client.remember(
        f"The e2e smoke-test token is {token}.",
        tags=["e2e"], source="pytest", workspace="e2e",
    )
    # A fresh DB should not gate-drop this write.
    assert written.get("id"), f"remember was dropped: {written}"

    recalled = client.recall(f"e2e smoke-test token {token}", top_k=5, workspace="e2e")
    hits = recalled.get("results", [])
    assert any(token in json.dumps(h) for h in hits), (
        f"token not recalled: {json.dumps(recalled)[:400]}"
    )

    forgotten = client.forget(written["id"])
    assert forgotten.get("forgotten") == written["id"]


def test_stats_and_record_event(client):
    evt = client.record_event(
        agent="pytest", action="e2e", observation="ran smoke test",
        outcome="ok", tags=["e2e"], workspace="e2e",
    )
    assert evt.get("id")
    stats = client.stats()
    assert isinstance(stats, dict)
