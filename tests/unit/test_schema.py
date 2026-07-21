"""Schema sanity tests."""

from ladym.schema import Edge, Layer, Memory, MemoryType


def test_memory_defaults():
    m = Memory(layer=Layer.EPISODIC, type=MemoryType.EVENT, content="did a thing")
    assert m.id  # auto-generated
    assert m.layer == Layer.EPISODIC.value
    assert m.type == MemoryType.EVENT.value
    assert m.created_at > 0
    assert m.access_count == 0
    assert m.workspace == "default"


def test_memory_touch_increments():
    m = Memory(layer=Layer.WORKING, type=MemoryType.NOTE, content="x")
    before = (m.last_access_at, m.access_count)
    m.touch()
    assert m.access_count == before[1] + 1
    assert m.last_access_at >= before[0]


def test_layer_enum_has_seven_layers():
    layers = {layer.value for layer in Layer}
    assert layers == {
        "L0_working",
        "L1_episodic",
        "L2_semantic",
        "L3_procedural",
        "L4_associative",
        "L5_mental",
        "L6_predictive",
    }


def test_edge_defaults():
    e = Edge(src_id="a", relation="calls", dst_id="b")
    assert e.valid_to is None  # still valid
    assert e.weight == 1.0


def test_new_layers_and_types_exist():
    from ladym.schema import Layer, MemoryType
    assert Layer.L5_MENTAL.value == "L5_mental"
    assert Layer.L6_PREDICTIVE.value == "L6_predictive"
    assert MemoryType.MENTAL_MODEL.value == "mental_model"
    assert MemoryType.FORWARD_INTENT.value == "forward_intent"


def test_stats_has_density(tmp_path):
    from ladym.config import Config
    from ladym.engine import Engine
    e = Engine(Config.for_testing(tmp_path))
    try:
        e.semantic.put_fact("alpha beta gamma delta")
        s = e.stats()
        assert s.avg_tokens_per_memory > 0
    finally:
        e.close()
