# LadyM

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Tests](https://img.shields.io/badge/tests-go%20test%20.%2F...-green.svg)](#测试)
[![MCP](https://img.shields.io/badge/MCP-compatible-purple.svg)](#mcp-服务器claude-codecursor-等)
[![Storage](https://img.shields.io/badge/storage-local--first%20SQLite-success.svg)](#架构)

[English](README.md) | **简体中文**

> 一套类脑、多层级的记忆框架，让 LLM Agent 用**一个关键词**就能**召回**工作区的知识与
> 代码分析——而不是每一轮都重新 `Read`、重新 `Grep` 那些同样的文件。

LadyM 把工作区的*理解*——代码分析、决策、技能、事件——缓存为一个分层、可合并、会衰减的
记忆，任何 Agent 都能通过单个关键词召回。用 **Go** 写成**单一静态二进制、零 cgo**：
SQLite 基于 [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)，向量检索是进程内
暴力余弦索引，代码索引用 [gotreesitter](https://github.com/odvcencio/gotreesitter)
（纯 Go 的 tree-sitter 运行时）。通过 **MCP**、**Claude Code Skill**、**Go SDK**、
**CLI** 四种方式暴露——全部调用同一个引擎，行为处处一致。

---

## 0.4.0 更新

- **双版本** —— 一个仓库、两种构建。**个人版**：你熟悉的单二进制体验（SQLite 或
  Postgres,管理台内嵌）。**企业版**:`go build -tags enterprise` 产出纯 Postgres 的
  微服务，`ladym` 二进制里零 SQLite、零管理台代码。
- **REST API 服务** —— 所有 MCP 工具现在都可以通过 HTTP 调用（`ladym serve --http`),
  另有 `/healthz` 和 `/api/metrics` 供运维使用。
- **轻量认证** —— 数据库级的用户名/密码（bcrypt + HTTP Basic)，不做重型 IAM。支持
  免密用户，默认关闭（`[auth] enabled`)；用 `ladym user add/list/delete/passwd` 管理。
- **Vue 管理台** —— 真正的前端（Vue 3 + Vite)，含登录、Memories、Users、Stats 四个
  页面和完整的 CRUD API。个人版内嵌于二进制；企业版拆成独立的 `ladymconsole` 配置中心
  微服务，与 ladym 节点共用同一个 Postgres。
- **`client/golang` SDK** —— 面向 HTTP API 的 Go 客户端，其他 Go 程序两行代码即可把
  LadyM 作为中间件集成：`client.New(addr, client.WithAuth(u, p))`。
- **企业版 compose 栈** —— `docker-compose.enterprise.yml` 提供三层部署：nginx 网关
  作为唯一对外节点，在内网中负载均衡到 ladym API 副本和配置中心，底层是
  Postgres + pgvector。

## 0.3.0 更新

- **完整 Go 重写** —— LadyM 现在是单一静态二进制，**无 Python 依赖、零 cgo**。
  用 `go install` 安装或从源码构建即可，无需任何额外环境。
- **纯 Go 存储栈** —— SQLite 跑在 `modernc.org/sqlite` 上，原先的 `sqlite-vec` 扩展被
  进程内暴力余弦索引取代（[storage/vector_index.go](storage/vector_index.go)），二进制保持
  密封、可在任意平台交叉编译。
- **纯 Go tree-sitter** —— 代码索引使用 `gotreesitter`，加载与上游 tree-sitter 相同的
  parse table，但无需原生构建。
- **一个引擎，所有前端** —— MCP server、CLI、Go SDK 全部封装同一个 `engine.Engine`，
  在任何宿主里行为完全一致。

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
| 🔒 **本地优先、零配置启动** | 默认 `HashingEmbedding` + `llm.provider="none"`，装完即用：**无需网络、无需下载模型、无需 API key**。一个 SQLite 文件装下一切。 |
| 🧬 **类脑六层 + 双路径** | L0–L4 核心存储（工作 / 情景 / 语义 / 程序 / 联想），外加 System 2 worker 抽取 **L5 心智模型**与 **L6 前瞻意图**，配合 `supersedes` 演化链，让记忆*演化*而非堆积。 |
| 🔌 **四端同源** | MCP server、Claude Code Skill、Go SDK、CLI 全部调用同一个 `Engine`——无论你是在 Claude Code、Cursor、脚本还是终端里，行为完全一致。 |

## 快速上手（30 秒）

```bash
# 1. 安装（单一静态二进制——运行时无需网络、无需 API key）
go install github.com/ProjAnvil/LadyM/cmd/ladym@latest

# 2. 索引你总是反复 grep 的代码库
ladym index ./src

# 3. 提个问题——直接拿回分析结果，无需 Read/Grep
ladym recall "how does password verification work" --code

# 4. 存一条事实以备后用
ladym remember "auth uses JWT with 24h expiry" --tags auth,security
```

就是这么简单——`ladym` 已经在你的 PATH 上，完全离线可用。

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

### 作为全局 CLI（推荐）

```bash
go install github.com/ProjAnvil/LadyM/cmd/ladym@latest
```

这会在你的 Go bin 路径（`$(go env GOPATH)/bin`）放一个静态 `ladym` 二进制。构建需要
Go 1.26+；二进制本身无任何运行时依赖。

### 从源码构建

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd LadyM
go build -o bin/ladym ./cmd/ladym
./bin/ladym stats
```

也可以用安装脚本，构建后复制到 `~/.local/bin`：

```bash
scripts/install.sh            # 或：scripts/install.sh /custom/bin/dir
```

### 用于开发

```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd LadyM
go build ./...                # 编译全部
go test ./...                 # 完整测试套件，完全离线
```

本 module 为纯 Go（`github.com/ProjAnvil/LadyM`），只有少量库依赖——macOS/Linux/Windows
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

服务器通过 stdio 讲 MCP 协议，暴露九个工具——`recall`、`remember`、`record_event`、
`search_code`、`index_code`、`consolidate`、`stats`、`link`、`forget`——详见
[mcp/server.go](mcp/server.go)。`record_event` 记录一条 L1 情景事件，喂给 System 2 worker
做合并（L1 → L2）以及受门控的 L5/L6 抽取器（记录约 3 条以上即可触发这些周期）。

### Claude Code Skill

开箱即用的 skill 在 [skills/ladym-recall.md](skills/ladym-recall.md)。把它复制到你的
`.claude/skills/` 目录，Agent 就能用一个关键词把工作区记忆拉进上下文，而不必重读文件。

### Go SDK

`ladym` 包是引擎的一站式 facade（[ladym/ladym.go](ladym/ladym.go)）：

```go
import "github.com/ProjAnvil/LadyM/ladym"

eng, err := ladym.NewEngine(ladym.DefaultConfig())
if err != nil {
	// handle error
}
defer eng.Close()

// 索引一次（增量——跳过未改动文件）
report, err := eng.IndexCode("./src", false, "", nil)

// 写路径
eng.Remember("auth uses JWT with 24h expiry", ladym.LayerSemantic, ladym.TypeFact,
	[]string{"auth"}, nil, "sdk", "")
eng.RecordEvent("claude", "fixed login bug", "", "success", nil, nil)

// 读路径：两层检索，ACT-R 激活排序
resp, err := eng.Recall("how does auth work", "", 8, nil, nil, 0)
for _, r := range resp.Results {
	fmt.Printf("%.3f [%s] %s\n", r.Score, r.Memory.Layer, r.Memory.Summary)
}

// 仅代码的快捷方式
codeResp, err := eng.SearchCode("verify password", 8, "")

// 认知操作
eng.Consolidate("", 0)          // L1 事件 → L2 事实（ADD/UPDATE/DELETE/NOOP）
eng.Proceduralize("", 0)        // 重复的成功事件 → L3 playbook
eng.Decay("", true, 0, 0)       // ACT-R 基础级遗忘（dry run）
eng.Link(srcID, dstID, "depends_on") // Zettelkasten 边
```

一次性辅助函数——`ladym.Recall(query, dbPath, workspace, topK)`、
`ladym.Remember(...)`、`ladym.IndexCode(...)`——会开一个短生命周期的 engine，执行后关闭，
适合不想管理生命周期的脚本。

### Python SDK（MCP wrapper）

Python 应用通过 [`wrapper/py`](wrapper/py/) 使用 Go 引擎——一个轻量类型化客户端，
它会拉起 `ladym serve` MCP server（stdio 上的 JSON-RPC 2.0），把九个工具
（`recall`、`remember`、`record_event`、`search_code`、`index_code`、`consolidate`、
`stats`、`link`、`forget`）暴露为 Python 方法。Python 侧不含任何记忆逻辑，
Go 二进制是唯一事实源。

```python
from ladym_wrapper import LadymClient        # 同步
from ladym_wrapper import AsyncLadymClient  # 异步

with LadymClient() as client:
    client.remember("deploys go through Argo CD", source="notes")
    hits = client.recall("how do we deploy?")
```

Go 二进制的解析顺序：`binary=` 参数 → `LADYM_BIN` 环境变量 → `PATH` → 仓库内的
`bin/ladym`。要求 Python ≥ 3.12，详见
[`wrapper/py/README.md`](wrapper/py/README.md)。（完整的 0.2.x Python 实现保留在
[`python`](https://github.com/ProjAnvil/LadyM/tree/python) 分支。）

### 注入你自己的 langchain-golang 模型

如果你的应用已经配好了 langchain-golang 的 chat / embedding 模型（含 api key、
base URL、model），可以包装后直接通过 `ModelRouting` 交给 Engine——无需在 LadyM 的配置里
重复声明凭证（[adapter/adapter.go](adapter/adapter.go)）：

```go
import (
	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/ladym"
	"github.com/projanvil/langchain-golang/core/modelconfig"
	"github.com/projanvil/langchain-golang/partners/openai"
)

eng, err := ladym.NewEngineWithModels(ladym.DefaultConfig(), &adapter.ModelRouting{
	Consolidate: adapter.WrapChatModel(openai.NewChatModel(
		modelconfig.WithModel("gpt-4o"),
		modelconfig.WithAPIKey(key),
		modelconfig.WithBaseURL(url),
	), ""),
	AttentionGate: adapter.WrapChatModel(openai.NewChatModel(
		modelconfig.WithModel("gpt-4o-mini"),
		modelconfig.WithAPIKey(key),
	), ""),
	Embedding: adapter.WrapEmbeddings(openai.NewEmbeddings(
		modelconfig.WithModel("text-embedding-3-small"),
		modelconfig.WithAPIKey(key),
	)),
})
```

五个认知操作（`consolidate`、`proceduralize`、`attention_gate`、
`l5_mental_model`、`l6_forward_intent`）各自可以绑定不同模型；
未设置的操作回退到 Config。

### LangGraph

[langgraph/](langgraph/) 包把 LadyM 集成为 langchain-golang LangGraph / LangChain agent
的长期记忆层。两条等价路径：

- **Tools（工具）** — `langgraph.CreateTools(eng, workspace, defaultTopK)` 返回 LangChain
  工具（`recall_memory`、`remember_fact`、`search_code`），适合 LLM 自主决定何时存取的
  ReAct 型 agent。
- **Nodes（节点）** — `langgraph.CreateRecallNode(eng, topK, prefix, wsFn)` /
  `langgraph.CreateRetainNode(eng, wsFn)` 返回图节点 `NodeFunc`，每轮自动把相关记忆作为
  SystemMessage 注入、并自动存下本轮对话。`langgraph.WorkspaceFromUserID()` 从运行期上下文
  的 `user_id` 解析 workspace，实现按用户隔离。

设计走查见 [`docs/langgraph-integration.md`](docs/langgraph-integration.md)。

### CLI

```bash
ladym index ./src                      # 索引代码库（增量）
ladym recall "auth flow"               # 跨代码与事实召回
ladym recall "auth flow" --code --json # 仅代码、机器可读输出
ladym remember "..." --tags auth       # 存一条事实
ladym record --agent claude --action "fixed login bug" --outcome success
ladym consolidate                       # L1 → L2
ladym worker --once                     # 触发 System 2 的 L5/L6 抽取
ladym stats                             # 查看记忆里有什么
```

用 `ladym <command> --help` 查看各命令 flag；`ladym completion <shell>` 生成 shell 自动补全。
每条索引结果都带符号的**身份、签名、docstring、代码片段、源文件**——外加可通过符号图查询的
**调用者/被调用者**。正是这些让 Agent 不必重读。

## 配置

配置按以下优先级解析（从高到低）：CLI flag → `LADYM_*` 环境变量 → `./ladym.toml` →
`~/.ladym/config.toml` → 内建默认值。`ladym --config <path>` 可在其上再叠加一个 TOML 文件。

| 环境变量 | 默认值 | 用途 |
|---|---|---|
| `LADYM_DB` | `./ladym.db` | SQLite 路径（默认每个项目一个 DB） |
| `LADYM_WORKSPACE` | `default` | 共享 DB 内的多工作区隔离 |
| `LADYM_EMBEDDING` | `hashing` | `hashing` / `openai` / `ollama` / `http` |
| `LADYM_DICT_DIR` | `~/.ladyM/dict` | CJK 分词词典目录；微服务部署指向共享卷 |
| `LADYM_EMBEDDING_MODEL` | （provider 默认） | 托管向量 provider 的模型名 |
| `LADYM_EMBEDDING_BASE_URL` | （provider 默认） | 覆盖向量 API base URL（OpenAI/Ollama 兼容） |
| `LADYM_EMBEDDING_API_KEY_ENV` | （无） | 存放向量 API key 的环境变量名 |
| `LADYM_LLM_PROVIDER` | `none` | `none` / `openai` / `anthropic` / `ollama` / `http` |
| `LADYM_LLM_BASE_URL` | （provider 默认） | 覆盖 LLM API base URL（OpenAI/Ollama 兼容） |
| `LADYM_LLM_MODEL` | `gpt-4o-mini` | 合并分类器的 LLM 模型名 |
| `LADYM_LLM_API_KEY_ENV` | （无） | 存放 LLM API key 的环境变量名 |
| `LADYM_ENABLE_WAL` | `true` | 开启 SQLite WAL journal 模式(默认开启,便于多进程共享同一个 db;在不支持 WAL 的文件系统上设为 `false`) |

`base_url` 让你能把向量和 LLM 调用指向任意 OpenAI/Ollama 兼容端点（如 vLLM、LiteLLM、本地
Ollama）；`http` provider 还可以模板化任意 embedding/LLM HTTP API。更细的开关
（`LADYM_EMBEDDING_TIMEOUT_S`、`LADYM_LLM_MAX_TOKENS`、`LADYM_LLM_TEMPERATURE`、
`[agents]` 下的 per-op 覆盖，以及 recall/activation 权重）放在 TOML 或 `config.Config`
结构体上——见 [config/config.go](config/config.go)。TOML 里的密钥字面值会被拒绝并告警；
请用 `<name>_env` 间接引用或下面的 secret store。

### Secret store（静态加密的密钥）

Provider API key 可以加密（AES-256-GCM）存到 `~/.ladyM/`，而不是贴进 shell rc 或 CI secrets：

```
ladym config set-master-key              # 首次：生成随机 master key
ladym config set DEEPSEEK_API_KEY sk-... # 存入 key（加密）
ladym config list                        # 列名称（绝不回显 value）
ladym config rm DEEPSEEK_API_KEY         # 删除
ladym config reset-master-key <newpass>  # 换 master key，所有 secret 原地重加密
```

密钥解析顺序——**LLM** provider：`Config.LLMAPIKey` 明文字段（开发逃生舱，仅在
`allow_plaintext_secrets` 开启时生效，默认关）→ secret store（`~/.ladyM/secrets.enc`）→
`api_key_env` 指向的进程环境变量。**Embedding** provider 跳过明文层，顺序为：secret store →
环境变量。三者皆无时，命令快速失败并给出单行 `ConfigError`，点明环境变量名和修复命令
（exit 1；MCP 工具返回结构化错误而非堆栈）。

**安全边界：** secret store 仅保证*静态加密*——防止明文经 `cat secrets.enc`、肩窥或误贴进
chat/log/commit 泄露。它**不**防御 `~/.ladyM/` 整目录被窃：master key 与密文同目录（这是为
非交互式 MCP / 后台 worker 无需口令即可解密所做的权衡）。需要更强隔离时，把 `~/.ladyM/` 放在
加密存储上，并依赖 OS 文件权限（目录 `0700`、文件 `0600`）。**丢失 `master.key` = 所有 secret
不可恢复**，请务必备份。

**更多 CLI 能力：**

| 命令 | 功能 |
|---|---|
| `ladym serve --http :8080` | HTTP 数据面 API（`/api/*`，可选 Basic 认证）+ 挂在 `/` 的嵌入式管理台（登录、记忆 CRUD、用户管理、统计） |
| `ladym config <sub>` | 加密 secret store：`set` / `set-master-key` / `reset-master-key` / `list` / `rm` |
| `ladym worker` | 后台 System 2 合并守护进程；flags：`--once`、`--interval N`（秒） |

## 测试

```bash
go test ./...            # 完整套件，完全离线
go test ./engine/ -v     # 单个包
go vet ./...             # lint
```

整套测试**无需网络、无需下载模型**即可运行——默认的 `HashingEmbedding` 是确定性、完全离线的，
所以 CI 是密封的。

### CJK 分词（中文 / 日文 / 韩文）

CJK 文本开箱即用（逐字回退分词）。要获得词典级的词级分词（更好的召回质量），下载一个
词典变体即可——在控制台 **Settings → Memory** 选择变体（`zh` 简体+繁体（默认）、
`zh_s`、`zh_t`、`jp` 日文）后点「下载词典」，或：

- **API**：`POST /api/cjk_dict/download`（管理员；body `{"dict": "zh"}`，可选
  `"mirror_base"` 指向按 gse 仓库布局的内网镜像）；`GET /api/cjk_dict` 查询状态
  与变体枚举；`DELETE /api/cjk_dict` 删除。
- **微服务多机**：所有实例指向同一词典目录——常驻命令（`serve` / `worker` /
  `ladymconsole`）的 `--dict-dir` flag、`LADYM_DICT_DIR` env 或 toml `dict_dir`
  三选一（默认扫描本机 `~/.ladyM/dict`）。任一实例下载一次即全员生效（最迟约
  30 秒自动加载，无需重启）。三种多机方案见 docs/deployment.md。
- **离线构建**：`go build -tags fulldict` 将词典内嵌进二进制（约 +31MB），无需任何
  下载即有词级分词；可与 `enterprise` 组合。

文件落在 `~/.ladyM/dict`（sha256 校验固定为 gse v1.0.2 版本；镜像优先
jsDelivr，回退 GitHub raw；`jp` 变体约 22.6MB 且仅 raw 可用）。

#### 下游消费与 release 资产

库消费者（go.mod）获得词典级中文分词有三条路，**模块版本永远不变**：

1. **运行时下载（构建完全不用变）**：由用户在管理台（Settings → Memory）或
   `POST /api/cjk_dict/download` 主动触发——**LadyM 自身绝不主动下载任何东西**。
   宿主应用若想启动即置备，也可显式调用 `storage.DownloadCJKDict()`。任何构建
   都可用，且无需重新编译即可升级词典。
2. **副作用导入（不改构建脚本）**：
   `import _ "github.com/ProjAnvil/LadyM/storage/fulldict"` 内嵌词典（约 +31MB）
   ——import 边决定词典数据是否链入，不导入的下游体积不受影响。
3. **构建 flag**：`-tags fulldict`——同样的内嵌词典，在构建命令里选择；
   预编译的 `ladym-personal-fulldict-*` release 资产用的就是它。

**刻意不提供** `v0.5.0-fulldict` 之类的模块版本：semver 会把该后缀解析为
v0.5.0 的*预发布版*，污染 `go get` 的版本解析。

**二进制用户**：每个 `v*` GitHub release 自动附带各平台 tarball——
`ladym-personal-…`（默认）、`ladym-personal-fulldict-…`（内嵌词典）、
`ladym-enterprise-…`——由 `.github/workflows/release.yml` 在 tag 推送时构建，
附 `SHA256SUMS`。本地等价命令：`make package-personal`、`make package-fulldict`、
`make package-enterprise`；克隆仓库后 `LADYM_BUILD_TAGS=fulldict scripts/install.sh`
可直接装变体。
- **Docker**：词典镜像一条命令构建——`docker compose -f docker-compose.dev.yml
  -f docker-compose.dev.dict.yml up -d --build`（dev）或企业版同名覆盖文件（主
  `Dockerfile` 内置 `dict` target；普通 `docker build .` 仍是无词典镜像）。可选
  变体、sha256 校验、气隙构建可把词典放进仓库 `dict/` 目录。给已发布镜像叠
  词典层用 `Dockerfile.dict --build-arg BASE=<镜像>`；或用
  `--build-arg BUILD_TAGS=enterprise,fulldict` 把词典**编进**二进制（每个
  +31MB）。

## 文档

- **[ARCHITECTURE.md](ARCHITECTURE.md)**——完整设计：六层记忆、认知操作、两层检索、ACT-R
  激活函数、存储布局，以及代码索引子系统。*仅英文。*
- **[scenarios/](scenarios/)**——可执行的端到端场景（S01 写入/召回、S03 代码索引、S07 L5 心智
  模型、S08 L6 前瞻意图、S09 注意力门控……），同时充当活的规格说明。
  [scenarios/README.md](scenarios/README.md) 为中文剧本说明。
- **[docs/](docs/)**——专题深入（代码索引分析、LangGraph 集成）。
- **Go 测试套件**（各包旁的 `*_test.go`）——可执行的规格说明。

## 状态与路线图

✅ 六层引擎、两层召回、ADD/UPDATE/DELETE/NOOP 合并、proceduralize、衰减、纯 Go tree-sitter
索引器（Python/JS/TS/Go/Rust/Java/C/C++ 有完整符号规格，Kotlin/C#/Ruby/PHP/Swift/Scala/
Bash/Lua/SQL/HTML/CSS 退化为行窗口切片）、MCP server、CLI、Skill、Go SDK、可插拔 provider +
TOML 配置、System 2 后台 worker、L5 心智模型 / L6 前瞻意图抽取、嵌入式管理台
（`ladym serve --http`，Vue 3 SPA，源码在 `console/`）、加密 secret store。

🚧 下一步：GraphRAG 风格的跨文件引用解析，以及多模态事件。

## 贡献

欢迎贡献。最快的参与方式：

1. 提交前跑 `go test ./...`（密封）和 `go vet ./...`。
2. 为任何新行为在 [scenarios/](scenarios/) 下加场景，或在改动包旁加 `*_test.go`——它们就是
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
