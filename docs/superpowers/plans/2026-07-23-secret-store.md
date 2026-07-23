# Secret Store + 缺 key 友好报错 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付一个跨平台加密 secret store（`~/.ladyM/`，AES-256-GCM），让 provider key 可交互登记、toml 零改动引用，并在缺 key 时 fail-fast 抛出带修复指引的 `ConfigError`（反 fallback）。

**Architecture:** 新增 `ladym/secrets.py`（SecretStore：master.key + secrets.enc）与 `ladym/errors.py`（ConfigError）；改 `providers/agents.py:make_agent` 与 `storage/embeddings.py:make_provider` 按三级（plaintext > secret store > env）取 key、缺失抛 ConfigError；CLI 加 `config` 命令组（set/set-master-key/reset-master-key/list/rm）+ 顶层友好错误（`--debug`）；MCP 工具兜 ConfigError；web editor 扩展 master key 与 kv 管理。

**Tech Stack:** Python ≥3.11、`cryptography`(AES-GCM)、`hashlib`(HKDF-SHA256，标准库)、typer、FastAPI(web)、pytest。

## Global Constraints

- **反 fallback**：provider 配了（≠"none"）却缺 key → 抛 `ConfigError` + 修复指引，**绝不静默退化**（见 spec §目标、记忆 `user-design-pref-no-fallback`）。
- **安全边界**：secret store 只防明文裸奔（cat/瞥见/误传明文），**不防 `~/.ladyM/` 整体泄露**（master key 与密文同目录）。此约束须写进 `secrets.py` 模块 docstring。
- master.key 存 **HKDF-SHA256 派生的 32 字节 AES key**，不存用户原始 KEY。
- toml **不改**：复用 `api_key_env`，查找链 `allow_plaintext 明文 > secret store mapping > env var`。
- 取 key 三级都为空才算"缺 key"。
- 文件权限：`~/.ladyM/` 目录 `0700`，`master.key`/`secrets.enc` `0600`。
- 写文件一律 `临时文件 + os.replace`（原子）。
- Python ≥3.11；ruff line-length 100；测试用 `typer.testing.CliRunner` + `monkeypatch.setenv("HOME", tmp)` 隔离（见 `tests/unit/test_cli.py:_isolate_config`）。
- 每个任务 TDD：先写失败测试 → 跑红 → 实现 → 跑绿 → commit。

---

## File Structure

| 文件 | 职责 | 动作 |
|------|------|------|
| `pyproject.toml` | 加 `cryptography` 核心依赖 | 改 |
| `src/ladym/errors.py` | `ConfigError` 异常 | 新建 |
| `src/ladym/secrets.py` | `SecretStore`：加解密/master key/原子 IO | 新建 |
| `src/ladym/providers/agents.py` | `make_agent` 三级取 key + 早 fail | 改 (84-104) |
| `src/ladym/storage/embeddings.py` | `OpenAIEmbedding` 收 api_key；`make_provider` openai 分支三级取 key + 早 fail | 改 (110-135, 203-207) |
| `src/ladym/cli.py` | `config` 子组(set/set-master-key/reset-master-key/list/rm + web)、顶层错误处理、`--debug`、entry 改 `main` | 改 (16-38, 342-378) |
| `src/ladym/mcp/server.py` | 工具兜 `ConfigError` → 结构化错误 | 改 |
| `src/ladym/web/app.py` | secret 管理 API + 模板 | 改 |
| `tests/unit/test_secrets.py` | SecretStore 测试 | 新建 |
| `tests/unit/test_errors.py` | ConfigError 测试 | 新建 |
| `tests/unit/test_cli.py` | config 命令组 + 友好错误测试 | 改 |
| `tests/unit/test_mcp_server.py` | MCP 缺 key 友好错误测试 | 改 |

---

### Task 1: 加 cryptography 核心依赖

**Files:**
- Modify: `pyproject.toml` (dependencies 数组, 21-30 行)

**Interfaces:** 无（环境准备）

- [ ] **Step 1: 加依赖**

编辑 `pyproject.toml`，在 `dependencies` 数组加一行（保持字母序不强制，放末尾即可）：

```toml
dependencies = [
    "pydantic>=2.6",
    "typer>=0.12",
    "rich>=13.7",
    "sqlite-vec>=0.1.6",
    "tree-sitter>=0.23",
    "tree-sitter-language-pack>=0.1",
    "numpy>=1.26",
    "sqlmodel>=0.0.16",
    "cryptography>=42.0",
]
```

- [ ] **Step 2: 安装并验证**

Run: `cd /Users/yuhaochen/Documents/codebase/projanvil/ladyM && uv sync --extra dev --extra web --extra llm --extra mcp --extra openai`
Expected: 成功，`cryptography` 出现在 lock。

Run: `.venv/bin/python -c "from cryptography.hazmat.primitives.ciphers.aead import AESGCM; print('ok')"`
Expected: `ok`

- [ ] **Step 3: Commit**

```bash
git add pyproject.toml uv.lock
git commit -m "feat(secret-store): add cryptography core dependency"
```

---

### Task 2: ConfigError 异常

**Files:**
- Create: `src/ladym/errors.py`
- Test: `tests/unit/test_errors.py`

**Interfaces:**
- Produces: `class ConfigError(RuntimeError)` — 消息须可读、自包含修复指引。

- [ ] **Step 1: 写失败测试**

创建 `tests/unit/test_errors.py`：

```python
"""ConfigError — actionable fail-fast for bad runtime config."""

from ladym.errors import ConfigError


def test_config_error_is_runtime_error():
    assert issubclass(ConfigError, RuntimeError)


def test_config_error_carries_message():
    err = ConfigError("provider openai missing key DEEPSEEK_API_KEY")
    assert "DEEPSEEK_API_KEY" in str(err)
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_errors.py -q`
Expected: FAIL `ModuleNotFoundError: ladym.errors`

- [ ] **Step 3: 实现**

