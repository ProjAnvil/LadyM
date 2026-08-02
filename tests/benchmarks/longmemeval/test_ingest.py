from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import ingest
from tests.benchmarks.longmemeval.fixtures import make_mini_instance


class _FakeMemory:
    def __init__(self, metadata):
        self.metadata = metadata


class _FakeEngine:
    def __init__(self):
        self.recorded = []
        self.consolidated = False
    def record_event(self, *, agent, action, observation="", metadata=None, **kw):
        self.recorded.append({"agent": agent, "action": action, "metadata": metadata or {}})
    def consolidate(self, **kw):
        self.consolidated = True
    def stats(self):
        class S:
            total_memories = len(self.recorded)
        return S()
    def close(self):
        pass


def test_ingest_records_every_turn_in_order(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    inst = make_mini_instance()
    fake = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake)
    assert len(fake.recorded) == 4  # 2 sessions x 2 turns
    # timestamp order: sess_1 before sess_2
    assert fake.recorded[0]["metadata"]["session_id"] == "sess_1"
    assert fake.recorded[2]["metadata"]["session_id"] == "sess_2"
    # doc_id convention
    assert fake.recorded[0]["metadata"]["doc_id"] == "sess_1_0"
    # has_answer propagated
    assert fake.recorded[0]["metadata"]["has_answer"] is True
    assert fake.recorded[1]["metadata"]["has_answer"] is False
    # raw variant must NOT consolidate
    assert fake.consolidated is False


def test_ingest_user_assistant_agent(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    fake = _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.recorded[0]["agent"] == "user"
    assert fake.recorded[1]["agent"] == "assistant"


def test_consolidated_variant_runs_consolidate(tmp_path):
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    fake = _FakeEngine()
    # consolidated path uses a marker file, not memory-count skip
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake)
    assert fake.consolidated is True


def test_consolidated_skip_writes_marker_and_skips_second_call(tmp_path):
    """Carry-forward: cache-skip behaviour for the consolidated variant.

    On the first call, ingest runs and writes a `.done` marker. On the second
    call with the same config, ingest must skip WITHOUT touching the engine
    (no count probe, no record_event, no consolidate) — the marker alone guards
    re-runs because consolidation changes memory count post-ingest.
    """
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    qid = make_mini_instance()["question_id"]
    db_path = cfg.db_path_for(qid)

    fake1 = _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=lambda **kw: fake1)
    assert fake1.consolidated is True
    # marker must exist after a successful consolidated ingest
    assert db_path.with_suffix(".done").exists()

    # second call: factory must not be invoked at all (marker-based skip)
    calls = []
    def _tracking_factory(**kw):
        calls.append(kw)
        return _FakeEngine()
    ingest.ingest_instance(make_mini_instance(), cfg, engine_factory=_tracking_factory)
    assert calls == [], "consolidated skip must not probe the engine"


def test_force_reruns_ingest_even_when_marker_exists(tmp_path):
    """Carry-forward: force=True bypasses the marker cache and re-runs."""
    cfg = BenchConfig(difficulty="oracle", variant="consolidated", base_dir=tmp_path)
    inst = make_mini_instance()
    fake1 = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake1)
    assert fake1.consolidated is True

    # second call with force=True must re-run ingest
    fake2 = _FakeEngine()
    ingest.ingest_instance(inst, cfg, engine_factory=lambda **kw: fake2, force=True)
    assert len(fake2.recorded) == 4
    assert fake2.consolidated is True
