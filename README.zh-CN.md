# LadyM

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Python 3.11+](https://img.shields.io/badge/python-3.11+-blue.svg)](https://www.python.org/)
[![Tests](https://img.shields.io/badge/tests-267-green.svg)](#测试)
[![MCP](https://img.shields.io/badge/MCP-compatible-purple.svg)](#mcp-服务器claude-codecursor-等)
[![Storage](https://img.shields.io/badge/storage-local--first%20SQLite-success.svg)](#架构)

[English](README.md) | **简体中文**

> 一套类脑、多层级的记忆框架，让 LLM Agent 用**一个关键词**就能**召回**工作区的知识与
> 代码分析——而不是每一轮都重新 `Read`、重新 `Grep` 那些同样的文件。

LadyM 把工作区的*理解*——代码分析、决策、技能、事件——缓存为一个分层、可合并、会衰减的
记忆，任何 Agent 都能通过单个关键词召回。基于 **uv + Python 3.11+**、**本地优先的 SQLite +
sqlite-vec**、以及用于代码索引的 **tree-sitter** 构建。通过 **MCP**、**Claude Code Skill**、
**Python SDK**、**CLI** 四种方式暴露——全部调用同一个引擎，行为处处一致。

---

## 痛点

如今的编程 Agent 没有长期记忆。每一轮都要重新 `Read` 同样的文件、重新 `Grep` 同样的符号，
把上下文窗口浪费在重复发现上。普通的向量 RAG 能帮一点忙，但它记不住决策是*为什么*做的、
任务*以前怎么做*的、以及代码库在扁平切片之外*到底是什么样*的。

LadyM 把这种一次性的重复发现，变成**结构化、会演化的记忆**。

## 为什么选 LadyM

| | 你能得到什么 |
|---|---|
| 🧠 **代码记忆与通用记忆同层融合** | L2 把 tree-sitter 代码符号和普通事实放进同一个存储，用同一个激活函数打分。记忆与代码库 RAG 是**一套系统而非两套**——这是其他框架都没有的位置。 |
| 🔒 **本地优先、零配置启动** | 默认 `HashingEmbedding` + `provider="none"`，装完即用：**无需网络、无需下载模型、无需 API key**。一个 SQLite 文件装下一切。 |
| 🧬 **类脑六层 + 双路径** | L0–L4 核心存储（工作 / 情景 / 语义 / 程序 / 联想），外加 System 2 worker 抽取 **L5 心智模型**与 **L6 前瞻意图**，配合 `supersedes` 演化链，让记忆*演化*而非堆积。 |
| 🔌 **四端同源** | MCP server、Claude Code Skill、Python SDK、CLI 全部调用同一个 `Engine`——无论你是在 Claude Code、Cursor、脚本还是终端里，行为完全一致。 |

## 快速上手（30 秒）

```bash
# 1. 安装（离线默认——无需 extras、运行时无需网络）
uv tool install .

# 2. 索引你总是反复 grep 的代码库
ladym index ./src

# 3. 提个问题——直接拿回分析结果，无需 Read/Grep
ladym recall "how does password verification work" --code

# 4. 存一条事实以备后用
ladym remember "auth uses JWT with 24h expiry" --tags auth,security
```

就是这么简单——`ladym` 已经在你的 PATH 上，完全离线可用。不想安装的话，用
`uvx --from . ladym stats` 试一下。

## 横向对比

| | 本地优先 | 融合代码库 RAG | 类脑分层 | 开源 | MCP/Skill/SDK/CLI |
|---|:---:|:---:|:---:|:---:|:---:|
| **LadyM** | ✅ | ✅ L2 融合 | ✅ L0–L6 + System 1/2 | ✅ MIT | ✅ 四端齐全 |
| **mem0** | 部分 | ❌ | 扁平 + 图 | ✅ | SDK |
| **Zep / Graphiti** | 云端 | ❌ | 时序知识图谱 | ✅ | SDK |
| **Letta (MemGPT)** | 自托管 | ❌ | OS 式 Agent 运行时 | ✅ | SDK |
| **腾讯 Hy-Memory** | 云端 SaaS | ❌ | ✅ L1–L6 | ❌ | SDK |

LadyM 借鉴了上述所有项目的学术思想（见 [ARCHITECTURE.md](ARCHITECTURE.md) §10），它独特的押注
是——**把代码库 RAG 融进一套完全本地运行的类脑记忆**。

## 架构

```
   System 1 —— 在线（毫秒级）              System 2 —— 离线 worker（秒到分钟级）
   ┌───────────────────────────┐          ┌───────────────────────────┐
   │ L0  Working      (草稿)   │          │ L5  Mental Model          │
   │ L1  Episodic     (事件)   │  抽取    │     (认知框架)            │
   │ L2  Semantic  (事实+代码) │─────────▶│ L6  Forward Intent        │
   │ L3  Procedural  (playbook)│          │     (前瞻动作)            │
   │ L4  Associative (图谱)    │          └───────────────────────────┘
   └───────────────────────────┘
                 ▲
   读 ◀──── recall(query) : 两层检索（lightweight → deep）+ 反射门控
   写 ───▶ encode / consolidate / proceduralize / link / forget / decay
```

| 层级 | 大脑类比 | LadyM 行为 |
|---|---|---|
| L0 Working | 前额叶皮层（工作记忆） | 进程内有限草稿缓冲 |
| L1 Episodic | 海马体 | 带时间戳的事件；基础级衰减 |
| L2 Semantic | 新皮层 | 合并后的事实**与代码分析**——同一存储 |
| L3 Procedural | 基底神经节 | Playbook + 已验证片段，带版本 |
| L4 Associative | 联想皮层 | Zettelkasten 边，带时序有效期 |
| L5 Mental Model | （元认知） | 从重复事件中抽象出的认知框架 |
| L6 Forward Intent | （规划） | 前瞻性下一步动作，异步沉淀 |

操作：`encode`（感知）、`consolidate`（海马回放 → 新皮层）、`proceduralize`（技能习得）、
`recall`（检索）、`link`（联想）、`forget`（突触修剪）、`reflect`（元认知）。

读路径**只走启发式**——召回过程绝不调用 LLM 裁判，因此又快又可预测。完整的认知科学依据
（借鉴了 MemGPT、mem0、A-MEM、Zep、HyMem、CoALA、ACT-R 以及 Anthropic 上下文工程工作的哪些
部分）见 **[ARCHITECTURE.md](ARCHITECTURE.md)**（仅英文）。

## 安装

### 完整安装（全部功能）

一条命令拉入全部用户向 extras——LLM provider、向量模型、MCP server，以及 `ladym config`
网页编辑器：

```bash
# 从本仓库 clone 安装
uv tool install ".[all]"

# 或直接从 git 安装，无需 clone
uv tool install "git+https://github.com/ProjAnvil/LadyM.git[all]"
```

### 作为全局 CLI（推荐）

```bash
uv tool install .                # 离线默认——无需 extras、运行时无需网络
uv tool install ".[web,llm]"     # LLM provider + `ladym config` 网页编辑器
# 其他 extras 同样可组合：[mcp] [local] [openai] [anthropic]
```

`.` 是密封的核心（仅离线）。它会在你的 PATH（`~/.local/bin/ladym`）上放一个 `ladym` 可执行
文件，与项目 venv 隔离。升级用 `uv tool install . --force --reinstall`，卸载用
`uv tool uninstall ladym`。

### 一次性运行 / 先试后装

```bash
uvx --from . ladym stats
uvx --from . ladym remember "auth uses JWT"
uvx --from . ladym recall "auth"
```

`uvx` 在一次性环境里运行 CLI，不污染你的 PATH。

### 用于开发

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd ladyM
uv venv --python 3.12
uv pip install -e ".[dev]"            # 核心 + 测试/lint 工具，可编辑安装
# 可选 extras 叠加：
uv pip install -e ".[mcp]"            # MCP server（用于 Claude Code / Cursor）
uv pip install -e ".[local]"          # sentence-transformers 向量
uv pip install -e ".[openai]"         # OpenAI 向量
uv pip install -e ".[llm]"            # LLM provider 支持（合并分类器）
uv pip install -e ".[web]"            # FastAPI + HTMX 的 `ladym config` 编辑器
```

要求 Python ≥ 3.11（用到 `enum.StrEnum`）。`sqlite-vec` 以 wheel 形式分发——macOS/Linux/Windows
都无需原生工具链。

## 集成

四种前端都调用同一个 `Engine`，行为处处一致。

### MCP 服务器（Claude Code、Cursor 等）

加到你的 MCP 客户端配置里：

```json
{
  "mcpServers": {
    "ladym": {
      "command": "ladym",
      "args": ["serve", "--db", "/absolute/path/to/ladym.db"]
    }
  }
}
```

服务器暴露九个工具——`recall`、`remember`、`record_event`、`search_code`、`index_code`、
`consolidate`、`stats`、`link`、`forget`——详见 [src/ladym/mcp/server.py](src/ladym/mcp/server.py)。
`record_event` 记录一条 L1 情景事件，喂给 System 2 worker 做合并（L1 → L2）以及受门控的 L5/L6
抽取器（记录约 3 条以上即可触发这些周期）。

### Claude Code Skill

开箱即用的 skill 在 [skills/ladym-recall.md](skills/ladym-recall.md)。把它复制到你的
`.claude/skills/` 目录，Agent 就能用一个关键词把工作区记忆拉进上下文，而不必重读文件。

### Python SDK

```python
from ladym import Engine, Config, Layer

eng = Engine(Config(db_path="ladym.db", workspace="myteam"))

# 索引一次（增量——跳过未改动文件）
eng.index_code("./src")

# 写路径
eng.semantic.put_fact("auth uses JWT with 24h expiry", tags=["auth"])
eng.episodic.record(agent="claude", action="fixed login bug", outcome="success")

# 读路径：两层检索，ACT-R 激活排序
resp = eng.recall("how does auth work")
for r in resp.results:
    print(f"{r.score:.3f} [{r.memory.layer}] {r.memory.summary}")

# 仅代码的快捷方式
for r in eng.search_code("verify password").results:
    print(r.memory.metadata["qualified_name"], r.memory.content[:80])

# 认知操作
eng.consolidate()         # L1 事件 → L2 事实（ADD/UPDATE/DELETE/NOOP）
eng.proceduralize()       # 重复的成功事件 → L3 playbook
eng.decay(dry_run=True)   # ACT-R 基础级遗忘
eng.link(a_id, b_id, "depends_on")  # Zettelkasten 边
```

### LangGraph

LadyM 提供可选的 LangGraph 集成（安装：`pip install 'ladym[langgraph]'`），把 ladyM
作为 LangGraph / LangChain agent 的长期记忆层。两条等价路径：

- **Tools（工具）** — `create_ladym_tools(engine)` 返回 LangChain 工具（`recall_memory`、
  `remember_fact`、`search_code`），适合 LLM 自主决定何时存取的 ReAct 型 agent。
- **Nodes（节点）** — `create_recall_node(engine)` / `create_retain_node(engine)` 返回图节点，
  每轮自动把相关记忆作为 SystemMessage 注入、并自动存下本轮对话（通过
  `config["configurable"]["user_id"]` 支持按用户隔离 workspace）。

完整示例见 [`docs/langgraph-integration.md`](docs/langgraph-integration.md)。

### CLI

```bash
ladym index ./src                      # 索引代码库（增量）
ladym recall "auth flow"               # 跨代码与事实召回
ladym remember "..." --tags auth       # 存一条事实
ladym record --agent claude --action "fixed login bug" --outcome success
ladym consolidate                       # L1 → L2
ladym worker --once                     # 触发 System 2 的 L5/L6 抽取
ladym stats                             # 查看记忆里有什么
```

每条结果都带符号的**身份、签名、docstring、代码片段、源文件**——外加可通过符号图查询的
**调用者/被调用者**。正是这些让 Agent 不必重读。

## 配置

| 环境变量 | 默认值 | 用途 |
|---|---|---|
| `LADYM_DB` | `./ladym.db` | SQLite 路径（默认每个项目一个 DB） |
| `LADYM_WORKSPACE` | `default` | 共享 DB 内的多工作区隔离 |
| `LADYM_EMBEDDING` | `hashing` | `hashing` / `st` / `openai` |
| `LADYM_EMBEDDING_MODEL` | （provider 默认） | `st` 或 `openai` 的模型名 |
| `LADYM_EMBEDDING_BASE_URL` | （provider 默认） | 覆盖向量 API base URL（OpenAI/Ollama 兼容） |
| `LADYM_LLM_PROVIDER` | （无） | LLM provider 名（如 `openai`、`ollama`）——需 `pip install 'ladym[llm]'` |
| `LADYM_LLM_BASE_URL` | （provider 默认） | 覆盖 LLM API base URL（OpenAI/Ollama 兼容） |
| `LADYM_LLM_MODEL` | （provider 默认） | 合并分类器的 LLM 模型名 |

`base_url` 让你能把向量和 LLM 调用指向任意 OpenAI/Ollama 兼容端点（如 vLLM、LiteLLM、本地
Ollama）。所有值也都可在 `Config` dataclass 上覆盖——见 [src/ladym/config.py](src/ladym/config.py)。

### Secret store（静态加密的密钥）

Provider API key 可以加密（AES-256-GCM）存到 `~/.ladyM/`，而不是贴进 shell rc 或 CI secrets：

```
ladym config set-master-key              # 首次：生成随机 master key
ladym config set DEEPSEEK_API_KEY sk-... # 存入 key（加密）
ladym config list                        # 列名称（绝不回显 value）
ladym config rm DEEPSEEK_API_KEY         # 删除
ladym config reset-master-key <newpass>  # 换 master key，所有 secret 原地重加密
```

密钥解析顺序——**LLM** provider：`Config.api_key` 明文字段（开发逃生舱，默认关）→ secret store
（`~/.ladyM/secrets.enc`）→ `api_key_env` 指向的进程环境变量。**Embedding** provider 跳过明文
层，顺序为：secret store → 环境变量。三者皆无时，命令快速失败并给出单行 `ConfigError`，点明
环境变量名和修复命令（exit 1；MCP 工具返回结构化错误而非 traceback）。

**安全边界：** secret store 仅保证*静态加密*——防止明文经 `cat secrets.enc`、肩窥或误贴进
chat/log/commit 泄露。它**不**防御 `~/.ladyM/` 整目录被窃：master key 与密文同目录（这是为
非交互式 MCP / 后台 worker 无需口令即可解密所做的权衡）。需要更强隔离时，把 `~/.ladyM/` 放在
加密存储上，并依赖 OS 文件权限（目录 `0700`、文件 `0600`）。**丢失 `master.key` = 所有 secret
不可恢复**，请务必备份。

**CLI extras：**

| 命令 | 功能 | 安装 |
|---|---|---|
| `ladym config` | 本地网页配置编辑器（FastAPI + HTMX，编辑 `ladym.toml`） | `pip install 'ladym[web]'` |
| `ladym worker` | 后台 System 2 合并守护进程 | 核心；flags：`--once`、`--interval N` |

## 测试

```bash
uv run pytest                  # 267 个测试，约 3 秒，完全离线
uv run pytest tests/integration -v
uv run pytest --cov=ladym      # 覆盖率报告
uv run ruff check src/ tests/  # lint
```

整套测试**无需网络、无需下载模型**即可运行——默认的 `HashingEmbedding` 是确定性、零依赖的，
所以 CI 是密封的。基于 sqlite-vec 的路径有自己的回归测试，见
[tests/integration/test_sqlite_vec.py](tests/integration/test_sqlite_vec.py)。

## 文档

- **[ARCHITECTURE.md](ARCHITECTURE.md)**——完整设计：六层记忆、认知操作、两层检索、ACT-R
  激活函数、存储布局，以及代码索引子系统。*仅英文。*
- **[scenarios/](scenarios/)**——可执行的端到端场景（S01 写入/召回、S03 代码索引、S07 L5 心智
  模型、S08 L6 前瞻意图、S09 注意力门控……），同时充当活的规格说明。
- **[tests/](tests/)**——可执行的规格说明。

## 状态与路线图

✅ 六层引擎、两层召回、ADD/UPDATE/DELETE/NOOP 合并、proceduralize、衰减、支持
Python/JS/TS/Go/Rust/Java/C/C++ 的 tree-sitter 索引器、MCP server、CLI、Skill、可插拔 provider +
TOML 配置、System 2 后台 worker、L5 心智模型 / L6 前瞻意图抽取、`ladym config` 网页编辑器、加密
secret store、267 个测试。

🚧 下一步：GraphRAG 风格的跨文件引用解析，以及多模态事件。

## 贡献

欢迎贡献。最快的参与方式：

1. 提交前跑 `uv run pytest`（267 个测试，密封）和 `uv run ruff check`。
2. 为任何新行为在 [scenarios/](scenarios/) 下加场景，或在 [tests/](tests/) 下加测试——它们就是
   规格说明。
3. 保持读路径只走启发式；任何 LLM 调用都路由到 System 2 worker。

任何层级或操作背后的设计理由，请先读 [ARCHITECTURE.md](ARCHITECTURE.md)，大改动请先开 issue
讨论。

## 引用

如果 LadyM 对你的研究或项目有所启发，欢迎引用：

```bibtex
@misc{ladym2026,
  title  = {LadyM: A brain-inspired, multi-tier memory framework for LLM agents and codebase RAG},
  author = {ProjAnvil},
  year   = {2026},
  url    = {https://github.com/ProjAnvil/LadyM}
}
```

## 许可证

[MIT](LICENSE) © ProjAnvil
