import json
from types import SimpleNamespace

import pytest

from longmemeval import _runtime, metrics, run_retrieval
from longmemeval.config import BenchConfig
from lme_fixtures import make_mini_instance


def test_runtime_provenance_sidecar(monkeypatch, tmp_path):
    raw = {"doc_id": "a_0", "session_id": "a", "date": "2026-11-08", "custom": {"raw": True}}
    second = {"doc_id": "b_0", "session_id": "b"}
    payload = [
        {"id": "raw", "metadata": {"session_id": "wrong", "knowledge_status": "observed", "secret": "hidden"}},
        {"id": "old"},
        {"id": "one", "metadata": {"source_episode": "raw", "knowledge_status": "inferred"}},
        {"id": "multi", "metadata": {"source_episode": "raw", "source_episodes": ["raw", "second", "raw", "unknown"]}},
        {"id": "unknown", "metadata": {"source_episodes": ["missing"]}},
        {"id": "unrelated"},
        {"id": "collision", "metadata": {"source_episode": "wrong", "session_id": "wrong", "verified_at": 123}},
        {"id": "same-doc", "metadata": {"source_episodes": ["old", "second", "second"]}},
    ]

    class Client:
        def __init__(self, **kw):
            pass

        def recall(self, query, top_k=None):
            return {"results": [{"memory": {**m, "content": m["id"]}, "score": 0.9} for m in payload]}

        def close(self):
            pass

    monkeypatch.setattr(_runtime, "LadymClient", Client)
    eng = _runtime.make_engine(tmp_path / "x.db", "ws")
    eng._sidecar = {
        "raw": raw, "old": second, "second": second, "distractor": {"session_id": "unrelated"},
        "collision": {"source_episode": "second", "session_id": "direct"},
    }
    try:
        recalled = [r.memory.metadata for r in eng.recall("query").results]
    finally:
        eng.close()
    assert recalled[0] == {**raw, "knowledge_status": "observed"}
    assert recalled[1] == second  # legacy metadata stays byte-for-byte compatible
    assert recalled[2] == {
        "source_episode": "raw", "knowledge_status": "inferred",
        "evidence_sources": [{"memory_id": "raw", **raw}],
    }
    assert recalled[3] == {
        "source_episode": "raw", "source_episodes": ["raw", "second", "raw", "unknown"],
        "evidence_sources": [{"memory_id": "raw", **raw}, {"memory_id": "second", **second}],
    }
    assert recalled[4] == {"source_episodes": ["missing"], "evidence_sources": []}
    assert recalled[5] == {}
    assert recalled[6] == {
        "source_episode": "second", "session_id": "direct", "verified_at": 123,
        "evidence_sources": [{"memory_id": "second", **second}],
    }
    assert recalled[7] == {
        "source_episodes": ["old", "second", "second"],
        "evidence_sources": [{"memory_id": "old", **second}, {"memory_id": "second", **second}],
    }  # same doc ID is not a duplicate source memory ID
    assert raw == {"doc_id": "a_0", "session_id": "a", "date": "2026-11-08", "custom": {"raw": True}}


def test_evidence_metrics_slot_cutoff(monkeypatch):
    monkeypatch.setattr(metrics, "_TURN_KS", [1, 2, 3])
    monkeypatch.setattr(metrics, "_SESSION_KS", [1, 2, 3])
    build = getattr(metrics, "build_evidence_metric_dict", None)
    assert callable(build), "grouped source metrics are missing"
    for groups, expected in [
        ([{"a", "b"}], [(1.0, 1.0), (1.0, 0.5), (1.0, 0.5)]),
        ([{"a", "b"}, {"a", "b"}], [(1.0, 1.0), (1.0, 0.5), (1.0, 0.5)]),
        ([set(), {"a"}, {"b"}], [(0.0, 0.0), (0.0, 0.5), (1.0, 0.8154648767857288)]),
        ([{"a"}, {"a"}, {"b"}], [(0.0, 1.0), (0.0, 0.5), (1.0, 0.8154648767857288)]),
    ]:
        result = build(groups, {"a", "b"}, groups, {"a", "b"})
        for scope in ("turn", "session"):
            for k, (recall, ndcg) in enumerate(expected, 1):
                assert result[scope][f"recall_all@{k}"] == recall
                assert result[scope][f"ndcg_any@{k}"] == pytest.approx(ndcg)
                assert 0 <= result[scope][f"ndcg_any@{k}"] <= 1
    empty = build([{"a"}], set(), [set()], set())
    assert all(v == 0 for scope in empty.values() for v in scope.values())


