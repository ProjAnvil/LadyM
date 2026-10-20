# 规格：hook 静默捕获与 plugin market（个人版）

> 日期：2026-10-20。状态：待用户确认。
> 前置调研：`docs/research/2026-10-20-hook-carrier-selection.md`（载体选型：Go 子命令）、`docs/research/2026-10-20-ai-memory-vs-ladym.md`（差距分析：静默捕获是最大缺口）。

## 1. 目标

给 LadyM 个人版补一条**不依赖 agent 自觉**的 L1 写入路径：

- agent harness（Claude Code、Codex）通过 lifecycle hook 静默捕获会话事件，写入 LadyM 记忆库；
- 分发走两个方向：个人版 CLI（`ladym hook install` 安装器）和 plugin market（Claude Code marketplace plugin + Codex plugin）。

企业版不做 hook。企业版本质是 memory 数据库，捕获是客户端（个人版 CLI / plugin）的职责。

## 2. 已确认的设计决策

| 决策点 | 结论 |
|---|---|
| 触发载体 | Go：`ladym hook` 子命令（复用现有二进制，交叉编译全平台） |
| workspace 映射 | stdin 的 `cwd` 取目录名（如 `/x/ladyM` → `ladyM`） |
| V1 事件范围 | 核心五类：SessionStart、SessionEnd、UserPromptSubmit、PostToolUse、Stop（+ Claude Code 独有的 PostToolUseFailure） |
| 分发方向 | ① 个人版 CLI 安装器；② plugin market。两者都做 |
| 脱敏 | V1 不做（显式取舍，见 §9 风险） |
| 写入路径 | 直写本地 SQLite（WAL + busy_timeout 已支持跨进程）；remote 模式（--server）后续版本 |

## 3. 架构总览

```
Claude Code / Codex hook 触发
        │  stdin: 事件 JSON
        ▼
ladym hook            ── 新包 hook/：解析 + 事件映射（纯函数，无 IO）
        │
        ▼
engine.RecordOperation(OperationEvent)   ── 现有写路径：词表归一 + 资源实体解析 + operates_on 确定性建边
        │
        ▼
ladym.db (workspace = cwd 目录名)
```

新包 `hook/` 对齐 `code/`（indexer）的先例：纯解析与映射逻辑，可独立测试。CLI 侧加 `hookCmd()`，命令注册进 `cli.go` 的 root。

## 4. `ladym hook` 子命令

### 4.1 行为

- 从 stdin 读完整 JSON，按 `hook_event_name` 分派。
- 已知事件映射为 `OperationEvent`，走 `RecordOperation` 写入。
- 未知事件类型：记 debug 日志（stderr，一行），exit 0。事件字段漂移不炸。
- JSON 解析失败：stderr 一行原因，exit 1（非阻断，harness 忽略）。
- 写入成功：无输出，exit 0。stdout 永远保持干净（不输出 JSON 控制 harness——LadyM 的 hook 是纯观察者，不做 decision/block）。
- 内部写入失败：stderr 一行，exit 1。绝不 exit 2（阻断 harness 动作）。
- 处理时长目标：远低于 60 秒 harness 超时（直写 SQLite 为毫秒级）。

### 4.2 事件 → OperationEvent 映射表

通用字段提取：`session_id`、`transcript_path`、`cwd` → `Metadata`；`cwd` 目录名 → workspace。

| hook 事件 | Agent | Action（raw） | ActionClass | Resources | Observation | Outcome |
|---|---|---|---|---|---|---|
| SessionStart | harness 名 | `session_start` | `create` | — | 启动来源（startup/resume，若有） | — |
| SessionEnd | harness 名 | `session_end` | — | — | — | — |
| UserPromptSubmit | harness 名 | `prompt_submit` | — | — | prompt 内容（截断 2000 字符） | — |
| PostToolUse | harness 名 | `tool:<tool_name>` | 工具名映射（§4.3） | `tool_input` 提取（§4.4） | `tool_response` 摘要（截断 500 字符） | `success` |
| PostToolUseFailure（仅 Claude Code） | harness 名 | `tool:<tool_name>` | 工具名映射 | 同上 | 失败信息摘要 | `fail` |
| Stop | harness 名 | `turn_stop` | — | — | — | `success` |

说明：

