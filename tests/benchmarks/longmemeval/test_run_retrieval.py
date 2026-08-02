import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import run_retrieval
from tests.benchmarks.longmemeval.fixtures import make_mini_dataset, make_mini_instance


class _FakeMemory:
    """Mirrors ladym.schema.Memory — only the .metadata field is exercised."""
    def __init__(self, metadata):
        self.metadata = metadata


class _FakeResult:
    """Faithful to ladym.schema.RecallResult: exposes .memory + .score, NOT .metadata."""
    def __init__(self, metadata, score=0.0):
        self.memory = _FakeMemory(metadata)
        self.score = score


class _FakeRecallResp:
    def __init__(self, metas):
        self.results = [_FakeResult(m) for m in metas]


class _FakeEngine:
    def __init__(self, metas_by_qid):
        self._m = metas_by_qid
    def recall(self, query, **kw):
        # hand back a fixed ranked list for whichever qid is being queried
        return _FakeRecallResp(self._m["_current"])
    def close(self):
        pass


def test_run_retrieval_emits_schema(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", top_k=50, base_dir=tmp_path)
    data = make_mini_dataset()
    # recall returns: gold turn first, then a distractor
    metas = [{"doc_id": "sess_1_0", "session_id": "sess_1"},
             {"doc_id": "sess_2_0", "session_id": "sess_2"}] * 25
    fake = _FakeEngine({})
    def factory(**kw):
        fake._m["_current"] = metas
        return fake
    out = run_retrieval.run_retrieval(data, cfg, engine_factory=factory)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    # abstention (mini_2_abs) is EXCLUDED
    ids = [l["question_id"] for l in lines]
    assert ids == ["mini_1"]
    m = lines[0]["retrieval_results"]["metrics"]
    assert set(m) == {"session", "turn"}
    assert "recall_all@50" in m["turn"]
    assert m["session"]["recall_all@5"] == 1.0  # sess_1 gold, ranked first
    # No failures -> run_report.json still emitted with an empty list.
    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert report == {"failures": []}


def test_run_retrieval_per_instance_fault_tolerance(tmp_path):
    """Important #2: per-instance try/except. One bad instance must not kill
    the whole run — the OTHER instance still emits a line, and the failure
    is recorded in run_report.json (spec requirement)."""
    cfg = BenchConfig(difficulty="oracle", variant="raw", top_k=50, base_dir=tmp_path)
    # 3 instances: mini_1 (good), bad_qid (recall raises), mini_3 (good).
    good = make_mini_instance()
    bad = make_mini_instance()
    bad["question_id"] = "bad_qid"
    third = make_mini_instance()
    third["question_id"] = "mini_3"
    dataset = [good, bad, third]

    metas = [{"doc_id": "sess_1_0", "session_id": "sess_1"}] * 25

    class _RoutingFactory:
        def __init__(self):
            self.calls = 0

        def __call__(self, **kw):
            self.calls += 1
            # The order of `dataset` is preserved; second engine built is the
            # bad instance. (run_retrieval builds a fresh engine per instance.)
            if self.calls == 2:
                raise RuntimeError("engine_factory blew up for this qid")
            eng = _FakeEngine({})
            eng._m["_current"] = metas
            return eng

    out = run_retrieval.run_retrieval(dataset, cfg, engine_factory=_RoutingFactory())
    ids = [json.loads(l)["question_id"] for l in out.read_text().splitlines()]
    # bad_qid is dropped; the two good ones still produce lines.
    assert ids == ["mini_1", "mini_3"]
    assert "bad_qid" not in ids

    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert len(report["failures"]) == 1
    assert report["failures"][0]["question_id"] == "bad_qid"
    assert "engine_factory blew up" in report["failures"][0]["error"]
