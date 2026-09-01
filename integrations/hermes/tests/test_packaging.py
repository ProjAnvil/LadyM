"""Packaging verification: build the wheel and check the Hermes entry point.

Builds ``integrations/hermes`` with hatchling (no isolation — the dev venv
already has build+hatchling), installs the wheel into a throwaway target dir,
and in a fresh interpreter verifies that the
``hermes_agent.memory_providers`` entry point group contains ``ladym`` and
that ``ep.load()`` yields a working ``register(ctx)``.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

import pytest

PLUGIN_DIR = Path(__file__).resolve().parents[1]

_VERIFY_SCRIPT = r"""
import sys, types
from dataclasses import dataclass

sys.path.insert(0, sys.argv[1])

# fake agent.memory_provider so ladym_hermes imports without hermes-agent
agent = types.ModuleType("agent")
mp = types.ModuleType("agent.memory_provider")

class MemoryProvider:
    pass

@dataclass(frozen=True)
class RecallStatus:
    provider_label: str
    count: int
    glyph: str = "x"

mp.MemoryProvider = MemoryProvider
mp.RecallStatus = RecallStatus
mp.is_trivial_prompt = lambda t: True
agent.memory_provider = mp
sys.modules["agent"] = agent
sys.modules["agent.memory_provider"] = mp

import importlib.metadata as md

eps = md.entry_points()
if hasattr(eps, "select"):
    group = eps.select(group="hermes_agent.memory_providers")
else:  # Python 3.9 dict interface
    group = eps.get("hermes_agent.memory_providers", [])
matches = [ep for ep in group if ep.name == "ladym"]
assert matches, "no 'ladym' entry point in hermes_agent.memory_providers"
register = matches[0].load()

class Ctx:
    def __init__(self):
        self.providers = []
    def register_memory_provider(self, p):
        self.providers.append(p)

ctx = Ctx()
register(ctx)
assert len(ctx.providers) == 1
assert ctx.providers[0].name == "ladym"

import ladym_hermes.cli  # CLI module must ship in the wheel too
assert callable(ladym_hermes.cli.register_cli)

print("entry point OK:", type(ctx.providers[0]).__module__)
"""


def _has_build_tooling() -> bool:
    import importlib.util

    return all(
        importlib.util.find_spec(m) is not None for m in ("build", "hatchling")
    )


@pytest.mark.skipif(
    not _has_build_tooling(),
    reason="build+hatchling not installed in the test venv",
)
def test_wheel_builds_and_entry_point_loads(tmp_path):
    dist = tmp_path / "dist"
    build = subprocess.run(
        [sys.executable, "-m", "build", "--wheel", "--no-isolation",
         "--outdir", str(dist), str(PLUGIN_DIR)],
        capture_output=True, text=True,
    )
    assert build.returncode == 0, build.stderr[-2000:]
    wheels = list(dist.glob("ladym_hermes-*.whl"))
    assert len(wheels) == 1, f"unexpected dist contents: {list(dist.iterdir())}"

    target = tmp_path / "target"
    install = subprocess.run(
        [sys.executable, "-m", "pip", "install", "--no-deps", "--quiet",
         "--target", str(target), str(wheels[0])],
        capture_output=True, text=True,
    )
    assert install.returncode == 0, install.stderr[-2000:]

    verify = subprocess.run(
        [sys.executable, "-c", _VERIFY_SCRIPT, str(target)],
        capture_output=True, text=True,
    )
    assert verify.returncode == 0, verify.stderr[-2000:]
    assert "entry point OK" in verify.stdout