- `ActionClass` 直接填受控词表的词（`OperationEvent.ActionClass` 覆盖归一化）。词表 miss 的工具留空，Action raw 里的 `tool:<name>` 交给 worker 的既有 backfill 机制。
- `SessionEnd`/`Stop` 的 ActionClass 留空：会话边界不是操作，不参与 recall_ops 的动作过滤。
- 截断值（2000/500）作为常量放 `hook/` 包内，不进配置（YAGNI）。

### 4.3 工具名 → ActionClass 映射表

受控词表基准：`operations/actions.go` 的 31 词五组。

| 工具名（两家并集） | ActionClass |
|---|---|
| Read、NotebookRead | `read` |
| Write | `write` |
| Edit、NotebookEdit | `update` |
| Bash、shell、Shell | `execute` |
| Grep、Glob、Search | `search` |
| WebFetch、WebSearch | `query` |
| TodoWrite、Task（写 TODO 时） | `update` |
| Task、Agent（派发子代理） | `analyze` |
| `mcp__*`（任意 MCP 工具） | 留空（raw 交给 backfill） |
| 其他未知 | 留空 |

Claude Code 与 Codex 的工具名差异（如 Codex 的 `shell` 对 Claude 的 `Bash`）在映射表内吸收，一份表服务两家。

### 4.4 Resources 提取

- `tool_input.file_path` 存在 → `Resources = [file_path 的文件名]`（去目录，资源实体走 LadyM 既有解析）。
- `tool_input.notebook_path` 同理。
- Bash/Shell：无文件路径，`Resources` 留空。命令文本进 `Action` 的 raw 侧不进 resources（命令不是资源）。
- 多值字段（如 Edit 的 `edits` 数组）V1 不提取（YAGNI，回填交给 transcript 回放，后续版本）。

### 4.5 命令面

```
ladym hook                     # 读 stdin，分派写入
  --harness <name>             # claude-code | codex：来源标识，进 Agent 字段（stdin 两家同构，无法自动判别）
  --db <path>                  # 同其他子命令
  -w, --workspace <name>       # 覆盖 cwd 目录名推断（少数场景）
```

`--harness` 由 install 时的 command 字符串携带（见 §5.1），手动测试可不传（Agent 字段留空）。

## 5. `ladym hook install` / `uninstall`

### 5.1 install

```
ladym hook install --claude    # 写 ~/.claude/settings.json
ladym hook install --codex     # 写 ~/.codex/hooks.json
ladym hook install --all       # 两者
```

Claude Code 写入内容（用户级 settings.json，合并进已有 `hooks` 数组，按事件名追加 hook 条目，重复安装幂等）：

```json
{
  "hooks": {
    "SessionStart":  [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }],
    "SessionEnd":    [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }],
    "PostToolUse":   [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }],
    "PostToolUseFailure": [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }],
    "Stop":          [{ "hooks": [{ "type": "command", "command": "ladym hook --harness claude-code" }] }]
  }
}
```

Codex 写入内容（`~/.codex/hooks.json`，同样幂等合并；事件集去掉 PostToolUseFailure）：

```json
{
  "hooks": {
    "SessionStart":  [{ "hooks": [{ "type": "command", "command": "ladym hook --harness codex", "timeout": 10 }] }],
    "SessionEnd":    [{ "hooks": [{ "type": "command", "command": "ladym hook --harness codex", "timeout": 10 }] }],
    "UserPromptSubmit": [{ "hooks": [{ "type": "command", "command": "ladym hook --harness codex", "timeout": 10 }] }],
    "PostToolUse":   [{ "hooks": [{ "type": "command", "command": "ladym hook --harness codex", "timeout": 10 }] }],
    "Stop":          [{ "hooks": [{ "type": "command", "command": "ladym hook --harness codex", "timeout": 10 }] }]
  }
}
```

要求：

- **合并不覆盖**：目标文件已存在时读入、合并、写回；用户已有 hook 条目原样保留。JSON 解析失败（文件损坏）时报错退出，不写。
- **幂等**：识别"自己写入的条目"（command 以 `ladym hook` 开头的条目，容忍 `--harness` 参数差异）已存在则跳过。
- **备份**：写回前把原文件复制为 `<file>.ladym-bak`（一次，不滚动覆盖）。
- command 字符串固定 `ladym hook`：无管道、无 `$()`、无路径拼接，bash / Git Bash / powershell 统一执行。
- install 完成后输出：Codex 侧提示「运行 /hooks 完成信任审查」（trust 绑定 hook 定义 hash）；Claude Code 侧提示重启会话生效。
- 二进制不在 PATH 时（`exec.LookPath("ladym")` 失败）install 照常写配置，但输出警告。

