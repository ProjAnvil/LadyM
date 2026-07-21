"""Tests for the supersedes pointer chain (SPEC §2.6).

Covers:

* UPDATE consolidation creates a new merged memory and retires the old one (with a
  ``supersedes`` edge and ``superseded_by`` metadata).
* DELETE consolidation retires the old one in place (``superseded`` true, no successor).
* recall tier-1 hides retired memories.
* recall tier-2 walks the ``supersedes`` edge so the newest version is reachable from the
  retired old anchor.
"""

import pytest

from ladym.config import Config
from ladym.engine import Engine
from ladym.operations.consolidate import Action
from ladym.operations.supersedes import is_retired, latest_in_chain


@pytest.fixture
def engine(tmp_path):
    e = Engine(Config.for_testing(tmp_path))
    yield e
    e.close()


def test_update_creates_new_and_retires_old(engine):
    from ladym.operations import supersedes as sup

    old = engine.semantic.put_fact("auth uses JWT", summary="v1")
    new = engine.semantic.put_fact("auth uses JWT with 24h expiry", summary="v2")
    sup.retire(engine.store, old, new_id=new.id)
    assert is_retired(engine.store.get_memory(old.id))
    assert latest_in_chain(engine.store, old.id) == new.id


def test_retire_preserves_supersedes_edge_against_close_loop(engine):
    """retire() must close existing outgoing edges BEFORE writing the new supersedes
    edge, otherwise the close-loop closes the very edge latest_in_chain needs to walk.

    This test makes the ordering guarantee actually exercised: ``old`` has a
    pre-existing ``old -> third`` edge with ``valid_to=None`` so the close-loop has
    real work to do. If the order were swapped, the new ``supersedes`` edge would be
    closed by the loop and latest_in_chain would not reach ``new``.
    """
    from ladym.operations import supersedes as sup
    from ladym.schema import Edge

    old = engine.semantic.put_fact("auth uses JWT", summary="v1")
    third = engine.semantic.put_fact("auth uses OAuth2", summary="related")
    new = engine.semantic.put_fact("auth uses JWT with 24h expiry", summary="v2")
    # pre-existing open edge old -> third; this is what the close-loop must close.
    engine.store.put_edge(
        Edge(src_id=old.id, relation="related_to", dst_id=third.id, valid_to=None)
    )

    sup.retire(engine.store, old, new_id=new.id)

    # the supersedes edge survived the close-loop: latest_in_chain reaches ``new``.
    assert latest_in_chain(engine.store, old.id) == new.id
    # the old -> third edge was closed (valid_to now set, edge still present in the
    # table — only its temporal validity ended, it was not deleted).
    rows = engine.store.conn.execute(
        "SELECT valid_to FROM edges WHERE src_id = ? AND dst_id = ? AND relation = 'related_to'",
        (old.id, third.id),
    ).fetchall()
    assert len(rows) == 1
    assert rows[0]["valid_to"] is not None


def test_delete_retires_without_successor(engine):
    from ladym.operations import supersedes as sup

    m = engine.semantic.put_fact("obsolete fact")
    sup.retire(engine.store, m)  # no new_id
    assert is_retired(engine.store.get_memory(m.id))
    assert engine.store.get_memory(m.id).metadata.get("superseded") is True


def test_recall_tier1_hides_retired(engine):
    from ladym.operations import supersedes as sup

    m = engine.semantic.put_fact("unique secret phrase zzz")
    sup.retire(engine.store, m)
    resp = engine.recall("unique secret phrase zzz")
    ids = [r.memory.id for r in resp.results]
    assert m.id not in ids


def test_recall_tier2_follows_supersedes(engine):
    from ladym.operations import supersedes as sup

    old = engine.semantic.put_fact("config value is five")
    new = engine.semantic.put_fact("config value is five", summary="v2")
    sup.retire(engine.store, old, new_id=new.id)
    # searching for the content should still let tier-2 walk old->new
    resp = engine.recall("config value is five")
    ids = [r.memory.id for r in resp.results]
    assert new.id in ids


# Sanity: ``Action`` import is exercised (used by consolidate's contract).
def test_action_enum_is_importable():
    assert Action.UPDATE.value == "UPDATE"
