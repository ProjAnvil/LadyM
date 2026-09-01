"""ladyM memory provider for Hermes Agent.

Bridges Hermes to a local ladyM (brain-inspired multi-layer memory) instance
via the ``ladym serve`` MCP stdio server. All state lives under
``<hermes_home>/ladym/``, so Hermes profiles stay isolated and
``hermes backup`` covers the database.
"""

from __future__ import annotations

import json
import logging
import os
import threading
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple

from agent.memory_provider import MemoryProvider, RecallStatus, is_trivial_prompt

from .ladym_client import LadymClient, LadymError, find_ladym_binary

logger = logging.getLogger(__name__)

CONFIG_FILENAME = "ladym.json"
DEFAULT_WORKSPACE = "hermes"
DEFAULT_RECALL_TOP_K = 5
_TRUNCATE_LIMIT = 1000
# Max time prefetch() waits for an in-flight/uncached recall before giving up
# and injecting nothing — memory must never stall a turn.
PREFETCH_WAIT_BUDGET_S = 2.0

_CONFIG_KEYS = ("ladym_bin", "workspace", "recall_top_k", "sync_turns", "prefetch")

_TRUE_WORDS = {"true", "1", "yes", "on"}
_FALSE_WORDS = {"false", "0", "no", "off"}


def _truncate(text: str, limit: int = _TRUNCATE_LIMIT) -> str:
    text = text or ""
    if len(text) <= limit:
        return text
    return text[:limit] + "…"


def default_hermes_home() -> str:
    """HERMES_HOME resolution shared by is_available() and the CLI."""
    return os.environ.get("HERMES_HOME") or str(Path.home() / ".hermes")


def load_config(hermes_home: str) -> Dict[str, Any]:
    """Read <hermes_home>/ladym.json; malformed content warns, never raises."""
    path = Path(hermes_home) / CONFIG_FILENAME
    if not path.is_file():
        return {}
    try:
        data = json.loads(path.read_text())
    except (json.JSONDecodeError, OSError) as exc:
        logger.warning("ladym: ignoring malformed %s: %s", path, exc)
        return {}
    if not isinstance(data, dict):
        logger.warning("ladym: ignoring non-object config in %s", path)
        return {}
    return data


def _parse_int(value: Any, default: int, key: str) -> int:
    if value is None:
        return default
    try:
        return int(value)
    except (TypeError, ValueError):
        logger.warning("ladym: invalid %s=%r, using default %d", key, value, default)
        return default


def _parse_bool(value: Any, default: bool, key: str) -> bool:
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        lowered = value.strip().lower()
        if lowered in _TRUE_WORDS:
            return True
        if lowered in _FALSE_WORDS:
            return False
    logger.warning("ladym: invalid %s=%r, using default %s", key, value, default)
    return default


def _parse_str(value: Any, default: str, key: str) -> str:
    if value is None:
        return default
    if isinstance(value, str) and value:
        return value
    logger.warning("ladym: invalid %s=%r, using default %r", key, value, default)
    return default


