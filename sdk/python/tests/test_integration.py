"""Integration test against a real `ladym serve --http` binary.

Gated on LADYM_SDK_IT=1 (same spirit as the Go-side LADYM_TEST_PG_DSN tests):
without it every test here skips. Requires a Go toolchain on PATH to build
./cmd/ladym into a temp dir.
"""

from __future__ import annotations

import os
import hashlib
import socket
import subprocess
import time
import urllib.request
from pathlib import Path

import pytest

from ladym_client import Client, ClaimFeedbackRequest, CreateClaimRequest, EvidenceRef, LadymError, RecordEvidenceRequest

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
    config_path = tmp / "archive.toml"
    config_path.write_text("[archive]\nenabled=true\n", encoding="utf-8")
    proc = subprocess.Popen(
        [str(binary), "--config", str(config_path), "serve", "--db", str(tmp / "it.db"), "--http", f"127.0.0.1:{port}"],
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

    evidence = c.record_evidence(RecordEvidenceRequest(kind="test_result", content="passed", outcome="success", workspace="sdk-it"))
    refs = [EvidenceRef(memory_id=evidence.id, fingerprint=hashlib.sha256(b"passed").hexdigest(), kind="test_result")]
    claim = c.create_claim(CreateClaimRequest(content="works", evidence_refs=refs, workspace="sdk-it"))
    verified = c.apply_claim_feedback(claim.memory.id, ClaimFeedbackRequest(expected_revision=1, action="verify", method="attested", evidence_refs=refs, workspace="sdk-it"))
    assert verified.revision == 2 and verified.verification.actor == "local"
    assert c.get_claim(claim.memory.id, "sdk-it").status == "verified"
    with pytest.raises(LadymError) as conflict:
        c.apply_claim_feedback(claim.memory.id, ClaimFeedbackRequest(expected_revision=1, action="reject", reason="bad", workspace="sdk-it"))
    assert conflict.value.status == 409
    archived = c.archive_evidence(RecordEvidenceRequest(kind="observation", content='{"token":"synthetic-secret"}', workspace="sdk-it"))
    assert "synthetic-secret" not in archived.content
    assert archived.metadata["archive_marker"] == "ladym.archive.v1"
    c.forget(evidence.id)
    assert c.get_claim(claim.memory.id, "sdk-it").memory.content == ""
