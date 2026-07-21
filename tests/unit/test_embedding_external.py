from ladym.providers.embeddings_http import (
    CallableEmbedding,
    FakeHTTPClient,
    HttpEmbedding,
    OllamaEmbedding,
)


def test_ollama_embedding_posts_to_api_embeddings():
    client = FakeHTTPClient(
        responder=lambda payload: {"embedding": [0.1, 0.2, 0.3]},
        expected_path="/api/embeddings",
    )
    emb = OllamaEmbedding("http://localhost:11434", "nomic-embed-text", client=client)
    v = emb.embed("hello")
    assert v == [0.1, 0.2, 0.3]
    assert emb.dim == 3
    assert client.last_payload["model"] == "nomic-embed-text"
    assert client.last_payload["prompt"] == "hello"


def test_ollama_health_check_reports_failure():
    client = FakeHTTPClient(responder=lambda p: (_ for _ in ()).throw(ConnectionError("nope")))
    emb = OllamaEmbedding("http://localhost:11434", "x", client=client)
    ok, msg = emb.health_check()
    assert ok is False
    assert "ConnectionError" in msg


def test_http_embedding_uses_response_path():
    client = FakeHTTPClient(responder=lambda p: {"data": {"vector": [1.0, 0.0]}})
    emb = HttpEmbedding(
        "https://example.test/embed", request_template='{"input": "{text}"}',
        response_path="data.vector", dim=2, client=client,
    )
    assert emb.embed("q") == [1.0, 0.0]


def test_callable_embedding_wraps_user_function():
    emb = CallableEmbedding(fn=lambda text: [float(len(text)), 0.0], dim=2)
    assert emb.embed("abcd") == [4.0, 0.0]
    assert emb.dim == 2


def test_external_batch_matches_singletons():
    client = FakeHTTPClient(responder=lambda p: {"embedding": [0.5, 0.5]})
    emb = OllamaEmbedding("http://x", "m", client=client)
    batch = emb.embed_batch(["a", "b"])
    assert batch == [emb.embed("a"), emb.embed("b")]