class LadymMemoryProvider(MemoryProvider):
    """Hermes memory provider backed by a local ladyM MCP server.

    ``client_factory`` is a test seam: a callable accepting
    ``binary=``, ``db=``, ``workspace=`` keyword arguments and returning an
    object with the :class:`LadymClient` interface.
    """

    def __init__(self, client_factory: Optional[Callable[..., Any]] = None) -> None:
        self._client_factory = client_factory
        self._client: Any = None
        self._session_id = ""
        self._binary: Optional[str] = None
        self._workspace = DEFAULT_WORKSPACE
        self._recall_top_k = DEFAULT_RECALL_TOP_K
        self._sync_turns = True
        self._prefetch_enabled = True
        self._primary = True
        self._last_recall_count: Optional[int] = None
        # queue_prefetch/prefetch cache: recall for the upcoming turn runs on a
        # daemon thread; prefetch() consumes the cached block.
        self._prefetch_lock = threading.Lock()
        self._prefetch_inflight_query: Optional[str] = None
        self._prefetch_thread: Optional[threading.Thread] = None
        self._prefetch_cache_query: Optional[str] = None
        self._prefetch_cache_block: str = ""
        self._prefetch_cache_count: int = 0

    # -- identity / availability ---------------------------------------------

    @property
    def name(self) -> str:
        return "ladym"

    def is_available(self) -> bool:
        """True when the ladym binary resolves. No network, no subprocess.

        Resolution chain: ``ladym_bin`` from $HERMES_HOME/ladym.json
        (default ~/.hermes) → LADYM_BIN env → PATH.
        """
        cfg = load_config(default_hermes_home())
        configured = cfg.get("ladym_bin")
        try:
            find_ladym_binary(configured if isinstance(configured, str) else None)
            return True
        except LadymError:
            return False

    def unavailable_reason(self) -> str:
        return (
            "ladym binary not found; run `hermes ladym install` to download "
            "it (add --fulldict for the embedded CJK dictionary), install it "
            "onto PATH, or set LADYM_BIN"
        )

    # -- lifecycle -------------------------------------------------------------

    def initialize(self, session_id: str, **kwargs: Any) -> None:
        hermes_home = kwargs.get("hermes_home")
        if not hermes_home:
            raise ValueError("initialize() requires the hermes_home kwarg")
        self._session_id = session_id

        cfg = load_config(hermes_home)
        binary = cfg.get("ladym_bin")
        self._binary = binary if isinstance(binary, str) and binary else None
        workspace = kwargs.get("agent_workspace") or cfg.get("workspace")
        self._workspace = _parse_str(workspace, DEFAULT_WORKSPACE, "workspace")
        self._recall_top_k = _parse_int(
            cfg.get("recall_top_k"), DEFAULT_RECALL_TOP_K, "recall_top_k")
        self._sync_turns = _parse_bool(cfg.get("sync_turns"), True, "sync_turns")
        self._prefetch_enabled = _parse_bool(cfg.get("prefetch"), True, "prefetch")

        context = kwargs.get("agent_context") or "primary"
        # Non-primary contexts (subagent/cron/flush) skip writes: their
        # prompts would pollute the user's memory store.
        self._primary = context == "primary"

        db_dir = Path(hermes_home) / "ladym"
        db_dir.mkdir(parents=True, exist_ok=True)
        factory = self._client_factory or (lambda **kw: LadymClient(**kw))
        self._client = factory(
            binary=self._binary,
            db=str(db_dir / "ladym.db"),
            workspace=self._workspace,
        )
        self._client.start()

    def shutdown(self) -> None:
        client, self._client = self._client, None
        if client is not None:
            try:
                client.close()
            except Exception as exc:  # never let shutdown crash the agent
                logger.warning("ladym: error closing client: %s", exc)

    def backup_paths(self) -> List[str]:
        # The DB lives under hermes_home, so `hermes backup` already covers it.
        return []

    # -- per-turn hooks ----------------------------------------------------------

    def system_prompt_block(self) -> str:
        return (
            "Persistent memory is provided by ladyM (local multi-layer memory "
            "engine). Relevant memories are recalled automatically before each "
            "turn; you can also search/store explicitly with the ladym_* tools."
        )

    def queue_prefetch(self, query: str, *, session_id: str = "") -> None:
        """Recall for the NEXT turn in the background; prefetch() consumes it."""
        if self._client is None or not self._prefetch_enabled:
            return
        if is_trivial_prompt(query):
            return
        with self._prefetch_lock:
            self._prefetch_inflight_query = query

        def work() -> None:
            block, count = self._recall_block(query)
            with self._prefetch_lock:
                # Drop the result if a newer queue_prefetch superseded us.
                if self._prefetch_inflight_query == query:
                    self._prefetch_cache_query = query
                    self._prefetch_cache_block = block
                    self._prefetch_cache_count = count

        t = threading.Thread(target=work, daemon=True)
        with self._prefetch_lock:
            self._prefetch_thread = t
        t.start()

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        self._last_recall_count = None
        if self._client is None or not self._prefetch_enabled:
            return ""
        if is_trivial_prompt(query):
            return ""
        with self._prefetch_lock:
            pending = self._prefetch_thread
            pending_matches = self._prefetch_inflight_query == query
        # Wait briefly for an in-flight background recall for THIS query.
        if pending is not None and pending.is_alive() and pending_matches:
            pending.join(timeout=PREFETCH_WAIT_BUDGET_S)
        with self._prefetch_lock:
            if self._prefetch_cache_query == query:
                if self._prefetch_cache_block:
                    self._last_recall_count = self._prefetch_cache_count
                return self._prefetch_cache_block
        # No cache (first turn): bounded synchronous recall — never stall a
        # turn longer than the budget.
        box: Dict[str, Tuple[str, int]] = {}

        def work() -> None:
            box["result"] = self._recall_block(query)

        t = threading.Thread(target=work, daemon=True)
        t.start()
        t.join(timeout=PREFETCH_WAIT_BUDGET_S)
        if t.is_alive():
            logger.warning("ladym: recall exceeded %.1fs budget, skipping injection",
                           PREFETCH_WAIT_BUDGET_S)
            return ""
        block, count = box.get("result", ("", 0))
        with self._prefetch_lock:
            self._prefetch_cache_query = query
            self._prefetch_cache_block = block
            self._prefetch_cache_count = count
        if not block:
            return ""
        self._last_recall_count = count
        return block

    def _recall_block(self, query: str) -> Tuple[str, int]:
        """Run recall and format the markdown block. Returns ("", 0) on any
        failure or empty result — memory must never break a turn."""
        try:
            resp = self._client.recall(
                query, top_k=self._recall_top_k, workspace=self._workspace
            )
        except Exception as exc:
            logger.warning("ladym: recall failed: %s", exc)
            return "", 0
        results = (resp or {}).get("results") or []
        if not results:
            return "", 0
        lines = ["## Recalled memories (ladyM)", ""]
        for hit in results:
            mem = hit.get("memory") or {}
            summary = mem.get("summary") or mem.get("content") or ""
            content = mem.get("content") or ""
            score = hit.get("score", 0)
            lines.append(f"- **{summary}** (score {score:.2f})")
            if content and content != summary:
                lines.append(f"  {content}")
        return "\n".join(lines), len(results)

    def recall_status(self) -> Optional[RecallStatus]:
        if self._last_recall_count is None:
            return None
        return RecallStatus(provider_label="ladyM", count=self._last_recall_count)

    def sync_turn(
        self,
        user_content: str,
        assistant_content: str,
        *,
        session_id: str = "",
        messages: Optional[List[Dict[str, Any]]] = None,
    ) -> None:
        if self._client is None or not self._sync_turns or not self._primary:
            return
        observation = _truncate(user_content)
        outcome = _truncate(assistant_content)
        workspace = self._workspace

        def work() -> None:
            try:
                self._client.record_event(
                    agent="hermes", action="conversation_turn",
                    observation=observation, outcome=outcome,
                    tags=["hermes"], workspace=workspace,
                )
            except Exception as exc:
                logger.warning("ladym: record_event failed: %s", exc)

        threading.Thread(target=work, daemon=True).start()

    def on_session_end(self, messages: List[Dict[str, Any]]) -> None:
        self._consolidate_in_background()

    def on_pre_compress(self, messages: List[Dict[str, Any]]) -> str:
        self._consolidate_in_background()
        return ""

    def on_session_switch(self, new_session_id: str, *, reset: bool = False,
                          **kwargs: Any) -> None:
        self._session_id = new_session_id
        if reset:
            self._last_recall_count = None

    def _consolidate_in_background(self) -> None:
        if self._client is None or not self._primary:
            return
        workspace = self._workspace

        def work() -> None:
            try:
                self._client.consolidate(workspace=workspace)
            except Exception as exc:
                logger.warning("ladym: consolidate failed: %s", exc)

        threading.Thread(target=work, daemon=True).start()

    # -- tools -----------------------------------------------------------------

    def get_tool_schemas(self) -> List[Dict[str, Any]]:
        def schema(name: str, description: str,
                   properties: Dict[str, Any], required: List[str]) -> Dict[str, Any]:
            return {
                "name": name,
                "description": description,
                "parameters": {
                    "type": "object",
                    "properties": properties,
                    "required": required,
                },
            }

        str_list = {"type": "array", "items": {"type": "string"}}
        return [
            schema("ladym_recall",
                   "Recall long-term memories matching a natural-language query. "
                   "Use at the start of a task to surface relevant prior context.",
                   {"query": {"type": "string"},
                    "top_k": {"type": "integer", "default": 5},
                    "code_only": {"type": "boolean", "default": False}},
                   ["query"]),
            schema("ladym_remember",
                   "Store a durable fact/note in long-term memory for future recall.",
                   {"content": {"type": "string"},
                    "tags": str_list,
                    "source": {"type": "string", "default": "hermes"}},
                   ["content"]),
            schema("ladym_record_event",
                   "Record an episodic event (agent, action, observation, outcome) "
                   "for later consolidation into semantic memory.",
                   {"agent": {"type": "string"},
                    "action": {"type": "string"},
                    "observation": {"type": "string"},
                    "outcome": {"type": "string"},
                    "tags": str_list},
                   ["agent", "action"]),
            schema("ladym_search_code",
                   "Search indexed code symbols and file summaries by keyword.",
                   {"query": {"type": "string"},
                    "top_k": {"type": "integer", "default": 10}},
                   ["query"]),
            schema("ladym_index_code",
                   "Index (or re-index) a codebase so ladym_search_code can find "
                   "its symbols and summaries.",
                   {"root": {"type": "string"},
                    "force": {"type": "boolean", "default": False}},
                   ["root"]),
            schema("ladym_forget",
                   "Delete a single memory by id.",
                   {"memory_id": {"type": "string"}},
                   ["memory_id"]),
        ]

    def handle_tool_call(self, tool_name: str, args: Dict[str, Any], **kwargs: Any) -> str:
        args = args or {}
        if self._client is None:
            return json.dumps({"error": "ladym provider not initialized"})
        # Write tools (remember/record_event/forget) intentionally stay enabled
        # in non-primary contexts: unlike the passive sync_turn/consolidate
        # writes (which are skipped there), these are explicit model-initiated
        # actions the operator asked for.
        ws = args.get("workspace") or self._workspace
        try:
            if tool_name == "ladym_recall":
                result = self._client.recall(
                    args["query"], top_k=args.get("top_k"),
                    workspace=ws, code_only=args.get("code_only"))
            elif tool_name == "ladym_remember":
                result = self._client.remember(
                    args["content"], tags=args.get("tags"),
                    source=args.get("source"), workspace=ws)
            elif tool_name == "ladym_record_event":
                result = self._client.record_event(
                    args["agent"], args["action"],
                    observation=args.get("observation"),
                    outcome=args.get("outcome"),
                    tags=args.get("tags"), workspace=ws)
            elif tool_name == "ladym_search_code":
                result = self._client.search_code(
                    args["query"], top_k=args.get("top_k"), workspace=ws)
            elif tool_name == "ladym_index_code":
                result = self._client.index_code(
                    args["root"], force=args.get("force"), workspace=ws)
            elif tool_name == "ladym_forget":
                result = self._client.forget(args["memory_id"])
            else:
                return json.dumps({"error": f"unknown tool: {tool_name}"})
        except KeyError as exc:
            return json.dumps({"error": f"missing argument: {exc}"})
        except Exception as exc:
            return json.dumps({"error": str(exc)})
        return json.dumps(result)

    # -- configuration ------------------------------------------------------------

    def get_config_schema(self) -> List[Dict[str, Any]]:
        return [
            {
                "key": "ladym_bin",
                "description": "Path to the ladym binary (default: LADYM_BIN env or PATH)",
                "type": "text",
                "required": False,
            },
            {
                "key": "workspace",
                "description": "ladyM workspace name for this agent",
                "type": "text",
                "default": DEFAULT_WORKSPACE,
                "required": False,
            },
            {
                "key": "recall_top_k",
                "description": "Max memories injected per turn via prefetch",
                "type": "integer",
                "default": DEFAULT_RECALL_TOP_K,
                "minimum": 1,
                "maximum": 50,
                "required": False,
            },
            {
                "key": "sync_turns",
                "description": "Record each conversation turn as an episodic event",
                "type": "boolean",
                "default": True,
                "required": False,
            },
            {
                "key": "prefetch",
                "description": "Recall and inject relevant memories before each turn",
                "type": "boolean",
                "default": True,
                "required": False,
            },
        ]

    def save_config(self, values: Dict[str, Any], hermes_home: str) -> None:
        path = Path(hermes_home) / CONFIG_FILENAME
        existing: Dict[str, Any] = {}
        if path.is_file():
            try:
                existing = json.loads(path.read_text())
            except json.JSONDecodeError:
                existing = {}
        for key in _CONFIG_KEYS:
            if key in values:
                existing[key] = values[key]
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(existing, indent=2) + "\n")
