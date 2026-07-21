"""External embedding providers: Ollama, generic HTTP, and user callables.

All HTTP providers accept an injected ``client`` so tests never touch the network.
"""
from __future__ import annotations

import json
from typing import Any, Callable

from ..storage.embeddings import EmbeddingProvider


class FakeHTTPClient:
    """Test double for HTTP providers. ``responder`` maps payload -> JSON-able dict."""

    def __init__(self, responder: Callable[[dict], Any], expected_path: str = ""):
        self._responder = responder
        self.expected_path = expected_path
        self.last_payload: dict | None = None
        self.last_url: str | None = None

    def post(self, url: str, payload: dict, *, timeout: float = 10.0,
             headers: dict | None = None) -> Any:
        self.last_url = url
        self.last_payload = payload
        if self.expected_path and self.expected_path not in url:
            raise AssertionError(f"expected path {self.expected_path!r} in {url!r}")
        return self._responder(payload)


def _extract(obj: Any, path: str) -> Any:
    cur = obj
    for part in path.split("."):
        cur = cur[part]
    return cur


class OllamaEmbedding(EmbeddingProvider):
    def __init__(self, base_url: str, model: str, *, timeout_s: float = 10.0,
                 client: Any | None = None):
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.timeout_s = timeout_s
        self._client = client
        self._dim: int | None = None

    @property
    def dim(self) -> int:  # type: ignore[override]
        if self._dim is None:
            self._dim = len(self.embed("__dim_probe__"))
        return self._dim

    def _post(self, prompt: str) -> list[float]:
        payload = {"model": self.model, "prompt": prompt}
        client = self._client or _real_http_client()
        resp = client.post(f"{self.base_url}/api/embeddings", payload, timeout=self.timeout_s)
        return list(resp["embedding"])

    def embed(self, text: str) -> list[float]:
        vec = self._post(text)
        if self._dim is None:
            self._dim = len(vec)
        return vec

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]  # Ollama has no batch endpoint

    def health_check(self) -> tuple[bool, str]:
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"


class HttpEmbedding(EmbeddingProvider):
    def __init__(self, base_url: str, *, request_template: str, response_path: str,
                 dim: int, model: str = "", timeout_s: float = 10.0,
                 client: Any | None = None, headers: dict | None = None):
        self.base_url = base_url
        self.request_template = request_template
        self.response_path = response_path
        self._dim = dim
        self.model = model
        self.timeout_s = timeout_s
        self._client = client
        self.headers = headers or {}
        self.dim = dim  # user-declared; verified on first real call below

    def embed(self, text: str) -> list[float]:
        body = self.request_template.replace("{text}", json.dumps(text)[1:-1])
        payload = json.loads(body)
        client = self._client or _real_http_client()
        resp = client.post(self.base_url, payload, timeout=self.timeout_s, headers=self.headers)
        vec = list(_extract(resp, self.response_path))
        if len(vec) != self._dim:
            raise ValueError(f"dim mismatch: declared {self._dim}, got {len(vec)}")
        return vec

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]

    def health_check(self) -> tuple[bool, str]:
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"


class CallableEmbedding(EmbeddingProvider):
    def __init__(self, fn: Callable[[str], list[float]], dim: int):
        self._fn = fn
        self.dim = dim

    def embed(self, text: str) -> list[float]:
        return list(self._fn(text))

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]


def _real_http_client():  # pragma: no cover - exercised only with a real provider
    import httpx

    class _Httpx:
        def post(self, url, payload, *, timeout=10.0, headers=None):
            r = httpx.post(url, json=payload, timeout=timeout, headers=headers or {})
            r.raise_for_status()
            return r.json()

    return _Httpx()