创建 `src/ladym/errors.py`：

```python
"""Centralized ladym exceptions."""


class ConfigError(RuntimeError):
    """Raised when runtime configuration makes an operation impossible.

    Typical cause: an LLM/embedding provider is configured (``provider !=
    "none"``) but its API key is missing. The message MUST be actionable and
    one-line — CLI/MCP surface it verbatim instead of dumping a traceback.

    This is **fail-fast, NOT a fallback**: we refuse to silently degrade to
    heuristic/offline mode when the user explicitly asked for a provider.
    See ``user-design-pref-no-fallback``.
    """
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_errors.py -q`
Expected: 2 passed

- [ ] **Step 5: Commit**

```bash
git add src/ladym/errors.py tests/unit/test_errors.py
git commit -m "feat(secret-store): add ConfigError exception"
```

---

### Task 3: SecretStore（AES-256-GCM）

**Files:**
- Create: `src/ladym/secrets.py`
- Test: `tests/unit/test_secrets.py`

**Interfaces:**
- Consumes: `ladym.errors.ConfigError`
- Produces:
  - `SecretStore(dir: Path = ~/.ladyM)` — `.has_master_key()`, `.require_master_key()`, `.get(name) -> str|None`, `.set(name, value)`, `.list_names() -> list[str]`, `.remove(name) -> bool`, `.set_master_key(key: str|None) -> bytes`, `.reset_master_key(new_key: str|None) -> None`
  - `get_store() -> SecretStore`（模块级，解析 `Path.home()`）

- [ ] **Step 1: 写失败测试**

创建 `tests/unit/test_secrets.py`：

```python
"""SecretStore — AES-256-GCM over ~/.ladyM."""

import os
import stat

import pytest

from ladym.errors import ConfigError
from ladym.secrets import SecretStore


@pytest.fixture
def store(tmp_path):
    return SecretStore(dir=tmp_path)


def test_no_master_key_means_empty(store):
    assert not store.has_master_key()
    with pytest.raises(ConfigError, match="set-master-key"):
        store.require_master_key()
    with pytest.raises(ConfigError):
        store.set("K", "v")  # cannot set without master key


def test_set_master_key_random_then_roundtrip(store):
    aes = store.set_master_key(None)  # random
    assert len(aes) == 32
    store.set("DEEPSEEK_API_KEY", "sk-abc")
    assert store.get("DEEPSEEK_API_KEY") == "sk-abc"
    assert store.list_names() == ["DEEPSEEK_API_KEY"]


def test_set_master_key_user_string_derived(store):
    store.set_master_key("my-passphrase")
    store.set("K", "secret")
    assert store.get("K") == "secret"
    # user string is NOT stored verbatim
    assert b"my-passphrase" not in (store._master).read_bytes()


def test_master_key_file_permissions(store):
    store.set_master_key(None)
    mode = stat.S_IMODE(os.stat(store._master).st_mode)
    assert mode == 0o600
    dmode = stat.S_IMODE(os.stat(store._dir).st_mode)
    assert dmode == 0o700


def test_overwrite_and_remove(store):
    store.set_master_key(None)
    store.set("K", "v1")
    store.set("K", "v2")  # overwrite
    assert store.get("K") == "v2"
    assert store.remove("K") is True
    assert store.get("K") is None
    assert store.remove("K") is False


def test_set_master_key_refuses_when_secrets_exist(store):
    store.set_master_key(None)
    store.set("K", "v")
    with pytest.raises(ConfigError, match="reset-master-key"):
        store.set_master_key("new")


def test_reset_master_key_reencrypts(store):
    store.set_master_key("old")
    store.set("A", "1")
    store.set("B", "2")
    store.reset_master_key("new")
    assert store.get("A") == "1"
    assert store.get("B") == "2"
    assert store.list_names() == ["A", "B"]


def test_reset_master_key_requires_existing(store):
    with pytest.raises(ConfigError, match="set-master-key"):
        store.reset_master_key("new")


def test_ciphertext_is_not_plaintext(store):
    store.set_master_key(None)
    store.set("K", "super-secret-value")
    raw = store._secrets.read_text()
    assert "super-secret-value" not in raw
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_secrets.py -q`
Expected: FAIL `ModuleNotFoundError: ladym.secrets`

- [ ] **Step 3: 实现**

创建 `src/ladym/secrets.py`：

