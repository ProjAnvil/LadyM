"""Integration test against a real `ladym serve --http` binary.

Gated on LADYM_SDK_IT=1 (same spirit as the Go-side LADYM_TEST_PG_DSN tests):
without it every test here skips. Requires a Go toolchain on PATH to build
./cmd/ladym into a temp dir.
"""

from __future__ import annotations

import os
import socket
import subprocess
import time
import urllib.request
from pathlib import Path

import pytest

from ladym_client import Client, LadymError

REPO_ROOT = Path(__file__).resolve().parents[3]

pytestmark = pytest.mark.skipif(
    os.environ.get("LADYM_SDK_IT") != "1",
    reason="integration test: set LADYM_SDK_IT=1 to run (builds + boots a real ladym server)",
)


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture(scope="module")
def server(tmp_path_factory):
    tmp = tmp_path_factory.mktemp("ladym-sdk-it")
    binary = tmp / "ladym"
    subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/ladym"],
        cwd=REPO_ROOT, check=True, capture_output=True, text=True,
    )
    port = _free_port()
    proc = subprocess.Popen(
        [str(binary), "serve", "--db", str(tmp / "it.db"), "--http", f"127.0.0.1:{port}"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    base = f"http://127.0.0.1:{port}"
    deadline = time.time() + 15
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(base + "/healthz", timeout=1) as resp:
                if resp.status == 200:
                    break
        except OSError:
            if proc.poll() is not None:
                raise RuntimeError("ladym serve exited before becoming healthy")
            time.sleep(0.1)
    else:
        proc.kill()
        raise RuntimeError("ladym serve did not become healthy in 15s")
    yield base
    proc.terminate()
    proc.wait(timeout=10)


def test_full_chain(server):
    c = Client(server)
    assert c.ping() is None

    r = c.remember("the quixplotron deploy pin is 42", tags=["it"], workspace="sdk-it")
    assert not r.dropped and r.id

    ev = c.record_event("sdk-bot", "boot", observation="sdk integration booted", workspace="sdk-it")
    assert ev.layer == "L1_episodic"

    resp = c.recall("quixplotron deploy pin", top_k=5, workspace="sdk-it")
    assert any("quixplotron" in h.memory.content for h in resp.results)

    st = c.stats("sdk-it")
    assert st.total_memories >= 2

    ml = c.list_memories(workspace="sdk-it")
    assert ml.total >= 2 and any(m.id == r.id for m in ml.memories)

    updated = c.update_memory(r.id, summary="deploy pin fact")
    assert updated["summary"] == "deploy pin fact"

    edge_id = c.link(r.id, ev.id)
    assert edge_id

    c.forget(r.id)
    resp = c.recall("quixplotron deploy pin", top_k=5, workspace="sdk-it")
    assert not any(h.memory.id == r.id for h in resp.results)

    with pytest.raises(LadymError) as ei:
        c.delete_memory(r.id)
    assert ei.value.status == 404
