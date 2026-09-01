"""Synchronous ladyM client over MCP stdio (newline-delimited JSON-RPC 2.0).

Spawns the Go ``ladym serve`` subprocess and exposes its MCP tools as typed
blocking methods. Thread-safe via a single lock: concurrent callers are
serialized, which matches the server's one-request-in-flight stdio model.

Reads run on a background thread feeding a queue, so every RPC has a
configurable timeout (``select`` on pipes is not portable). On timeout or
server death the client goes dead: the subprocess is killed and all later
calls fail fast instead of queueing behind a corpse.

Zero third-party dependencies — stdlib only.
"""

from __future__ import annotations

import json
import logging
import os
import queue
import shutil
import subprocess
import threading
from collections import deque
from pathlib import Path
from typing import Any, Deque, Dict, List, Optional

__all__ = ["LadymClient", "LadymError", "find_ladym_binary"]

logger = logging.getLogger(__name__)

# The Go server caps scanner lines at 1 MiB (mcp/server.go); refuse to send
# requests anywhere near that instead of getting the connection killed.
MAX_REQUEST_BYTES = 900_000

DEFAULT_TIMEOUT_S = 30.0

_EOF = object()  # queue sentinel: server stdout closed


class LadymError(RuntimeError):
    """Raised when the ladym binary is missing, the server dies, or a tool call fails."""


def find_ladym_binary(explicit: Optional[str] = None) -> str:
    """Resolve the ``ladym`` Go binary.

    Order: explicit argument → ``LADYM_BIN`` env var → ``PATH``.
    """
    candidates = [
        explicit,
        os.environ.get("LADYM_BIN"),
        shutil.which("ladym"),
    ]
    for candidate in candidates:
        if candidate and Path(candidate).is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
    raise LadymError(
        "ladym binary not found; build it with `go build -o bin/ladym ./cmd/ladym` "
        "from the ladyM repo, install it onto PATH, or set LADYM_BIN"
    )


