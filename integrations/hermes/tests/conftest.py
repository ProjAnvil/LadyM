"""Pytest fixtures for the ladym Hermes plugin tests.

Injects a fake ``agent.memory_provider`` module into ``sys.modules`` so the
plugin can be imported and tested without a Hermes Agent installation. The
fake mirrors the real contract (NousResearch/hermes-agent,
agent/memory_provider.py): a ``MemoryProvider`` ABC, a frozen ``RecallStatus``
dataclass, and ``is_trivial_prompt``.
"""

from __future__ import annotations

import re
import sys
import types
from abc import ABC
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, List, Optional

# Make both import forms available:
# - ``integrations/`` so ``import hermes`` loads the directory-plugin shim
# - ``integrations/hermes/src`` so ``import ladym_hermes`` matches the
#   pip-installed wheel layout
_HERE = Path(__file__).resolve()
sys.path.insert(0, str(_HERE.parents[2]))
sys.path.insert(0, str(_HERE.parents[1] / "src"))


def _install_fake_agent_module() -> None:
    if "agent.memory_provider" in sys.modules:
        return

    TRIVIAL_PROMPT_RE = re.compile(
        r"^(yes|no|ok|okay|sure|thanks|thank you|y|n|yep|nope|yeah|nah|"
        r"hi|hey|hello|yo|sup|"
        r"continue|go ahead|do it|proceed|got it|cool|nice|great|done|next|lgtm|k)"
        r"[\s!?.:;,\"'~…()\[\]{}<>*&^%$#@!+=`]*$",
        re.IGNORECASE,
    )

    def is_trivial_prompt(text: Optional[str]) -> bool:
        if not text:
            return True
        stripped = text.strip()
        if not stripped:
            return True
        if stripped.startswith("/"):
            return True
        return bool(TRIVIAL_PROMPT_RE.match(stripped))

    @dataclass(frozen=True)
    class RecallStatus:
        provider_label: str
        count: int
        glyph: str = "🧠"

    class MemoryProvider(ABC):
        """Minimal stand-in for the Hermes MemoryProvider ABC."""

        pre_compress_checkpoint_api_version = 1

        @property
        def name(self) -> str:
            raise NotImplementedError

        def is_available(self) -> bool:
            return True

        def unavailable_reason(self) -> str:
            return ""

        def initialize(self, session_id: str, **kwargs: Any) -> None:
            pass

        def system_prompt_block(self) -> str:
            return ""

        def prefetch(self, query: str, *, session_id: str = "") -> str:
            return ""

        def queue_prefetch(self, query: str, *, session_id: str = "") -> None:
            pass

        def recall_status(self) -> Optional[RecallStatus]:
            return None

        def sync_turn(
            self,
            user_content: str,
            assistant_content: str,
            *,
            session_id: str = "",
            messages: Optional[List[Dict[str, Any]]] = None,
        ) -> None:
            pass

        def get_tool_schemas(self) -> List[Dict[str, Any]]:
            return []

        def handle_tool_call(self, tool_name: str, args: Dict[str, Any], **kwargs: Any) -> str:
            raise NotImplementedError

        def get_config_schema(self) -> List[Dict[str, Any]]:
            return []

        def save_config(self, values: Dict[str, Any], hermes_home: str) -> None:
            pass

        def on_session_end(self, messages: List[Dict[str, Any]]) -> None:
            pass

        def on_pre_compress(self, messages: List[Dict[str, Any]]) -> str:
            return ""

        def on_session_switch(self, new_session_id: str, **kwargs: Any) -> None:
            pass

        def backup_paths(self) -> List[str]:
            return []

        def shutdown(self) -> None:
            pass

    agent_mod = types.ModuleType("agent")
    mp_mod = types.ModuleType("agent.memory_provider")
    mp_mod.MemoryProvider = MemoryProvider
    mp_mod.RecallStatus = RecallStatus
    mp_mod.is_trivial_prompt = is_trivial_prompt
    agent_mod.memory_provider = mp_mod
    sys.modules["agent"] = agent_mod
    sys.modules["agent.memory_provider"] = mp_mod


_install_fake_agent_module()
