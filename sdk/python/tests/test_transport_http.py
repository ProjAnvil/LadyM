"""Scenario tests for the default urllib transport over a loopback
http.server (hermetic — no external network)."""

from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from ladym_client import Client, LadymError


class _Handler(BaseHTTPRequestHandler):
    """Records the request and replays a canned response."""

    seen = None  # last request: dict(method, path, headers, body)
    respond_status = 200
    respond_body = b'{"status":"ok"}'
    respond_content_type = "application/json"

    def _handle(self):
        length = int(self.headers.get("Content-Length") or 0)
        type(self).seen = {
            "method": self.command,
            "path": self.path,
            "headers": dict(self.headers),
            "body": self.rfile.read(length) if length else b"",
        }
        self.send_response(type(self).respond_status)
        self.send_header("Content-Type", type(self).respond_content_type)
        self.end_headers()
        self.wfile.write(type(self).respond_body)

    do_GET = _handle
    do_POST = _handle
    do_PUT = _handle
    do_DELETE = _handle

    def log_message(self, *args):  # keep test output quiet
        pass


@pytest.fixture
def loopback():
    _Handler.seen = None
    _Handler.respond_status = 200
    _Handler.respond_body = b'{"status":"ok"}'
    _Handler.respond_content_type = "application/json"
    srv = HTTPServer(("127.0.0.1", 0), _Handler)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    yield f"http://127.0.0.1:{srv.server_port}"
    srv.shutdown()
    t.join()
    srv.server_close()


def test_real_transport_get_with_query_encoding(loopback):
    c = Client(loopback)
    c.list_memories(workspace="ws 1", limit=5)
    assert _Handler.seen["method"] == "GET"
    assert _Handler.seen["path"].startswith("/api/memories?")
    assert "workspace=ws+1" in _Handler.seen["path"]
    assert "limit=5" in _Handler.seen["path"]
    assert _Handler.seen["body"] == b""
    assert "Authorization" not in _Handler.seen["headers"]


def test_real_transport_post_sends_json_body_and_content_type(loopback):
    c = Client(loopback, username="alice", password="pw")
    c.remember("a fact with unicode: 部署", tags=["t"], workspace="ws")
    seen = _Handler.seen
    assert seen["method"] == "POST"
    assert seen["headers"]["Content-Type"] == "application/json"
    body = json.loads(seen["body"])
    assert body["content"] == "a fact with unicode: 部署"
    assert body["workspace"] == "ws"
    assert seen["headers"]["Authorization"].startswith("Basic ")


def test_real_transport_error_maps_http_error_to_ladym_error(loopback):
    _Handler.respond_status = 401
    _Handler.respond_body = b'{"error":"unauthorized"}'
    c = Client(loopback)
    with pytest.raises(LadymError) as ei:
        c.ping()
    assert ei.value.status == 401 and ei.value.message == "unauthorized"


def test_real_transport_non_json_error_page_falls_back_to_text(loopback):
    _Handler.respond_status = 502
    _Handler.respond_body = b"<html>Bad Gateway</html>"
    _Handler.respond_content_type = "text/html"
    c = Client(loopback)
    with pytest.raises(LadymError) as ei:
        c.ping()
    assert ei.value.status == 502 and "Bad Gateway" in ei.value.message


def test_real_transport_timeout_is_passed(loopback):
    # urllib surfaces an unresponsive server as a timeout error; a 5s timeout
    # on a healthy loopback server must not interfere with the happy path.
    c = Client(loopback, timeout=5.0)
    assert c.ping() is None


def test_parse_json_edge_cases():
    from ladym_client.client import _parse_json

    assert _parse_json(b"") == {}
    assert _parse_json(b"   ") == {}
    assert _parse_json(b'[1, 2]') == {}  # valid JSON but not an object
    assert _parse_json(b"not json") == {"error": "not json"}
    assert _parse_json(b'{"a": 1}') == {"a": 1}
