"""Tests for ladym.providers.llm — LLMProvider ABC + FakeLLMProvider."""

from pydantic import BaseModel

from ladym.providers.llm import FakeLLMProvider, LLMProvider, Message


class Decision(BaseModel):
    action: str
    content: str | None = None


def test_fake_complete_returns_scripted_text():
    p = FakeLLMProvider(complete_fn=lambda msgs: "pong")
    assert p.complete([Message(role="user", content="ping")]) == "pong"


def test_fake_structured_returns_dict():
    p = FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"action": "ADD", "content": None}
    )
    out = p.complete_structured([Message(role="user", content="x")], Decision)
    assert out == {"action": "ADD", "content": None}


def test_fake_raises_when_no_script():
    import pytest

    p = FakeLLMProvider()
    with pytest.raises(NotImplementedError):
        p.complete([Message(role="user", content="x")])


def test_llmprovider_is_abstract():
    import pytest

    with pytest.raises(TypeError):
        LLMProvider()  # type: ignore[abstract]


def test_make_llm_provider_none_returns_none():
    from ladym.providers.llm import make_llm_provider
    assert make_llm_provider(provider="none", base_url="", model="",
                             api_key="", structured_method="function_calling") is None


def test_langchain_provider_built_when_importable():
    import pytest

    from ladym.adapter import LangChainLLMProvider
    from ladym.providers.llm import make_llm_provider
    pytest.importorskip("langchain_openai")
    p = make_llm_provider(provider="openai", base_url="https://example.test/v1",
                          model="m", api_key="k", structured_method="function_calling",
                          max_tokens=8, temperature=0.0, timeout_s=5)
    assert isinstance(p, LangChainLLMProvider)


def test_agent_registry_inherits_global_llm():
    from ladym.config import Config
    from ladym.providers.agents import AgentRegistry

    cfg = Config()
    cfg.llm_provider = "none"
    reg = AgentRegistry(cfg)
    assert reg.get("consolidate").provider == "none"
    cfg.agents_overrides = {"l5_mental_model": {"provider": "openai", "model": "m",
                                                "base_url": "u", "api_key_env": "K"}}
    assert reg.get("l5_mental_model").provider == "openai"
    assert reg.get("l5_mental_model").model == "m"


def test_agent_registry_timeout_s_inherits_llm_timeout_s():
    """``AgentConfig.timeout_s`` falls back to ``Config.llm_timeout_s`` (Task 2.1 flat field)."""
    from ladym.config import Config
    from ladym.providers.agents import AgentRegistry

    cfg = Config()
    cfg.llm_provider = "none"
    cfg.llm_timeout_s = 90.0
    reg = AgentRegistry(cfg)
    # Global llm_timeout_s flows through to every op's AgentConfig.timeout_s.
    assert reg.get("consolidate").timeout_s == 90.0


def test_agent_registry_timeout_s_per_op_override():
    """A per-op ``timeout_s`` override wins over the global ``llm_timeout_s``."""
    from ladym.config import Config
    from ladym.providers.agents import AgentRegistry

    cfg = Config()
    cfg.llm_provider = "none"
    cfg.llm_timeout_s = 90.0
    cfg.agents_overrides = {"consolidate": {"timeout_s": 5.0}}
    reg = AgentRegistry(cfg)
    assert reg.get("consolidate").timeout_s == 5.0


def test_make_agent_none_when_provider_none():
    from ladym.config import Config
    from ladym.providers.agents import make_agent

    cfg = Config()
    agent = make_agent(cfg, "consolidate")
    assert agent is None  # heuristic mode


def test_make_agent_passes_plaintext_api_key(monkeypatch):
    """A configured plaintext llm_api_key is threaded into make_llm_provider."""
    from ladym.config import Config
    from ladym.providers import agents

    captured: dict = {}

    def fake_make(**kw):
        captured.update(kw)
        return "STUB"

    monkeypatch.setattr(agents, "make_llm_provider", fake_make)
    cfg = Config()
    cfg.llm_provider = "openai"
    cfg.llm_api_key = "sk-plaintext"
    result = agents.make_agent(cfg, "consolidate")
    assert result == "STUB"
    assert captured["api_key"] == "sk-plaintext"
