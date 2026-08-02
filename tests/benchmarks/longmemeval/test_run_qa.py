"""Phase B tests: run_qa emits hypothesis.jsonl with RAG answers + abstention."""
import json

from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import run_qa
from tests.benchmarks.longmemeval.fixtures import make_mini_dataset


class _FakeMemory:
    """Mirrors ladym.schema.Memory — only .content + .metadata are exercised."""
    def __init__(self, content, metadata=None):
        self.content = content
        self.metadata = metadata or {}


class _FakeResult:
    """Faithful to ladym.schema.RecallResult: exposes .memory + .score,
    NOT direct .content/.metadata (which would mask a regression)."""
    def __init__(self, content, score, metadata=None):
        self.memory = _FakeMemory(content, metadata)
        self.score = score


class _FakeResp:
    def __init__(self, results):
        self.results = results


class _FakeEngine:
    def __init__(self, results):
        self._r = results
    def recall(self, query, **kw):
        return _FakeResp(self._r)
    def close(self):
        pass


def test_run_qa_emits_hypothesis(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    res = [
        _FakeResult("user said blue", 0.9, {"date": "2024-01-01", "session_id": "sess_1"}),
        _FakeResult("assistant agreed", 0.5, {"date": "2024-01-02", "session_id": "sess_1"}),
    ]
    eng = _FakeEngine(res)
    calls = []
    def fake_llm(system, user):
        calls.append(user)
        return "blue"
    out = run_qa.run_qa(data, cfg, engine_factory=lambda **kw: eng, answer_llm=fake_llm)
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    assert {l["question_id"] for l in lines} == {"mini_1", "mini_2_abs"}
    hyps = {l["question_id"]: l["hypothesis"] for l in lines}
    assert hyps["mini_1"] == "blue"
    # RAG context should include the recalled content + metadata
    assert calls, "answer_llm must be invoked for non-abstaining questions"
    assert "user said blue" in calls[0]


def test_run_qa_abstention_when_low_score(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    data = make_mini_dataset()
    # abstention q with near-zero recall score -> "I don't know."
    res = [_FakeResult("irrelevant", 0.01)]
    eng = _FakeEngine(res)
    called = []
    out = run_qa.run_qa(
        data, cfg, engine_factory=lambda **kw: eng,
        answer_llm=lambda s, u: (called.append(1), "no")[1],
    )
    lines = {
        json.loads(l)["question_id"]: json.loads(l)["hypothesis"]
        for l in out.read_text().splitlines()
    }
    assert lines["mini_2_abs"] == "I don't know."