class LadymClient:
    """Blocking client for ``ladym serve`` (JSON-RPC 2.0 over stdio).

    Usage::

        client = LadymClient(db="/path/ladym.db", workspace="hermes")
        client.start()
        try:
            hits = client.recall("deployment steps")
        finally:
            client.close()
    """

    def __init__(
        self,
        binary: Optional[str] = None,
        db: Optional[str] = None,
        workspace: Optional[str] = None,
        extra_args: Optional[List[str]] = None,
        env: Optional[Dict[str, str]] = None,
        cwd: Optional[str] = None,
        timeout: float = DEFAULT_TIMEOUT_S,
    ) -> None:
        self._binary = find_ladym_binary(binary)
        self._argv = [self._binary, "serve"]
        if db is not None:
            self._argv += ["--db", str(db)]
        if workspace is not None:
            self._argv += ["--workspace", workspace]
        self._argv += list(extra_args or [])
        self._env = env  # None → inherit the parent environment
        self._cwd = cwd  # None → inherit the parent working directory
        self._timeout = timeout

        self._lock = threading.Lock()
        self._proc: Optional[subprocess.Popen] = None
        self._dead: Optional[str] = None  # death reason; None = healthy
        self._responses: "queue.Queue[Any]" = queue.Queue()
        self._next_id = 0
        self._stderr_tail: Deque[str] = deque(maxlen=50)
        self.server_info: Dict[str, Any] = {}
        self.protocol_version: str = ""

    # -- lifecycle ------------------------------------------------------------

    def start(self) -> None:
        """Spawn the server and perform the MCP initialize handshake."""
        with self._lock:
            if self._proc is not None:
                raise LadymError("client already started")
            self._dead = None
            try:
                self._proc = subprocess.Popen(
                    self._argv,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    text=True,
                    bufsize=1,
                    env=self._env,
                    cwd=self._cwd,
                )
            except OSError as exc:
                raise LadymError(f"failed to spawn {self._argv[0]}: {exc}") from exc
            threading.Thread(
                target=self._read_stdout, args=(self._proc,), daemon=True
            ).start()
            threading.Thread(
                target=self._drain_stderr, args=(self._proc,), daemon=True
            ).start()
            try:
                result = self._rpc_locked("initialize", {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {},
                    "clientInfo": {"name": "hermes-ladym-plugin", "version": "0.1.0"},
                })
                # Notification (no id): the server ignores it per the contract.
                self._send_locked({"jsonrpc": "2.0",
                                   "method": "notifications/initialized"})
            except Exception:
                # Never leak the subprocess on a failed handshake.
                self._mark_dead_locked("initialize handshake failed")
                raise
            self.server_info = result.get("serverInfo", {}) or {}
            self.protocol_version = result.get("protocolVersion", "")

    def close(self) -> None:
        """Terminate the server subprocess. Safe to call more than once and
        never blocks on an already-dead client."""
        with self._lock:
            proc, self._proc = self._proc, None
            self._dead = self._dead or "client closed"
        if proc is None:
            return
        try:
            if proc.stdin:
                proc.stdin.close()
        except OSError:
            pass
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)

    def _mark_dead_locked(self, reason: str) -> None:
        """Kill + reap the subprocess and latch the dead state (lock held)."""
        self._dead = reason
        proc, self._proc = self._proc, None
        if proc is not None and proc.poll() is None:
            proc.kill()
            try:
                proc.wait(timeout=5)
            except Exception:
                pass

    def _drain_stderr(self, proc: subprocess.Popen) -> None:
        if proc.stderr is None:
            return
        for line in proc.stderr:
            self._stderr_tail.append(line.rstrip())

    def _read_stdout(self, proc: subprocess.Popen) -> None:
        """Feed parsed response frames into the queue; EOF pushes a sentinel."""
        try:
            if proc.stdout is not None:
                for line in proc.stdout:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        self._responses.put(json.loads(line))
                    except json.JSONDecodeError:
                        continue  # not a response frame; skip
        finally:
            self._responses.put(_EOF)

    # -- RPC core ---------------------------------------------------------------

    def _send_locked(self, payload: Dict[str, Any]) -> None:
        proc = self._proc
        if proc is None or proc.stdin is None:
            raise LadymError("client not started")
        data = json.dumps(payload)
        size = len(data.encode("utf-8"))
        if size > MAX_REQUEST_BYTES:
            raise LadymError(
                f"request too large: {size} bytes (limit {MAX_REQUEST_BYTES}); "
                f"`ladym serve` accepts at most 1 MiB per line — shrink the "
                f"content instead of killing the connection"
            )
        try:
            proc.stdin.write(data + "\n")
            proc.stdin.flush()
        except (BrokenPipeError, OSError) as exc:
            raise LadymError(self._dead_msg(exc)) from exc

    def _dead_msg(self, exc: Optional[BaseException] = None) -> str:
        tail = "; ".join(self._stderr_tail) or "no stderr"
        base = f"ladym server died ({exc})" if exc else "ladym server died"
        return f"{base}; recent stderr: {tail}"

    def _rpc_locked(self, method: str, params: Optional[Dict[str, Any]]) -> Any:
        if self._dead:
            raise LadymError(self._dead)
        proc = self._proc
        if proc is None:
            raise LadymError("client not started")
        if proc.poll() is not None:
            self._mark_dead_locked(self._dead_msg())
            raise LadymError(self._dead or "ladym server died")
        self._next_id += 1
        rid = self._next_id
        self._send_locked({"jsonrpc": "2.0", "id": rid, "method": method,
                           "params": params or {}})
        while True:
            try:
                item = self._responses.get(timeout=self._timeout)
            except queue.Empty:
                self._mark_dead_locked(
                    f"ladym server timed out after {self._timeout}s "
                    f"waiting for a {method} response"
                )
                raise LadymError(self._dead or "ladym server timed out")
            if item is _EOF:
                self._mark_dead_locked(self._dead_msg(EOFError("EOF on stdout")))
                raise LadymError(self._dead or "ladym server died")
            if not isinstance(item, dict) or item.get("id") != rid:
                continue  # stray/notification frame; requests are serialized
            if "error" in item and item["error"]:
                err = item["error"]
                raise LadymError(f"{method} failed: {err.get('message', err)}")
            return item.get("result")

    def _rpc(self, method: str, params: Optional[Dict[str, Any]]) -> Any:
        with self._lock:
            return self._rpc_locked(method, params)

    # -- MCP methods ------------------------------------------------------------

    def ping(self) -> Any:
        return self._rpc("ping", {})

    def list_tools(self) -> List[str]:
        result = self._rpc("tools/list", {})
        return [t["name"] for t in (result or {}).get("tools", [])]

    def call_tool(self, name: str, arguments: Optional[Dict[str, Any]] = None) -> Any:
        """Call an MCP tool and return the parsed JSON payload.

        The server wraps payloads as ``content[0].text`` (a JSON string) and
        signals tool errors with ``isError: true`` — both are unwrapped here.
        """
        result = self._rpc("tools/call", {"name": name, "arguments": arguments or {}})
        content = (result or {}).get("content") or []
        text = ""
        for block in content:
            if block.get("type") == "text":
                text = block.get("text", "")
                break
        if result and result.get("isError"):
            message = text
            try:
                message = json.loads(text).get("error", text)
            except (json.JSONDecodeError, AttributeError):
                pass
            raise LadymError(f"{name} failed: {message}")
        if not text:
            return None
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            return text

    # -- typed tool wrappers (mirror `ladym serve` tools/list) -------------------

    @staticmethod
    def _args(**kwargs: Any) -> Dict[str, Any]:
        return {k: v for k, v in kwargs.items() if v is not None}

    def recall(self, query: str, top_k: Optional[int] = None,
               workspace: Optional[str] = None,
               code_only: Optional[bool] = None) -> Any:
        return self.call_tool("recall", self._args(
            query=query, top_k=top_k, workspace=workspace, code_only=code_only))

    def remember(self, content: str, tags: Optional[List[str]] = None,
                 source: Optional[str] = None,
                 workspace: Optional[str] = None) -> Any:
        return self.call_tool("remember", self._args(
            content=content, tags=tags, source=source, workspace=workspace))

    def record_event(self, agent: str, action: str,
                     observation: Optional[str] = None,
                     outcome: Optional[str] = None,
                     tags: Optional[List[str]] = None,
                     workspace: Optional[str] = None) -> Any:
        return self.call_tool("record_event", self._args(
            agent=agent, action=action, observation=observation,
            outcome=outcome, tags=tags, workspace=workspace))

    def search_code(self, query: str, top_k: Optional[int] = None,
                    workspace: Optional[str] = None) -> Any:
        return self.call_tool("search_code", self._args(
            query=query, top_k=top_k, workspace=workspace))

    def index_code(self, root: str, force: Optional[bool] = None,
                   languages: Optional[List[str]] = None,
                   workspace: Optional[str] = None) -> Any:
        return self.call_tool("index_code", self._args(
            root=root, force=force, languages=languages, workspace=workspace))

    def consolidate(self, workspace: Optional[str] = None) -> Any:
        return self.call_tool("consolidate", self._args(workspace=workspace))

    def stats(self, workspace: Optional[str] = None) -> Any:
        return self.call_tool("stats", self._args(workspace=workspace))

    def link(self, src: str, dst: str, relation: Optional[str] = None) -> Any:
        return self.call_tool("link", self._args(src=src, dst=dst, relation=relation))

    def forget(self, memory_id: str) -> Any:
        return self.call_tool("forget", {"memory_id": memory_id})
