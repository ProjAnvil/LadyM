"""Tests for the activation function."""

import math

from ladym.config import ActivationWeights
from ladym.operations.activation import (
    activation_score,
    frequency_factor,
    infer_query_types,
    recency_factor,
)
from ladym.schema import Layer, Memory, MemoryType


def _mem(layer=Layer.SEMANTIC, type_=MemoryType.FACT, **kw) -> Memory:
    base = {"layer": layer, "type": type_, "content": "x"}
    base.update(kw)
    return Memory(**base)


def test_recency_factor_decays_with_age():
    now = 1000.0
    half_life = 100.0
    just_now = recency_factor(now, half_life_s=half_life, now=now)
    one_half = recency_factor(now - half_life, half_life_s=half_life, now=now)
    two_half = recency_factor(now - 2 * half_life, half_life_s=half_life, now=now)
    assert just_now == 1.0
    assert abs(one_half - 0.5) < 1e-6
    assert abs(two_half - 0.25) < 1e-6


def test_frequency_factor_log_curve():
    assert frequency_factor(0) == 0.0
    assert frequency_factor(1) == math.log(2)
    assert frequency_factor(100) > frequency_factor(1)


def test_activation_score_rewards_similarity():
    m = _mem()
    w = ActivationWeights(recency=0.0, frequency=0.0, graph=0.0, type_boost=0.0)
    high = activation_score(m, query_similarity=0.9, weights=w)
    low = activation_score(m, query_similarity=0.1, weights=w)
    assert high > low


def test_activation_score_rewards_recency():
    now = 1000.0
    fresh = _mem(last_access_at=now - 1)
    stale = _mem(last_access_at=now - 10_000_000)
    w = ActivationWeights(similarity=0.0, frequency=0.0, graph=0.0, type_boost=0.0)
    assert activation_score(fresh, query_similarity=0.0, weights=w, now=now) > \
           activation_score(stale, query_similarity=0.0, weights=w, now=now)


def test_activation_score_rewards_frequency():
    many = _mem(access_count=50)
    few = _mem(access_count=0)
    w = ActivationWeights(similarity=0.0, recency=0.0, graph=0.0, type_boost=0.0)
    assert activation_score(many, query_similarity=0.0, weights=w) > \
           activation_score(few, query_similarity=0.0, weights=w)


def test_activation_score_graph_boost():
    m = _mem()
    w = ActivationWeights(similarity=0.0, recency=0.0, frequency=0.0, type_boost=0.0, graph=0.5)
    solo = activation_score(m, query_similarity=0.0, weights=w, neighbour_counts={})
    linked = activation_score(
        m, query_similarity=0.0, weights=w, neighbour_counts={m.id: 5}
    )
    assert linked > solo


def test_infer_query_types_detects_code():
    assert MemoryType.CODE_SYMBOL in infer_query_types("where is the login function defined?")


def test_infer_query_types_detects_playbook():
    types = infer_query_types("how do i deploy the service")
    assert MemoryType.PLAYBOOK in types


def test_infer_query_types_default_empty():
    assert infer_query_types("what did the user say") == []
