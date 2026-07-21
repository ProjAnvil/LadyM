"""Attention gate (SPEC §2.7): pre-``remember`` filter on the write path.

The gate sits in front of every long-term write (L1/L2/L3) and decides one of three
outcomes:

* ``pass``    — store the content unchanged.
* ``rewrite`` — store a cleaner / canonicalised form supplied by the LLM.
* ``drop``    — do not persist at all.

L0 (working) memory is never gated: it is ephemeral scratch and the gate would only
add latency without value.

Two modes:

* **Heuristic** (default, ``provider="none"``): cheap and deterministic. Drops content
  that is too short, indistinguishable from a fixed noise vocabulary, or a near-clone
  of an L1 episodic event written within ``dedup_window_s`` seconds. Passes everything
  else.
* **LLM** (when ``agents.attention_gate`` resolves to a real provider): defers to a
  structured-output call that returns ``{action, content?, reason}``.

``Engine.remember`` consumes the :class:`GateDecision` directly: on ``drop`` it returns
an *unpersisted* :class:`~ladym.schema.Memory` tagged ``metadata={"gated": "dropped",
"reason": ...}`` so the call still satisfies the return-type contract (NFR-4); on
``rewrite`` it persists the rewritten text and tags ``metadata={"gated": "rewritten"}``.
"""

from __future__ import annotations

import hashlib
import time
from dataclasses import dataclass

from ..schema import Layer

# Built-in noise vocabulary. Tokens are lower-cased before comparison so this set
# must already be in lowercase. Extended per-deployment via ``Config.attention.noise_words``.
_BUILTIN_NOISE = frozenset({"lol", "ok", "test", "asdf", "foo", "bar", "todo"})


@dataclass
class GateDecision:
    """Outcome of the attention gate.

    ``action`` is one of ``"pass"``, ``"rewrite"``, ``"drop"``. ``content`` is only
    populated on ``rewrite`` (the new text to persist); ``reason`` is a short
    diagnostic carried through to ``Memory.metadata`` for observability.
    """

    action: str
    content: str | None = None
    reason: str = ""


def _hash(s: str) -> str:
    """Stable short hash for exact-content dedup."""
    return hashlib.blake2b(s.encode(), digest_size=8).hexdigest()


def attention_gate(content: str, *, engine, layer: Layer) -> GateDecision:
    """Apply the attention gate to ``content`` destined for ``layer``.

    Returns a :class:`GateDecision`. L0 working is always passed; otherwise the gate
    consults the LLM agent bound to ``attention_gate`` if any, falling back to the
    heuristic rules described in the module docstring.
    """
    cfg = engine.config
    if layer == Layer.WORKING:
        return GateDecision(action="pass", reason="working memory never gated")

    agent = getattr(engine, "_agents", {}).get("attention_gate")
    if agent is not None:
        return _llm_gate(agent, content)

    # ----- heuristic mode -----
    stripped = content.strip()
    if len(stripped) < cfg.attention.min_chars:
        return GateDecision(action="drop", reason="too short")

    tokens = {w.lower() for w in stripped.split()}
    noise = _BUILTIN_NOISE | set(cfg.attention.noise_words)
    if tokens and tokens <= noise:
        return GateDecision(action="drop", reason="noise")

    # Recent-duplicate: same content hash inside the dedup window against L1 events.
    # SPEC §2.7: keep the scan O(recent_rows) rather than O(all_episodes) by pushing the
    # time-window cut into SQL; hash-equality is then checked in Python (cheap, and stays
    # independent of the store's content_hash column which may be empty for legacy rows).
    now = time.time()
    window = cfg.attention.dedup_window_s
    needle = _hash(content)
    since = now - window
    cur = engine.store.conn.execute(
        "SELECT content FROM memories "
        "WHERE workspace = ? AND layer = ? AND created_at >= ?",
        (cfg.workspace, Layer.EPISODIC.value, since),
    )
    for row in cur:
        if _hash(row["content"]) == needle:
            return GateDecision(action="drop", reason="recent duplicate")

    return GateDecision(action="pass")


def _llm_gate(provider, content: str) -> GateDecision:
    """LLM-backed gate. Defers to ``provider.complete_structured`` for the decision."""
    from pydantic import BaseModel

    class _G(BaseModel):
        action: str
        content: str | None = None
        reason: str = ""

    msgs = [
        {
            "role": "system",
            "content": (
                "Decide if the user content is worth storing long-term. "
                "Reply JSON {action, content?, reason}. "
                "action in pass|rewrite|drop. "
                "rewrite returns the cleaned-up text in `content`."
            ),
        },
        {"role": "user", "content": content},
    ]
    d = provider.complete_structured(msgs, _G)
    return GateDecision(
        action=d["action"],
        content=d.get("content"),
        reason=d.get("reason", ""),
    )
