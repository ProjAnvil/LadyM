"""ConfigError — actionable fail-fast for bad runtime config."""

from ladym.errors import ConfigError


def test_config_error_is_runtime_error():
    assert issubclass(ConfigError, RuntimeError)


def test_config_error_carries_message():
    err = ConfigError("provider openai missing key DEEPSEEK_API_KEY")
    assert "DEEPSEEK_API_KEY" in str(err)
