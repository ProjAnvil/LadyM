#!/usr/bin/env python3
"""Fake ``ladym serve`` MCP server for client tests.

Speaks newline-delimited JSON-RPC 2.0 over stdio, mirroring mcp/server.go:
requests without an ``id`` are ignored; ``tools/call`` results wrap a JSON
payload as ``content[0].text``; tool failures set ``isError: true`` with a
``{"error": ...}`` text payload. Canned data only — no memory logic.

Special tools / modes:
  explode     — always returns an isError result.
  die         — exits the process without replying (crash simulation).
  hang        — never replies in time (timeout simulation; sleeps 30s).
  FAKE_LADYM_DIE_ON_INIT=1 in the environment — exit before the handshake.
"""

import json
import os
import sys
import time

TOOLS = [
    "recall", "remember", "record_event", "search_code", "index_code",
    "consolidate", "stats", "link", "forget",
]

RECALL_PAYLOAD = {
    "query": "",
    "tier_reached": 1,
    "reflected_sufficient": True,
    "elapsed_ms": 1,
    "results": [
        {
            "score": 0.9,
            "tier": 1,
            "via": "bm25",
            "memory": {
                "id": "mem-fake-1",
                "layer": "L2",
                "type": "fact",
                "summary": "fake summary one",
                "content": "fake content one",
                "source": "test",
                "tags": ["fake"],
            },
        },
        {
            "score": 0.5,
            "tier": 1,
            "via": "bm25",
            "memory": {
                "id": "mem-fake-2",
                "layer": "L2",
                "type": "fact",
                "summary": "fake summary two",
                "content": "fake content two",
                "source": "test",
                "tags": [],
            },
        },
    ],
}


def tool_payload(name, args):
    if name == "recall":
        payload = dict(RECALL_PAYLOAD)
        payload["query"] = args.get("query", "")
        return payload, False
    if name == "remember":
        return {"id": "mem-new-1", "hash": "deadbeef"}, False
    if name == "record_event":
        return {"id": "evt-1", "layer": "L1", "type": "episode"}, False
    if name == "search_code":
        return {"results": [], "elapsed_ms": 0}, False
    if name == "index_code":
        return {"files_seen": 0, "files_indexed": 0}, False
    if name == "consolidate":
        return {"kept_episodes": 0, "promoted_to_semantic": 0}, False
    if name == "stats":
        return {"memories": 2, "workspaces": ["test"]}, False
    if name == "link":
        return {"id": "edge-1", "src": args.get("src"), "dst": args.get("dst")}, False
    if name == "forget":
        return {"forgotten": args.get("memory_id", "")}, False
    if name == "explode":
        return {"error": "boom: fake tool failure"}, True
    return {"error": "unknown tool: %s" % name}, True


def reply(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def main():
    if os.environ.get("FAKE_LADYM_DIE_ON_INIT"):
        os._exit(1)
    # Swallow argv (serve --db ... --workspace ...) like the real binary.
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "id" not in req or req["id"] is None:
            continue  # notification: no response
        rid = req["id"]
        method = req.get("method")
        if method == "initialize":
            reply({
                "jsonrpc": "2.0", "id": rid,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "ladym", "version": "fake-0.0.0"},
                },
            })
        elif method == "ping":
            reply({"jsonrpc": "2.0", "id": rid, "result": {}})
        elif method == "tools/list":
            reply({
                "jsonrpc": "2.0", "id": rid,
                "result": {"tools": [
                    {"name": t, "description": "fake", "inputSchema": {"type": "object"}}
                    for t in TOOLS
                ]},
            })
        elif method == "tools/call":
            params = req.get("params") or {}
            name = params.get("name")
            if name == "die":
                os._exit(1)
            if name == "hang":
                time.sleep(30)  # client under test must time out and kill us
            payload, is_error = tool_payload(name, params.get("arguments") or {})
            reply({
                "jsonrpc": "2.0", "id": rid,
                "result": {
                    "content": [{"type": "text", "text": json.dumps(payload)}],
                    "isError": is_error,
                },
            })
        else:
            reply({
                "jsonrpc": "2.0", "id": rid,
                "error": {"code": -32603, "message": "method not found: %s" % method},
            })


if __name__ == "__main__":
    main()