def test_evidence_metrics_legacy_stable():
    legacy = metrics.build_metric_dict(["a", "a", "b"], {"a", "b"}, {"s"}, ["s", "x"])
    expected = {
        "session": {"recall_all@5": 1.0, "ndcg_any@5": 1.0, "recall_all@10": 1.0, "ndcg_any@10": 1.0},
        "turn": {"recall_all@5": 1.0, "ndcg_any@5": 1.3154648767857289,
                 "recall_all@10": 1.0, "ndcg_any@10": 1.3154648767857289,
                 "recall_all@50": 1.0, "ndcg_any@50": 1.3154648767857289},
    }
    assert json.dumps(legacy) == json.dumps(expected)
    build = getattr(metrics, "build_evidence_metric_dict", None)
    assert callable(build), "grouped source metrics are missing"
    unique = metrics.build_metric_dict(["a", "x", "b"], {"a", "b"}, {"s", "t"}, ["s", "x", "t"])
    assert build([{"a"}, {"x"}, {"b"}], {"a", "b"}, [{"s"}, {"x"}, {"t"}], {"s", "t"}) == unique


def test_retrieval_emits_both_metrics(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", top_k=10, base_dir=tmp_path)
    instance = make_mini_instance()
    instance["answer_session_ids"] = ["sess_1", "sess_2"]
    instance["haystack_sessions"][1][0]["has_answer"] = True
    metas = [
        {"evidence_sources": []},
        {"evidence_sources": [{"memory_id": "m1", "doc_id": "sess_1_0", "session_id": "sess_1"},
                              {"memory_id": "m2", "doc_id": "sess_2_0", "session_id": "sess_2"}]},
        {"evidence_sources": [{"memory_id": "m1", "doc_id": "sess_1_0", "session_id": "sess_1"}]},
        {}, {},
        {"doc_id": "sess_1_0", "session_id": "sess_1"},  # rank six must not slide inside K=5
    ]

    class Engine:
        def recall(self, query, top_k=None):
            assert top_k == 10
            return SimpleNamespace(results=[SimpleNamespace(memory=SimpleNamespace(metadata=m)) for m in metas])

        def close(self):
            pass

    out = run_retrieval.run_retrieval([instance], cfg, engine_factory=lambda **kw: Engine())
    row = json.loads(out.read_text())
    assert row["question_id"] == instance["question_id"]
    results = row["retrieval_results"]
    assert set(results) == {"metrics", "evidence_metrics"}
    assert results["metrics"]["turn"]["recall_all@5"] == 0.0
    assert results["metrics"]["turn"]["ndcg_any@5"] == 0.0
    for scope in ("turn", "session"):
        assert results["evidence_metrics"][scope]["recall_all@5"] == 1.0
        assert results["evidence_metrics"][scope]["ndcg_any@5"] == 0.5
    assert json.loads((cfg.results_dir / "run_report.json").read_text()) == {"failures": []}


@pytest.mark.parametrize("metas,recall5,ndcg5", [
    ([{}] * 5 + [{"doc_id": "sess_1_0", "session_id": "sess_1"}], 0.0, 0.0),
    ([{"evidence_sources": [{"doc_id": f"noise_{i}", "session_id": f"noise_{i}"} for i in range(10)]},
      {}, {}, {}, {"doc_id": "sess_1_0", "session_id": "sess_1"}], 1.0, 0.43067655807339306),
    ([{"doc_id": "noise_0", "session_id": "noise"}] * 5 +
     [{"doc_id": "sess_1_0", "session_id": "sess_1"}], 0.0, 0.0),
])
def test_retrieval_memory_slots_not_flat_sources_or_dedup_sessions(tmp_path, metas, recall5, ndcg5):
    cfg = BenchConfig(difficulty="oracle", variant="raw", top_k=10, base_dir=tmp_path)

    class Engine:
        def recall(self, query, **kw):
            return SimpleNamespace(results=[SimpleNamespace(memory=SimpleNamespace(metadata=m)) for m in metas])

        def close(self):
            pass

    out = run_retrieval.run_retrieval([make_mini_instance()], cfg, engine_factory=lambda **kw: Engine())
    result = json.loads(out.read_text())["retrieval_results"]
    for scope in ("turn", "session"):
        assert result["evidence_metrics"][scope]["recall_all@5"] == recall5
        assert result["evidence_metrics"][scope]["ndcg_any@5"] == pytest.approx(ndcg5)
        assert result["evidence_metrics"][scope]["recall_all@10"] == 1.0
    if len(metas) == 6:
        assert result["metrics"]["session"]["recall_all@5"] == 1.0
        assert result["metrics"]["turn"]["recall_all@5"] == 0.0