### 5.2 uninstall

- 按事件遍历，删除 command 为 `ladym hook` 的条目；某事件数组因此为空则删除该事件键。
- 不动用户自己的 hook 条目。

## 6. plugin market

### 6.1 Claude Code plugin

仓库内新增 `plugins/ladym/`（plugin 目录本体）：

```
plugins/ladym/
├── .claude-plugin/
│   └── plugin.json        # name: ladym, version, description
├── hooks/
│   └── hooks.json         # §5.1 Claude Code 同款事件集
└── .mcp.json              # 可选：指向 ladym serve（stdio MCP），plugin 启用即挂 LadyM 工具
```

marketplace：独立仓库（`<org>/ladym-plugins`）或本仓库 `plugins/` 根下放 `.claude-plugin/marketplace.json`。**实施时核对官方 create-marketplace 文档定案**（倾向独立仓库：marketplace 是 catalog，plugin 本体随 LadyM release 走或直接指向本仓库路径）。

安装前提：ladym 二进制已在 PATH（plugin 不托管二进制，command 仍为 `ladym hook`）。

### 6.2 Codex plugin

Codex plugin 通过 manifest 或默认 `hooks/hooks.json` 捆绑 lifecycle hooks，走 `/hooks` trust 流程。**Codex plugin manifest 的确切 schema 实施时核对 developers.openai.com/codex 的 plugin 文档**，内容与 §5.1 Codex 事件集一致。

### 6.3 V1 边界

- plugin 里不带 skills/agents 组件（ LadyM 的 Claude Code Skill 已另行存在）。
- `type: "http"` hook 直连 `ladym serve --http` 的路径**不进 V1**（Claude Code 独有，且要求服务常驻）；留作后续增强。

## 7. 测试策略（TDD，hermetic）

沿用项目纪律：hashing embedding、零网络、零 key。

1. `hook/` 包单测：
   - 每种事件的 stdin JSON fixture → 断言映射出的 `OperationEvent` 全字段（含截断、workspace 推断）。
   - 未知事件类型、未知字段、损坏 JSON 的宽容/报错分支。
   - 工具名映射表全条目 + miss 分支。
2. CLI 集成测试（沿用 `cmdtest_test.go` 模式）：
   - 管道喂 JSON → 断言 db 里 L1 记忆及 `operates_on` 边落库。
   - exit code 矩阵（0/1，永不 2）。
3. install/uninstall 测试：
   - 空 HOME 下的生成、已有用户 hook 的合并不覆盖、二次安装幂等、uninstall 只删自己的条目、损坏 JSON 拒写。
4. plugin 静态校验：`hooks.json` / `plugin.json` 的 JSON 语法与事件集断言（golden 文件比对）。

## 8. 里程碑

| 里程碑 | 内容 | 验收 |
|---|---|---|
| M0 | `hook/` 包：解析器 + 映射表 | 单测全绿（§7.1） |
| M1 | `ladym hook` 子命令接 engine | 集成测试：stdin→db 记忆+边落库（§7.2） |
| M2 | `ladym hook install/uninstall` | 配置生成/合并/幂等测试（§7.3） |
| M3 | plugin market：plugins/ladym + marketplace + Codex plugin | 静态校验 + 手工装通两家 harness |
| M4 | 文档：README 章节或 docs/hooks.md 安装指引 | 手工过一遍安装流程 |

## 9. 已接受的风险

1. **脱敏 V1 不做**（用户决策）。捕获的 prompt 与工具输出可能带密钥入库。缓解：文档明示风险；V2 引入 `[capture] ignore_paths` 与密钥形态打码。db 是本地文件，泄露面等于本机磁盘。
2. **事件字段漂移**：两家 harness 的事件 JSON 在演进。缓解：未知字段宽容、未知事件 exit 0。
3. **L1 写放大**：每次工具调用一条 PostToolUse。缓解：ACT-R 衰减 + worker consolidate 兜底；后续实测写入速率再决定是否削峰（spool）。
4. **Codex trust-review**：安装体验多一步。缓解：install 输出明确指引。
5. **plugin 机制演进**：两家的 plugin 规范都在快速迭代。缓解：plugin 内容极薄（一个 hooks.json + manifest），变动面小。

## 10. 待实施时核对

- Codex plugin manifest 的确切 schema（官方 build plugins 文档）。
- Claude Code marketplace.json 的字段规范（官方 create-marketplace 文档）。
- PostToolUseFailure 的 stdin 字段（是否带 error 信息）。