```python
"""Encrypted secret store — AES-256-GCM over ``~/.ladyM``.

Security boundary (READ BEFORE TRUSTING THIS):
    This store prevents **plaintext-at-rest**: ``cat``-ting ``secrets.enc``,
    shoulder-surfing, or accidentally pasting the file into chat/logs/commits
    will not reveal key values. It does **NOT** protect against full
    ``~/.ladyM/`` exfiltration — the master key and the ciphertext live in the
    same directory, so anyone who can read the directory can decrypt. This is
    the explicit trade-off for cross-platform, non-interactive operation
    (MCP servers and background workers must be able to decrypt without a
    passphrase prompt). For stronger isolation, keep ``~/.ladyM`` on encrypted
    storage and rely on OS account/file-permission boundaries.
"""

from __future__ import annotations

import base64
import hashlib
import os
import secrets as _py_secrets
from pathlib import Path

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from .errors import ConfigError

LADYM_DIR = Path.home() / ".ladyM"
_NONCE_LEN = 12  # AES-GCM standard nonce length


def _derive_aes_key(user_key: str) -> bytes:
    """HKDF-SHA256 a user-supplied string into a 32-byte AES key."""
    return hashlib.hkdf_sha256(
        ikm=user_key.encode(),
        length=32,
        salt=b"ladym-master-key-salt",
        info=b"ladym-master-key",
    )


class SecretStore:
    def __init__(self, dir: Path = LADYM_DIR):
        self._dir = Path(dir)
        self._master = self._dir / "master.key"
        self._secrets = self._dir / "secrets.enc"
        self._cache: dict[str, str] = {}

    # ----- master key -----
    def has_master_key(self) -> bool:
        return self._master.exists()

    def require_master_key(self) -> None:
        if not self.has_master_key():
            raise ConfigError(
                "no master key set — run `ladym config set-master-key` first."
            )

    def _read_aes_key(self) -> bytes:
        self.require_master_key()
        return base64.b64decode(self._master.read_bytes())

    def set_master_key(self, key: str | None) -> bytes:
        if self._read_all():
            raise ConfigError(
                "secrets.enc already has entries — setting a fresh master key "
                "would make them unrecoverable. Use "
                "`ladym config reset-master-key` to re-encrypt under a new key."
            )
        aes_key = _derive_aes_key(key) if key is not None else _py_secrets.token_bytes(32)
        self._write_master(aes_key)
        return aes_key

    def reset_master_key(self, new_key: str | None) -> None:
        old_aes = self._read_aes_key()  # also require_master_key
        old_kv = self._read_all()
        # decrypt all under old key
        plain: dict[str, str] = {}
        for name, enc in old_kv.items():
            nonce, ct = self._split(enc)
            plain[name] = AESGCM(old_aes).decrypt(nonce, ct, None).decode()
        # re-encrypt under new key
        new_aes = _derive_aes_key(new_key) if new_key is not None else _py_secrets.token_bytes(32)
        new_kv: dict[str, str] = {}
        for name, value in plain.items():
            new_kv[name] = self._encrypt_value(new_aes, value)
        self._write_master(new_aes)
        self._write_all(new_kv)
        self._cache = {}

    # ----- kv -----
    def get(self, name: str) -> str | None:
        if name in self._cache:
            return self._cache[name]
        enc = self._read_all().get(name)
        if enc is None:
            return None
        nonce, ct = self._split(enc)
        value = AESGCM(self._read_aes_key()).decrypt(nonce, ct, None).decode()
        self._cache[name] = value
        return value

    def set(self, name: str, value: str) -> None:
        self.require_master_key()
        kv = self._read_all()
        kv[name] = self._encrypt_value(self._read_aes_key(), value)
        self._write_all(kv)
        self._cache[name] = value

    def list_names(self) -> list[str]:
        return sorted(self._read_all().keys())

    def remove(self, name: str) -> bool:
        kv = self._read_all()
        if name not in kv:
            return False
        del kv[name]
        self._write_all(kv)
        self._cache.pop(name, None)
        return True

    # ----- crypto helpers -----
    @staticmethod
    def _encrypt_value(aes_key: bytes, value: str) -> str:
        nonce = os.urandom(_NONCE_LEN)
        ct = AESGCM(aes_key).encrypt(nonce, value.encode(), None)
        return base64.b64encode(nonce + ct).decode()

    @staticmethod
    def _split(enc: str) -> tuple[bytes, bytes]:
        raw = base64.b64decode(enc)
        return raw[:_NONCE_LEN], raw[_NONCE_LEN:]

    # ----- low-level IO (atomic + permissions) -----
    def _read_all(self) -> dict[str, str]:
        if not self._secrets.exists():
            return {}
        kv: dict[str, str] = {}
        for line in self._secrets.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            name, _, enc = line.partition("=")
            kv[name.strip()] = enc.strip()
        return kv

    def _write_all(self, kv: dict[str, str]) -> None:
        self._ensure_dir()
        text = "".join(f"{k} = {v}\n" for k, v in sorted(kv.items()))
        self._atomic_write(self._secrets, text.encode(), 0o600)

    def _write_master(self, aes_key: bytes) -> None:
        self._ensure_dir()
        self._atomic_write(self._master, base64.b64encode(aes_key), 0o600)

    def _ensure_dir(self) -> None:
        self._dir.mkdir(parents=True, exist_ok=True)
        try:
            os.chmod(self._dir, 0o700)
        except OSError:
            pass  # non-POSIX FS best-effort

    @staticmethod
    def _atomic_write(path: Path, data: bytes, mode: int) -> None:
        tmp = path.with_suffix(path.suffix + ".tmp")
        tmp.write_bytes(data)
        try:
            os.chmod(tmp, mode)
        except OSError:
            pass
        os.replace(tmp, path)


def get_store() -> SecretStore:
    """Module-level accessor (resolves ``Path.home()`` at call time)."""
    return SecretStore()
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_secrets.py -q`
Expected: 9 passed

- [ ] **Step 5: Commit**

```bash
git add src/ladym/secrets.py tests/unit/test_secrets.py
git commit -m "feat(secret-store): AES-256-GCM SecretStore with master key"
```

---

### Task 4: make_agent 三级取 key + 早 fail

**Files:**
- Modify: `src/ladym/providers/agents.py` (84-104, `make_agent`)
- Test: `tests/unit/test_llm_providers.py`（追加）或新建 `tests/unit/test_make_agent.py`

**Interfaces:**
- Consumes: `ladym.errors.ConfigError`、`ladym.secrets.get_store`
- Produces: `make_agent(cfg, op)` 在 `provider != "none"` 且三级 key 全空时抛 `ConfigError`

- [ ] **Step 1: 写失败测试**

新建 `tests/unit/test_make_agent.py`：

