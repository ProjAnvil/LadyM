# Secret Store + 缺 key 友好报错 — 设计

- **日期**: 2026-07-23
- **状态**: 设计完成，待实现
- **相关**: `2026-07-21-providers-config-control-plane-design.md`（provider/api_key_env 机制）、`2026-07-22-attention-gate-noise-dup-prefix-design.md`（gate）

## 背景与动机

ladym 的 LLM/embedding provider key 目前只能经环境变量配置（`ladym.toml` 的 `api_key_env` 指向 env var 名）。两个痛点:

1. **key 管理复杂**: env var 不持久（新 shell 丢失）、MCP server 还要在 `.mcp.json` 单独配 env（第三处），多处易不一致，secret 又不能明文落 toml（会被 `_strip_secrets` 丢弃）。
2. **缺 key 时崩溃丑陋**: 配了 `provider != "none"` 但缺 key 时，`make_agent`（`providers/agents.py:92`）不校验就构造 provider，延迟到 `ChatOpenAI.__init__` 才炸成几十行 `OpenAIError` traceback——成熟产品不该暴露给用户。

本设计同时解决两者:
- **加密 secret store**: 交互式登记 key，加密落盘，toml 用 `KEY_NAME` 引用。跨平台、无交互解密（MCP/worker 可用）。
- **缺 key 早 fail + 友好报错**: `make_agent` 在 `provider != "none"` 且 key 缺失时立刻抛明确异常；CLI/MCP 兜住，输出一行修复指引（非 traceback）。

## 目标

- 跨平台（macOS/Linux/Windows），无原生 keychain 依赖。
- key 可交互登记（CLI + web），加密落盘，toml **零改动**引用。
- 缺 key 时 fail fast + 明确指引（**反 fallback**，符合用户偏好——见 `user-design-pref-no-fallback`）。

## 非目标 / 安全边界（必须在代码注释 + 文档明示）

- **不防 `~/.ladyM/` 整体泄露**: master key 与密文同目录，拿到目录即可解。这是"跨平台 + 无交互"的明确权衡。
- **只防"明文裸奔"**: `cat` 文件、屏幕瞥见、明文误传到日志/聊天/提交。
- 不做 passphrase / keychain（MCP/worker 非交互约束）。
- master key 文件不存用户原始 KEY（存 HKDF 派生值），降低"用户复用密码"被连带泄露的风险。

## 架构

### 1. Secret store 模块（新增 `src/ladym/secrets.py`）

**文件布局**（均在 `~/.ladyM/`，权限 `0600`，目录 `0700`）:
- `master.key` — base64 编码的 **32 字节 AES key**（派生值，非用户原始 KEY）。
- `secrets.enc` — 逐行 `KEY_NAME = <base64>`，其中 `<base64> = base64(nonce(12B) ‖ ciphertext ‖ tag(16B))`，AES-256-GCM。

**master key 来源**:
- 用户指定 KEY → `HKDF-SHA256(key_material=KEY.encode(), salt=None, info=b"ladym-master-key")` → 32 字节 → 存。**不存原始 KEY**。
- 随机生成 → `os.urandom(32)` → 存。

**Python API**（`ladym.secrets.SecretStore`）:
| 方法 | 行为 |
|------|------|
| `has_master_key() -> bool` | master.key 是否存在 |
| `require_master_key()` | 不存在则抛 `ConfigError`（提示 `ladym config set-master-key`） |
| `get(name) -> str \| None` | 解密读；内存 LRU 缓存 |
| `set(name, value)` | 加密写（先 `require_master_key()`）；原子替换 secrets.enc |
| `list_names() -> list[str]` | 列 KEY_NAME，不碰 value |
| `remove(name)` | 删一条；原子替换 |
| `set_master_key(key: str \| None)` | None→随机；派生后存 master.key。**若 secrets.enc 已有 kv → 报错**（会无法解密；应走 `reset_master_key`） |
| `reset_master_key(new_key: str \| None)` | 原子 re-encrypt 全部 kv（见下） |

**原子性**: 所有写操作走 `临时文件 + os.replace`；`reset_master_key` 先在内存用旧 key 解密全部 kv、用新 key 重新加密、计算好两份新文件内容后一并 `os.replace`，任一步失败则不变。

**`reset_master_key(new_key)` 语义**:
1. `require_master_key()`（无旧 master key → `ConfigError`，因为无法解密旧 kv）。
2. 读旧 master.key → 解密全部 kv 到内存 dict。
3. 新 key 派生新 AES key → 重新加密全部 kv。
4. 原子写新 secrets.enc + 新 master.key（先写临时、`os.replace`）。

### 2. toml 引用 + `make_agent` 取 key 顺序

toml **不改**: `api_key_env = "DEEPSEEK_API_KEY"` 保持原义。`make_agent`（`providers/agents.py`）取 key 优先级（llm 与 embedding 对称）:

1. `allow_plaintext` 明文（dev 逃生舱，默认关，保持现有最高优先级）
2. **secret store**: `SecretStore.get(api_key_env)` ← 新增
3. env var 明文: `os.environ.get(api_key_env)`

provider 为 `embedding` 时同理——改 `storage/embeddings.py:make_provider`（取 `embedding_api_key`），按相同三级顺序取 key，`provider != "none"` 且缺失时抛 `ConfigError`。

