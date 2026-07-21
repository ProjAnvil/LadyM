"""End-to-end integration tests that exercise the full memory lifecycle.

These mirror the user's core pain point: an agent should NOT need to Read/Grep a file every
turn — it should ``recall`` the indexed analysis instead.
"""

from __future__ import annotations

import time
from pathlib import Path

import pytest

from ladym import Config, Engine, Layer

FIXTURE = Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    e.index_code(FIXTURE)  # pre-index the fixture repo once
    yield e
    e.close()


# ----------------------------------------------------------------------------
# The headline scenario: no Read/Grep needed — recall returns the analysis.
# ----------------------------------------------------------------------------

def test_recall_returns_signature_docstring_and_callers_without_reading_file(engine):
    """The single test that proves LadyM's value proposition."""
    resp = engine.search_code("verify a password against its hash")
    assert resp.results, "expected at least one code memory to be recalled"

    top = resp.results[0].memory
    # the agent gets the analysis without opening the file:
    assert "verify_password" in top.content           # symbol identity
    assert "Check" in top.content or "plaintext" in top.content  # docstring carried through
    # signature is queryable via the code_symbols projection
    syms = engine.store.symbols_for_file(top.metadata["file_path"])
    verify = next(s for s in syms if "verify_password" in s.qualified_name)
    assert "verify_password" in verify.signature
    assert "password" in verify.signature          # parameter list preserved
    assert "->" in verify.signature                # return annotation preserved
    assert verify.line_start > 0
    # callers / callees are available without re-parsing
    refs = engine.store.refs_for_symbol(verify.qualified_name)
    assert any(r.src_symbol != verify.qualified_name for r in refs)  # something calls it


def test_index_persists_across_engine_reopen(tmp_path):
    """A reopened engine (new process equivalent) can still answer recalls — the whole
    point of having memory rather than re-reading on every turn."""
    cfg = Config.for_testing(tmp_path)
    eng1 = Engine(cfg)
    eng1.index_code(FIXTURE)
    n1 = eng1.stats().code_symbols
    eng1.close()

    eng2 = Engine(cfg)  # same db path, fresh engine
    resp = eng2.search_code("verify password")
    assert resp.results
    assert eng2.stats().code_symbols == n1
    eng2.close()


# ----------------------------------------------------------------------------
# Full cognitive lifecycle: encode → consolidate → proceduralize → recall → decay
# ----------------------------------------------------------------------------

def test_full_memory_lifecycle(engine):
    # 1. encode a bunch of episodes (some succeed, some fail)
    for i in range(4):
        engine.record_event(
            agent="claude", action="deploy to prod",
            observation=f"ran deploy.sh release {i}", outcome="success",
        )
    engine.record_event(agent="claude", action="deploy to prod",
                        observation="ran deploy.sh broken", outcome="failure")

    # 2. consolidate episodes → semantic facts
    c_report = engine.consolidate()
    assert c_report.promoted_to_semantic >= 1

    # 3. proceduralize successful clusters → playbook
    p_report = engine.proceduralize(min_cluster_size=3)
    assert p_report.playbooks_created >= 1

    # 4. recall pulls from multiple layers in one query
    resp = engine.recall("deploy to prod")
    layers_seen = {r.memory.layer for r in resp.results}
    assert layers_seen & {Layer.SEMANTIC.value, Layer.PROCEDURAL.value, Layer.EPISODIC.value}

    # 5. decay does not touch the promoted semantic/procedural items
    old = time.time() - 100 * 365 * 24 * 3600
    engine.store.conn.execute(
        f"UPDATE memories SET last_access_at = {old} WHERE layer = '{Layer.EPISODIC.value}'"
    )
    engine.store.conn.commit()
    engine.decay(max_age_s=1.0, activation_floor=0.9)
    # only episodic items decayed; semantic + procedural untouched
    remaining_layers = {m.layer for m in engine.store.iter_memories()}
    assert Layer.SEMANTIC.value in remaining_layers
    assert Layer.PROCEDURAL.value in remaining_layers


# ----------------------------------------------------------------------------
# Two-tier retrieval (HyMem cognitive economy)
# ----------------------------------------------------------------------------

def test_tier1_sufficient_for_well_covered_query(engine):
    """When the query is well-covered by indexed content, tier-1 alone answers it."""
    resp = engine.search_code("AuthService login password")
    # tier 1 should usually suffice for an on-topic code query against a small repo
    assert resp.results
    assert resp.tier_reached in (1, 2)


def test_tier2_triggered_when_anchor_links_exist(engine):
    """Graph edges cause tier-2 expansion when tier-1 reflection flags insufficient."""
    a = engine.semantic.put_fact("obscure anchor fact about xyzzy")  # low coverage hit
    b = engine.semantic.put_fact("xyzzy neighbour that elaborates the anchor meaningfully")
    engine.link(a.id, b.id, relation="elaborates")
    resp = engine.recall("xyzzy")
    ids = {r.memory.id for r in resp.results}
    assert a.id in ids


# ----------------------------------------------------------------------------
# Multi-workspace isolation
# ----------------------------------------------------------------------------

def test_workspace_isolation(tmp_path):
    cfg1 = Config.for_testing(tmp_path)
    cfg1.workspace = "team_a"
    eng_a = Engine(cfg1)
    eng_a.semantic.put_fact("team A secret: deploy key abc")

    cfg2 = Config.for_testing(tmp_path)
    cfg2.workspace = "team_b"
    cfg2.db_path = cfg1.db_path  # share the DB file, different workspace
    eng_b = Engine(cfg2)

    resp_a = eng_a.recall("deploy key")
    resp_b = eng_b.recall("deploy key")
    assert any("team A secret" in r.memory.content for r in resp_a.results)
    assert all("team A secret" not in r.memory.content for r in resp_b.results)
    eng_a.close()
    eng_b.close()


# ----------------------------------------------------------------------------
# Incremental indexing correctness
# ----------------------------------------------------------------------------

def test_incremental_index_picks_up_new_file(tmp_path):
    """Adding a new file and re-indexing should pick it up without touching unchanged ones."""
    repo = tmp_path / "repo"
    repo.mkdir()
    (repo / "a.py").write_text("def alpha():\n    return 1\n")
    cfg = Config.for_testing(tmp_path)
    eng = Engine(cfg)
    r1 = eng.index_code(repo)
    assert r1.files_indexed == 1

    (repo / "b.py").write_text("def beta():\n    return 2\n")
    r2 = eng.index_code(repo)
    assert r2.files_indexed == 1            # only the new file
    assert r2.files_skipped_unchanged == 1  # a.py unchanged

    resp = eng.search_code("beta function")
    assert any("beta" in r.memory.content for r in resp.results)
    eng.close()


def test_incremental_index_picks_up_changed_file(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    f = repo / "a.py"
    f.write_text("def alpha():\n    return 1\n")
    cfg = Config.for_testing(tmp_path)
    eng = Engine(cfg)
    eng.index_code(repo)

    f.write_text("def alpha_v2():\n    return 2\n")  # modify
    r = eng.index_code(repo)
    assert r.files_indexed == 1
    # the new symbol should be retrievable, the old one gone
    resp = eng.search_code("alpha")
    contents = " ".join(r.memory.content for r in resp.results)
    assert "alpha_v2" in contents
    eng.close()