```python
"""make_agent — three-tier key resolution + fail-fast ConfigError."""

import pytest

from ladym.config import Config
from ladym.errors import ConfigError
from ladym.providers import agents


def _cfg(provider="openai", api_key_env="DEEPSEEK_API_KEY", **kw):
    c = Config()
    c.llm_provider = provider
    c.llm_api_key_env = api_key_env
    c.llm_base_url = kw.get("base_url", "")
    c.llm_model = kw.get("model", "x")
    return c


def test_none_provider_returns_none(monkeypatch):
    monkeypatch.setattr(agents, "get_store", lambda: _FakeStore({}))
    assert agents.make_agent(_cfg(provider="none"), "consolidate") is None


def test_env_var_used(monkeypatch):
    monkeypatch.setattr(agents, "get_store", lambda: _FakeStore({}))
    monkeypatch.setenv("DEEPSEEK_API_KEY", "sk-env")
    # openai provider needs the openai extra; only assert no ConfigError raised
    # by checking the key-resolution path up to make_llm_provider via a non-openai
    # provider is hard — so we assert the missing-key branch instead (below).
    # Here just ensure env path does not raise ConfigError by using plaintext.
    c = _cfg()
    c.llm_api_key = "sk-plain"  # allow_plaintext wins
    try:
        agents.make_agent(c, "consolidate")
    except Exception as e:
        assert not isinstance(e, ConfigError)


def test_missing_key_raises_config_error(monkeypatch):
    monkeypatch.setattr(agents, "get_store", lambda: _FakeStore({}))
    monkeypatch.delenv("DEEPSEEK_API_KEY", raising=False)
    with pytest.raises(ConfigError) as ei:
        agents.make_agent(_cfg(), "consolidate")
    assert "DEEPSEEK_API_KEY" in str(ei.value)
    assert "set-master-key" in str(ei.value)


def test_store_overrides_env(monkeypatch):
    # if key is in store, it is resolved (no ConfigError); env presence irrelevant
    monkeypatch.setattr(agents, "get_store", lambda: _FakeStore({"DEEPSEEK_API_KEY": "sk-store"}))
    monkeypatch.delenv("DEEPSEEK_API_KEY", raising=False)
    c = _cfg()
    try:
        agents.make_agent(c, "consolidate")
    except Exception as e:
        assert not isinstance(e, ConfigError)


class _FakeStore:
    def __init__(self, mapping):
        self._m = mapping
    def get(self, name):
        return self._m.get(name)
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_make_agent.py -q`
Expected: FAIL（当前 make_agent 不抛 ConfigError；`get_store` 未在 agents 模块引用）

- [ ] **Step 3: 实现**

编辑 `src/ladym/providers/agents.py`：顶部 import 区加 `from ..errors import ConfigError`，import `os` 已有。在 `make_agent` 上方加 helper，并替换 `make_agent` 取 key 段：

```python
from ..errors import ConfigError
from ..secrets import get_store


def _missing_key_msg(provider: str, env_name: str) -> str:
    return (
        f'LLM provider "{provider}" needs an API key but "{env_name}" is neither '
        f"registered in the secret store nor set as an environment variable. "
        f"Run `ladym config set-master-key` then `ladym config set {env_name} "
        f"<value>`, or set llm.provider=\"none\" in ladym.toml for offline mode."
    )


def _resolve_api_key(plaintext: str, env_name: str, store) -> str:
    """Three-tier: allow_plaintext > secret store mapping > env var."""
    if plaintext:
        return plaintext
    if env_name:
        v = store.get(env_name)
        if v:
            return v
        return os.environ.get(env_name, "")
    return ""
```

替换 `make_agent` 主体（91-104）为：

```python
def make_agent(cfg: Config, op: str) -> LLMProvider | None:
    """Build (or skip) the LLM provider bound to one operation.

    Returns ``None`` for heuristic mode (``provider == "none"``). For any other
    provider, the API key is resolved three-tier (allow_plaintext > secret store
    > env var); if all three are empty, raises :class:`ConfigError` — fail-fast,
    NOT a silent fallback to offline.
    """
    ac = AgentRegistry(cfg).get(op)
    if ac.provider == "none":
        return None
    api_key = _resolve_api_key(ac.api_key, ac.api_key_env, get_store())
    if not api_key:
        raise ConfigError(_missing_key_msg(ac.provider, ac.api_key_env))
    return make_llm_provider(
        provider=ac.provider,
        base_url=ac.base_url,
        model=ac.model,
        api_key=api_key,
        structured_method=ac.structured_method,
        max_tokens=ac.max_tokens,
        temperature=ac.temperature,
        timeout_s=ac.timeout_s,
    )
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_make_agent.py -q`
Expected: 4 passed

回归：Run: `.venv/bin/python -m pytest tests/unit/test_attention_gate.py tests/unit/test_llm_providers.py -q`
Expected: 仍通过（provider=none 不受影响）

- [ ] **Step 5: Commit**

```bash
git add src/ladym/providers/agents.py tests/unit/test_make_agent.py
git commit -m "feat(secret-store): make_agent three-tier key resolution + fail-fast"
```

---

### Task 5: CLI 顶层错误处理 + --debug

**Files:**
- Modify: `src/ladym/cli.py` (callback 30-38、底部 377-378)、`pyproject.toml` (entry 62)

**Interfaces:**
- Produces: `ladym.cli.main()`（包 `app()`，兜 ConfigError/Exception → 一行消息 + exit 1；`--debug` 透传 traceback）；`[project.scripts] ladym = "ladym.cli:main"`

- [ ] **Step 1: 写失败测试**

追加到 `tests/unit/test_cli.py`（文件顶部已 `from typer.testing import CliRunner` + `runner = CliRunner()`，沿用 `_isolate_config`）：

```python
def test_config_error_surfaces_friendly_not_traceback(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)
    db = str(tmp_path / "x.ladym.db")
    # provider=openai but no key anywhere → make_agent raises ConfigError on remember
    monkeypatch.setenv("LADYM_LLM_PROVIDER", "openai")
    monkeypatch.setenv("LADYM_LLM_API_KEY_ENV", "NO_SUCH_KEY")
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)
    from ladym import cli as cli_mod
    # point secret store at an empty tmp HOME so no mapping
    r = runner.invoke(app, ["remember", "x", "--db", db])
    assert r.exit_code == 1
    assert "NO_SUCH_KEY" in r.output
    assert "set-master-key" in r.output
    assert "Traceback (most recent call last)" not in r.output


