"""supersedes pointer chain (SPEC §2.6).

Retired memories stay in the store (so lineage / audit is preserved) but are flagged so:

* ``UPDATE`` consolidation: a NEW merged memory is created, the OLD target is marked with
  ``metadata.superseded_by = <new.id>`` and a ``supersedes`` edge old→new is written. The
  old target is no longer returned by tier-1 recall, but tier-2 walks the edge to surface
  the newest version.
* ``DELETE`` consolidation: the target is marked ``metadata.superseded = true`` and has no
  successor. Tier-1 hides it; tier-2 has no edge to follow.
"""

from __future__ import annotations

import time

from ..schema import Edge, Memory
from ..storage.store import SQLiteStore


def is_retired(mem: Memory | None) -> bool:
    """True iff ``mem`` was retired by an UPDATE or DELETE consolidation pass.

    ``None`` (memory gone) is treated as not-retired so callers can keep applying the
    filter inline without a separate None-guard.
    """
    if mem is None:
        return False
    meta = mem.metadata or {}
    return bool(meta.get("superseded_by") or meta.get("superseded"))


def retire(store: SQLiteStore, old: Memory, *, new_id: str | None = None) -> None:
    """Retire ``old``.

    * ``new_id`` given  → UPDATE chain: write ``superseded_by`` + a ``supersedes`` edge
      so :func:`latest_in_chain` can walk to the successor.
    * ``new_id`` is None → DELETE retirement: set ``superseded=true`` and write no edge.

    Outgoing still-valid edges of ``old`` are closed (``valid_to=now``) so the graph does
    not leak through a retired node.
    """
    now = time.time()
    old.metadata = {**(old.metadata or {}), "superseded_at": now}
    if new_id:
        old.metadata["superseded_by"] = new_id
    else:
        old.metadata["superseded"] = True
    # close still-open outgoing edges first so neighbours do not keep flowing through a
    # retired node. Done BEFORE writing the new ``supersedes`` edge so we don't close
    # the very edge we are about to create (recall relies on it staying open).
    for e in store.neighbors(old.id):
        if e.valid_to is None:
            e.valid_to = now
            store.put_edge(e)
    if new_id:
        store.put_edge(
            Edge(src_id=old.id, relation="supersedes", dst_id=new_id, valid_from=now)
        )
    store.put_memory(old)


def latest_in_chain(store: SQLiteStore, mem_id: str) -> str:
    """Walk ``supersedes`` edges forward and return the id of the newest version.

    Stops at the head (a memory with no outgoing ``supersedes`` edge) and guards against
    cycles by tracking the visited set.
    """
    seen: set[str] = set()
    cur = mem_id
    while cur not in seen:
        seen.add(cur)
        nxt = [
            e
            for e in store.neighbors(cur, relation="supersedes")
            if e.src_id == cur
        ]
        if not nxt:
            return cur
        cur = nxt[0].dst_id
    return cur