### 3. `ConfigError` + 早 fail

- 新增 `src/ladym/errors.py`，定义 `ConfigError(Exception)`（项目尚无独立 errors 模块——`EmbeddingProviderError` 暂留 `storage/embeddings.py` 不动；本次只加 `ConfigError` 集中放置）。
- `make_agent`: `provider != "none"` 且上面三级都拿不到 key → 抛 `ConfigError`。消息自包含修复指引:
  > `ladym: LLM provider "openai" 缺 API key — "DEEPSEEK_API_KEY" 既未在 secret store 登记也未设为环境变量。先 \`ladym config set-master-key\`，再 \`ladym config set DEEPSEEK_API_KEY <value>\`；或在 ladym.toml 设 llm.provider="none" 走离线模式。`
- 比"构造注定失败的 provider、几十行后炸 `OpenAIError`"**更早、更明确**——这是 fail fast，**不是 fallback**（不静默退化到 none）。

### 4. CLI/MCP 错误处理

- **CLI**（`cli.py`）: 顶层兜 `ConfigError` 和 provider 调用类错误 → 一行消息 + `exit 1`；`--debug`（全局 flag）才打 traceback。
- **MCP**（`mcp/server.py`）: 工具调用包 `try/except ConfigError` → 返回结构化错误 JSON（含修复指引），不让 traceback 冒到 agent。

### 5. CLI 命令（`ladym config` 组）

统一子命令风格（与现有 `remember`/`record`/`index`/`consolidate`/`forget`/`worker` 一致），**不用** `--masterKey` flag（避免命令组里 flag 与子命令混用的 Typer 解析歧义）:

```
ladym config                          # 默认 = 启动 web editor（保持现有行为）
ladym config set KEY VALUE            # 写 kv（v 加密；无 master key → ConfigError）
ladym config set-master-key [KEY]     # 设 master key；无参 → 随机生成强 KEY 并打印
ladym config reset-master-key [KEY]   # 换 master key（原子 re-encrypt 全部 kv）
ladym config list                     # 列 KEY_NAME（不打印 value）
ladym config rm KEY                   # 删一条
```

- `set-master-key` / `reset-master-key` 带 KEY 参数直接用；无参则 `getpass` 隐藏输入（`reset` 无参可随机）。
- `set-master-key` 在已有 kv 时报错（应走 `reset-master-key`），避免旧 kv 变成无法解密的死数据。

### 6. Web editor 扩展（`ladym config`）

现有 web config editor（编辑 toml，需 `web` extra）扩展两块:
- **master key 区**: set / reset 按钮（reset 二次确认，提示会 re-encrypt 全部 kv）。
- **kv 管理区**: list（仅 name）、add/edit（value 输入框）、rm。**仅 master key 已设时启用**；保存时调后端加密。
- 后端 HTTP API: `GET /api/secrets`（list names）、`POST /api/secrets`（set）、`DELETE /api/secrets/{name}`、`POST /api/master-key`（set/reset）。
- CLI 与 web 共用同一份 `~/.ladyM/secrets.enc`，互看互改。

## 数据流

- **写 key**: `config set K V` → `require_master_key()` → `set(K,V)`（HKDF 派生 AES key → AES-GCM 加密 V → 原子替换 secrets.enc）→ 失效 get 缓存。
- **读 key（make_agent）**: `provider != "none"` → 取 key（plaintext > `SecretStore.get(api_key_env)` > env）→ 空 → `ConfigError`。
- **reset master key**: 旧 AES key 解密全部 kv → `HKDF(new_key)` 重新加密 → 原子替换 secrets.enc + master.key → 清 get 缓存。

## 测试计划

- `tests/unit/test_secrets.py`:
  - 加解密往返（set→get 一致）。
  - master.key 首次生成 + 文件权限 `0600`、目录 `0700`。
  - `get/set/list_names/remove` 行为。
  - `set_master_key`: 用户指定 KEY（HKDF 派生，不存原始）、无参随机生成。
  - `set_master_key` 在已有 kv 时报错。
  - `reset_master_key`: re-encrypt 后旧 kv 仍可正确解密；原子性（模拟写失败 → 文件不变）。
  - `require_master_key` 抛 `ConfigError`。
  - `tmp_path` 隔离 `~/.ladyM`（经环境变量/参数重定向 home）。
- `tests/unit/test_agents.py`（或 test_providers）:
  - 取 key 三级优先级（plaintext > store > env）。
  - `provider != "none"` + 三级全空 → `ConfigError`（消息含指引关键词）。
  - `provider == "none"` → 返回 None（不受影响）。
- `tests/unit/test_cli.py`:
  - `config set` 无 master key → 友好错误、`exit 1`、无 traceback。
  - `--debug` 下 `config set` 失败 → 有 traceback。
  - `set-master-key` / `set` / `list` / `rm` 端到端（用 tmp home）。
- `tests/unit/test_mcp_server.py`:
  - `remember` 缺 key → 结构化错误 JSON（含指引），无 traceback 冒泡。

## 依赖

- `cryptography`（AES-GCM）— **当前不是** ladym 依赖（已核实 `pyproject.toml`）；加为**核心依赖**（secret store 是本次要交付的基础能力，不做可选 extra，避免运行时缺库又得退化处理——与"反 fallback"一致）。HKDF 用 `hashlib`（标准库），无额外依赖。
