import json
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import run_retrieval
from tests.benchmarks.longmemeval.fixtures import make_mini_dataset


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
