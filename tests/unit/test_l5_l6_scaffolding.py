"""Scaffolding: System2Config L5/L6 knobs + packaged prompt files."""

from __future__ import annotations

from importlib.resources import files

from ladym.config import Config


def test_system2_config_l5_l6_defaults():
    s2 = Config().system2
    assert s2.l5_cluster_similarity == 0.65
    assert s2.l5_min_cluster_size == 3
    assert s2.l5_merge_similarity == 0.80
    assert s2.l5_merge_every_n_cycles == 5
    assert s2.l6_max_episodes == 50
    assert s2.l6_horizon_s == 3 * 24 * 3600.0


def test_prompts_are_packaged_and_readable():
    prompts = files("ladym.prompts")
    assert "mental model" in (prompts / "l5.txt").read_text().lower()
    assert "intent" in (prompts / "l6.txt").read_text().lower()
