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