def test_debug_shows_traceback(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)
    db = str(tmp_path / "x.ladym.db")
    monkeypatch.setenv("LADYM_LLM_PROVIDER", "openai")
    monkeypatch.setenv("LADYM_LLM_API_KEY_ENV", "NO_SUCH_KEY")
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)
    r = runner.invoke(app, ["--debug", "remember", "x", "--db", db])
    assert r.exit_code == 1
    assert "Traceback" in r.output
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_cli.py -k "config_error or debug_shows" -q`
Expected: FAIL（当前 remember 崩 OpenAIError traceback，exit≠1 友好消息）

- [ ] **Step 3: 实现**

编辑 `src/ladym/cli.py`：

(a) 顶部加 `from .errors import ConfigError`（与现有 `from .config import Config` 等同区）。

(b) 改 `@app.callback()`（30-38）加 `debug`：

```python
_debug: bool = False


@app.callback()
def _main(
    config: str | None = typer.Option(  # noqa: B008 - typer idiom
        None, "--config", help="Path to a ladym.toml to load on top of defaults/env."
    ),
    debug: bool = typer.Option(  # noqa: B008 - typer idiom
        False, "--debug", help="Show full Python tracebacks on error."
    ),
) -> None:
    """LadyM — global options live here (parsed BEFORE the subcommand)."""
    global _config_path, _debug
    _config_path = config
    _debug = debug
```

(c) 替换文件底部 `if __name__ == "__main__": app()`（377-378）为：

```python
def main() -> None:
    """Entry point: run the Typer app, converting ConfigError (and other
    provider errors, when not --debug) into a one-line message + exit 1."""
    try:
        app()
    except ConfigError as e:
        if _debug:
            raise
        console.print(f"[red]ladym:[/red] {e}")
        raise typer.Exit(1) from None
    except Exception as e:  # noqa: BLE001 — top-level friendly handler
        if _debug:
            raise
        console.print(f"[red]ladym:[/red] {type(e).__name__}: {e}")
        raise typer.Exit(1) from None


if __name__ == "__main__":  # pragma: no cover
    main()
```

(d) 改 `pyproject.toml`：

```toml
[project.scripts]
ladym = "ladym.cli:main"
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_cli.py -q`
Expected: 全部通过（含新 2 条 + 旧的不受影响）

手动验证：`.venv/bin/ladym --help` 显示 `--debug`。

- [ ] **Step 5: Commit**

```bash
git add src/ladym/cli.py pyproject.toml tests/unit/test_cli.py
git commit -m "feat(secret-store): CLI friendly errors + --debug, entry→main"
```

---

### Task 6: `config` 命令组（set/set-master-key/reset-master-key/list/rm）

**Files:**
- Modify: `src/ladym/cli.py`（把现有 `config` @app.command() 342-374 改造成 Typer 子组 + 新子命令）
- Test: `tests/unit/test_cli.py`（追加）

**Interfaces:**
- Consumes: `ladym.secrets.SecretStore`/`get_store`
- Produces: `ladym config set KEY VALUE` / `config set-master-key [KEY]` / `config reset-master-key [KEY]` / `config list` / `config rm KEY`；`ladym config`（无子命令）仍启动 web editor。

- [ ] **Step 1: 写失败测试**

追加到 `tests/unit/test_cli.py`：

```python
def test_config_set_requires_master_key(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)  # HOME → tmp_path → empty ~/.ladyM
    r = runner.invoke(app, ["config", "set", "DEEPSEEK_API_KEY", "sk-x"])
    assert r.exit_code == 1
    assert "set-master-key" in r.output


def test_config_set_and_list_roundtrip(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)
    assert runner.invoke(app, ["config", "set-master-key", "pass"]).exit_code == 0
    assert runner.invoke(app, ["config", "set", "K1", "v1"]).exit_code == 0
    r = runner.invoke(app, ["config", "list"])
    assert "K1" in r.output
    assert "v1" not in r.output  # value not printed


def test_config_reset_master_key_reencrypts(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)
    runner.invoke(app, ["config", "set-master-key", "old"])
    runner.invoke(app, ["config", "set", "K", "secret"])
    assert runner.invoke(app, ["config", "reset-master-key", "new"]).exit_code == 0
    # value still resolvable after re-encryption (verified via get in a py call below)
    from ladym.secrets import SecretStore
    s = SecretStore(dir=tmp_path / ".ladyM")
    assert s.get("K") == "secret"


def test_config_rm(tmp_path, monkeypatch):
    _isolate_config(monkeypatch, tmp_path)
    runner.invoke(app, ["config", "set-master-key", "p"])
    runner.invoke(app, ["config", "set", "K", "v"])
    assert runner.invoke(app, ["config", "rm", "K"]).exit_code == 0
    r = runner.invoke(app, ["config", "list"])
    assert "K" not in r.output
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_cli.py -k "config_set or config_reset or config_rm" -q`
Expected: FAIL（`config set` 等子命令不存在）

- [ ] **Step 3: 实现**

编辑 `src/ladym/cli.py`：

(a) 顶部加 `import getpass` 与 `from .secrets import SecretStore, get_store`。

(b) 删除现有 `@app.command() def config(...)`（342-374），替换为子组 + 原 web 逻辑搬进 callback：

```python
config_app = typer.Typer(
    name="config",
    help="Manage ladym.toml (web editor) and the encrypted secret store.",
    no_args_is_help=False,
)


@config_app.callback(invoke_without_command=True)
def config_main(
    ctx: typer.Context,
    port: int = typer.Option(8765, "--port"),
    no_browser: bool = typer.Option(False, "--no-browser"),
):
    """With no subcommand: launch the local web config editor (needs web extra)."""
    if ctx.invoked_subcommand is not None:
        return
    try:
        import fastapi  # noqa: F401
        import uvicorn
        from .web.app import build_app
    except ImportError:
        console.print(
            "[red]web extra not installed[/red] — install with: pip install 'ladym\\[web]'"
        )
        raise typer.Exit(1) from None
    cfg_path = Path(_config_path) if _config_path else None
    app_obj = build_app(config_path=cfg_path)
    if not no_browser:
        import threading
        import webbrowser
        threading.Timer(1.0, lambda: webbrowser.open(f"http://127.0.0.1:{port}/")).start()
    console.print(f"[bold]LadyM config[/bold] on http://127.0.0.1:{port}/")
    uvicorn.run(app_obj, host="127.0.0.1", port=port, log_level="warning")


