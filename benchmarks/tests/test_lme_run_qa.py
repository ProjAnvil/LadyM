"""Phase B tests: run_qa emits hypothesis.jsonl with RAG answers + abstention."""
import json

from longmemeval.config import BenchConfig
from longmemeval import run_qa
from lme_fixtures import make_mini_dataset


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
    # Empty failures list -> run_report.json still emitted.
    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert report == {"failures": []}


def test_run_qa_per_instance_fault_tolerance(tmp_path):
    """Important #2: per-instance try/except. One bad instance must not kill
    the whole run — the OTHER instance still emits a hypothesis line, and
    the failure is recorded in run_report.json (spec requirement)."""
    import copy
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    good = make_mini_dataset()[0]                      # mini_1
    bad = copy.deepcopy(good)
    bad["question_id"] = "bad_qid"                     # non-abstention
    dataset = [good, bad]

    res = [_FakeResult("user said blue", 0.9, {"date": "d", "session_id": "s"})]

    class _RoutingFactory:
        def __init__(self):
            self.calls = 0

        def __call__(self, **kw):
            self.calls += 1
            if self.calls == 2:
                raise RuntimeError("recall blew up for bad_qid")
            return _FakeEngine(res)

    out = run_qa.run_qa(
        dataset, cfg, engine_factory=_RoutingFactory(),
        answer_llm=lambda s, u: "blue",
    )
    lines = [json.loads(l) for l in out.read_text().splitlines()]
    ids = [l["question_id"] for l in lines]
    # bad_qid is dropped; mini_1 still produced a line.
    assert ids == ["mini_1"]
    assert "bad_qid" not in ids

    report = json.loads((cfg.results_dir / "run_report.json").read_text())
    assert len(report["failures"]) == 1
    assert report["failures"][0]["question_id"] == "bad_qid"
    assert "recall blew up" in report["failures"][0]["error"]