def _store() -> SecretStore:
    return get_store()


@config_app.command("set")
def config_set(
    key: str = typer.Argument(..., help="KEY_NAME (same value as api_key_env in ladym.toml)."),
    value: str = typer.Argument(..., help="Secret value (plaintext; encrypted at rest)."),
):
    """Store KEY=VALUE in the encrypted secret store."""
    _store().set(key, value)
    console.print(f"[green]stored[/green] {key}")


@config_app.command("set-master-key")
def config_set_master_key(
    key: str | None = typer.Argument(
        None, help="Master key string; omit to generate a strong random key."
    ),
):
    """Initialize the master key (required before storing any secret)."""
    store = _store()
    store.set_master_key(key)
    if key is None:
        console.print(
            "[green]generated[/green] a random master key at "
            f"{store._master} — back it up; losing it makes secrets unrecoverable."
        )
    else:
        console.print("[green]master key set[/green]")


@config_app.command("reset-master-key")
def config_reset_master_key(
    key: str | None = typer.Argument(None, help="New master key; omit for random."),
):
    """Re-encrypt every secret under a new master key."""
    _store().reset_master_key(key)
    console.print("[green]master key reset[/green]; all secrets re-encrypted")


@config_app.command("list")
def config_list():
    """List stored KEY_NAMEs (values are never printed)."""
    names = _store().list_names()
    if not names:
        console.print("[yellow]no secrets stored[/yellow]")
        return
    for n in names:
        console.print(n)


@config_app.command("rm")
def config_rm(
    key: str = typer.Argument(...),
):
    """Remove a stored secret."""
    if _store().remove(key):
        console.print(f"[green]removed[/green] {key}")
    else:
        console.print(f"[yellow]no such key[/yellow] {key}")
        raise typer.Exit(1)


app.add_typer(config_app)
```

注意：`config_set_master_key`/`config_reset_master_key`/`config_set` 在缺 master key 时由 `SecretStore` 抛 `ConfigError`，会被 Task 5 的 `main()` 转成友好消息。

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_cli.py -q`
Expected: 全通过

手动验证：
- `.venv/bin/ladym config --help` 显示 set/set-master-key/reset-master-key/list/rm 子命令。
- `.venv/bin/ladym config`（无子命令）仍启动 web editor（需 web extra）。

- [ ] **Step 5: Commit**

```bash
git add src/ladym/cli.py tests/unit/test_cli.py
git commit -m "feat(secret-store): config command group (set/set-master-key/reset/list/rm)"
```

---

### Task 7: MCP 工具兜 ConfigError

**Files:**
- Modify: `src/ladym/mcp/server.py`（每个会触发 make_agent 的工具：remember/consolidate，以及 worker 相关——但 MCP 无 worker；核心是 remember）
- Test: `tests/unit/test_mcp_server.py`

**Interfaces:**
- Consumes: `ladym.errors.ConfigError`
- Produces: 工具在 ConfigError 时返回 `{"error": "<friendly msg>"}` JSON，不冒 traceback。

- [ ] **Step 1: 写失败测试**

追加到 `tests/unit/test_mcp_server.py`（沿用其现有 build_server 调用模式；先读该文件确认 client 调用方式）：

```python
def test_remember_returns_friendly_error_when_key_missing(tmp_path, monkeypatch):
    # provider=openai, no key anywhere → ConfigError inside eng.remember → tool
    # must return a structured error, not raise.
    monkeypatch.setenv("LADYM_DB", str(tmp_path / "m.ladym.db"))
    monkeypatch.setenv("LADYM_LLM_PROVIDER", "openai")
    monkeypatch.setenv("LADYM_LLM_API_KEY_ENV", "NO_SUCH_KEY")
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)
    monkeypatch.setenv("HOME", str(tmp_path))  # empty secret store
    server = build_server()
    result = server.call_tool("remember", {"content": "x"})  # 见文件现有调用方式
    text = result[0].text if isinstance(result, list) else str(result)
    assert "NO_SUCH_KEY" in text
    assert "set-master-key" in text
```

（先读 `tests/unit/test_mcp_server.py` 确认其调用 server 工具的实际 API——可能是 `server.call_tool(name, args)` 返回 list 或需经 client——再对齐测试代码。）

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_mcp_server.py -k "friendly_error" -q`
Expected: FAIL（当前 ConfigError 冒泡）

- [ ] **Step 3: 实现**

编辑 `src/ladym/mcp/server.py`：顶部 `from ..errors import ConfigError`。在会触发 make_agent 的工具（`remember`、`consolidate`）函数体包一层：

```python
from ..errors import ConfigError


def _guard(fn):
    """Wrap a tool so ConfigError becomes a structured JSON error string."""
    from functools import wraps

    @wraps(fn)
    def wrapper(*args, **kwargs):
        try:
            return fn(*args, **kwargs)
        except ConfigError as e:
            return json.dumps({"error": str(e)})
    return wrapper
```

把 `@server.tool()` 装饰的 `remember` 改为 `@server.tool() @ _guard`（或在其函数体首行 `try:`）。对 `remember` 与 `consolidate` 各加（这两个经 make_agent）。示例（remember）：

```python
@server.tool()
def remember(content, tags=None, ...):
    try:
        ...  # 现有函数体
    except ConfigError as e:
        return json.dumps({"error": str(e)})
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_mcp_server.py -q`
Expected: 全通过

- [ ] **Step 5: Commit**

```bash
git add src/ladym/mcp/server.py tests/unit/test_mcp_server.py
git commit -m "feat(secret-store): MCP tools return structured error on ConfigError"
```

---

### Task 8（可选，embedding 当前 ollama 不触发）: OpenAI embedding secret store 支持

**Files:**
- Modify: `src/ladym/storage/embeddings.py` (`OpenAIEmbedding` 110-135、`make_provider` openai 分支 203-207)
- Test: `tests/unit/test_embeddings.py` 或 `test_embedding_external.py`

**Interfaces:**
- Produces: `OpenAIEmbedding(model, dim, base_url, api_key)`；`make_provider` openai 分支三级取 `embedding_api_key_env` + 缺失抛 ConfigError。

> 说明：用户当前 embedding=ollama（无需 key），此任务为满足 spec §2 的对称性、面向将来配 OpenAI embedding。可独立于 Task 1-7 之后任何时间实施。

- [ ] **Step 1: 写失败测试**

追加：

```python
def test_openai_embedding_accepts_api_key():
    from ladym.storage.embeddings import OpenAIEmbedding
    emb = OpenAIEmbedding(model="text-embedding-3-small", base_url=None, api_key="sk-x")
    assert emb._model == "text-embedding-3-small"


def test_make_provider_openai_missing_key_raises(tmp_path, monkeypatch):
    from ladym.config import Config
    from ladym.storage.embeddings import make_provider
    from ladym.errors import ConfigError
    c = Config()
    c.embedding_provider = "openai"
    c.embedding_api_key_env = "NO_SUCH_KEY"
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)
    monkeypatch.setattr("ladym.storage.embeddings.get_store", lambda: _Empty())
    with pytest.raises(ConfigError, match="NO_SUCH_KEY"):
        make_provider(c)


class _Empty:
    def get(self, name): return None
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_embeddings.py -k "openai_embedding or openai_missing" -q`
Expected: FAIL

- [ ] **Step 3: 实现**

编辑 `src/ladym/storage/embeddings.py`：

(a) `OpenAIEmbedding.__init__` 加 `api_key`（110-127）：

```python
def __init__(self, model="text-embedding-3-small", dim=1536,
             base_url=None, api_key=None):
    try:
        from openai import OpenAI  # type: ignore
    except ImportError as e:  # pragma: no cover - optional dep
        raise ImportError(
            "openai is not installed. Install with: pip install 'ladym[openai]'"
        ) from e
    kw = {"base_url": base_url} if base_url else {}
    if api_key:
        kw["api_key"] = api_key
    self._client = OpenAI(**kw)
    self._model = model
    self.dim = dim
```

(b) 顶部加 `from ..errors import ConfigError`、`from ..secrets import get_store`、`import os`。加 helper：

```python
def _resolve_embedding_key(cfg) -> str | None:
    env_name = cfg.embedding_api_key_env
    if not env_name:
        return None
    v = get_store().get(env_name)
    if v:
        return v
    return os.environ.get(env_name, "") or None
```

(c) `make_provider` 的 `elif name == "openai":` 分支（203-207）改为：

```python
elif name == "openai":
    api_key = _resolve_embedding_key(config)
    if not api_key and not os.environ.get(config.embedding_api_key_env, ""):
        # neither store nor env (nor plaintext) — fail fast
        raise ConfigError(
            f'embedding provider "openai" needs an API key but '
            f'"{config.embedding_api_key_env}" is neither in the secret store '
            f'nor the environment. Run `ladym config set-master-key` then '
            f'`ladym config set {config.embedding_api_key_env} <value>`.'
        )
    provider = OpenAIEmbedding(
        config.embedding_model or "text-embedding-3-small",
        base_url=config.embedding_base_url or None,
        api_key=api_key,
    )
```

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_embeddings.py tests/unit/test_embedding_external.py -q`
Expected: 通过

回归：`uv run pytest tests/ -q`（确保 hashing/ollama embedding 不受影响）。

- [ ] **Step 5: Commit**

```bash
git add src/ladym/storage/embeddings.py tests/unit/test_embeddings.py
git commit -m "feat(secret-store): OpenAI embedding three-tier key + fail-fast"
```

---

### Task 9: web editor 扩展（master key + kv 管理）

**Files:**
- Modify: `src/ladym/web/app.py`（加 secret API 路由）、`src/ladym/web/templates/index.html`（加 master key + kv 面板）
- Test: `tests/unit/test_web_app.py`（新建，需 web extra）

**Interfaces:**
- Produces: `GET /api/secrets`（list names）、`POST /api/secrets`（set KEY/VALUE）、`DELETE /api/secrets/{name}`、`POST /api/master-key`（set/reset）。UI 仅在 master key 已设时启用 kv 区。

> 说明：依赖 web extra（fastapi/httpx 测试）。先读 `src/ladym/web/app.py` 与 `templates/index.html` 全文，对齐其 HTMX 风格再写。

- [ ] **Step 1: 写失败测试**

新建 `tests/unit/test_web_app.py`：

```python
"""Web editor secret management endpoints."""

import pytest

fastapi = pytest.importorskip("fastapi")
from fastapi.testclient import TestClient  # noqa: E402

from ladym.web.app import build_app  # noqa: E402


@pytest.fixture
def client(tmp_path, monkeypatch):
    monkeypatch.setenv("HOME", str(tmp_path))
    app = build_app(config_path=None)
    return TestClient(app)


def test_secrets_empty_without_master_key(client):
    r = client.get("/api/secrets")
    assert r.status_code == 200
    assert r.json() == {"master_key_set": False, "names": []}


def test_set_master_key_then_set_kv(client, tmp_path):
    assert client.post("/api/master-key", json={"key": "p"}).status_code == 200
    assert client.post("/api/secrets", json={"name": "K", "value": "v"}).status_code == 200
    r = client.get("/api/secrets")
    assert r.json()["master_key_set"] is True
    assert r.json()["names"] == ["K"]


def test_reset_master_key(client):
    client.post("/api/master-key", json={"key": "old"})
    client.post("/api/secrets", json={"name": "K", "value": "v"})
    assert client.post("/api/master-key", json={"key": "new", "reset": True}).status_code == 200
    # value still decryptable
    from ladym.secrets import SecretStore
    assert SecretStore().get("K") == "v"
```

- [ ] **Step 2: 跑红**

Run: `.venv/bin/python -m pytest tests/unit/test_web_app.py -q`
Expected: FAIL（路由不存在）

- [ ] **Step 3: 实现**

编辑 `src/ladym/web/app.py`：顶部 `from ..secrets import SecretStore, get_store`。在 `build_app` 内加路由（与现有 `@app.post("/save")` 同风格）：

```python
from ..secrets import SecretStore

@app.get("/api/secrets")
def api_secrets():
    s = get_store()
    return {"master_key_set": s.has_master_key(), "names": s.list_names()}

@app.post("/api/secrets")
async def api_secrets_set(request: Request):
    payload = await request.json()
    s = get_store()
    s.set(payload["name"], payload["value"])
    return {"ok": True}

@app.delete("/api/secrets/{name}")
def api_secrets_rm(name: str):
    get_store().remove(name)
    return {"ok": True}

@app.post("/api/master-key")
async def api_master_key(request: Request):
    payload = await request.json()
    s = get_store()
    if payload.get("reset"):
        s.reset_master_key(payload.get("key"))
    else:
        s.set_master_key(payload.get("key"))
    return {"ok": True, "master_key_set": True}
```

在 `templates/index.html` 加两块（master key 设置按钮；kv 列表 + 增删表单，HTMX 提交到上述端点）。模板按现有风格写——先读 `index.html` 确认其 form/HTMX 结构后，在 `</form>` 后追加 `<section>` for secrets，`disabled` 态由 `/api/secrets` 的 `master_key_set` 控制（用一段 inline JS 或服务端渲染标志）。

- [ ] **Step 4: 跑绿**

Run: `.venv/bin/python -m pytest tests/unit/test_web_app.py -q`
Expected: 通过

- [ ] **Step 5: Commit**

```bash
git add src/ladym/web/app.py src/ladym/web/templates/index.html tests/unit/test_web_app.py
git commit -m "feat(secret-store): web editor master key + kv management"
```

---

### Task 10: 全量回归 + 文档

**Files:**
- Modify: `CLAUDE.md`（或 `docs/superpowers/specs/2026-07-23-secret-store-design.md` 顶部状态改"已实现"）；可选 `README.md`

- [ ] **Step 1: 全量测试**

Run: `cd /Users/yuhaochen/Documents/codebase/projanvil/ladyM && .venv/bin/python -m pytest tests/ -q`
Expected: 全通过（含新 test_secrets/test_errors/test_make_agent/test_web_app + 旧的 test_cli/test_mcp_server/test_embeddings 回归）

- [ ] **Step 2: lint/type**

Run: `.venv/bin/ruff check src/ladym tests/unit`
Expected: clean（line-length 100）

- [ ] **Step 3: 手动端到端**

```bash
.venv/bin/ladym config set-master-key           # 随机生成
.venv/bin/ladym config set DEEPSEEK_API_KEY sk-replace-me
.venv/bin/ladym config list
# 不带 DEEPSEEK_API_KEY env 时，重连 MCP（去掉 .mcp.json 的 LADYM_LLM_PROVIDER=none 临时补丁）后：
.venv/bin/ladym remember "test" -w scn-demo --db e2e.ladym.db   # 应走 secret store 解到 key，不再崩
.venv/bin/ladym config rm DEEPSEEK_API_KEY
.venv/bin/ladym config reset-master-key newpass
```

Expected: 全程无 traceback；forget/rm 后 remember 回到 ConfigError 友好消息。

- [ ] **Step 4: 文档更新**

把 `docs/superpowers/specs/2026-07-23-secret-store-design.md` 顶部 `状态: 设计完成，待实现` 改为 `状态: 已实现（见 plan 2026-07-23-secret-store.md）`。在 `CLAUDE.md`/`README` 补一段 secret store 用法（`config set-master-key` / `config set`）与安全边界。

- [ ] **Step 5: Commit**

```bash
git add docs/superpowers/specs/2026-07-23-secret-store-design.md CLAUDE.md README.md
git commit -m "docs(secret-store): mark implemented + usage & security boundary"
```

---

## Self-Review

**1. Spec 覆盖**：§1 加密/文件格式 → Task 3；§2 toml 引用 + 取 key 顺序 → Task 4（llm）+ Task 8（embedding）；§3 ConfigError 早 fail → Task 2+4+8；§4 CLI/MCP 错误 → Task 5+7；§5 config 命令组 → Task 6；§6 web → Task 9；依赖 cryptography → Task 1；测试 → 各 Task。✅ 无遗漏。

**2. Placeholder 扫描**：Task 9 Step 3 模板部分指向"先读 index.html 再写"——这是必要的上下文采集（非 placeholder），但给出完整路由代码与面板结构要求。其余每步均有完整代码/命令。✅

**3. 类型/签名一致性**：`SecretStore._master`/`_secrets`/`_dir`/`_cache` 在 Task 3 测试与实现、Task 6 测试（`SecretStore(dir=...)`）、Task 9 中一致；`get_store()` 在 agents.py(Task 4)/embeddings.py(Task 8)/cli.py(Task 6)/web(Task 9) 一致；`_resolve_api_key`/`_missing_key_msg` 仅 agents.py 内部；`ConfigError` 各处 import 路径一致（`..errors`/`.errors`）。`make_agent` 签名不变（`cfg, op`）。`OpenAIEmbedding` 加 `api_key` 参数为纯增量。✅

**4. 范围**：单一 spec、共享 secrets.py，一个 plan 足够；Task 8/9 标可选/后置，可独立交付。✅
