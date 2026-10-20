# 调研：hook 触发载体选型（Go 二进制 vs Python）与两家 harness 适配面

> 日期：2026-10-20。资料来源：Claude Code hooks 官方文档与社区核实页、OpenAI Codex hooks 官方文档（developers.openai.com/codex/hooks）、aftergradient.com 的 Codex hooks 源码级指南（openai/codex 仓库，2026-10-02 核对到 rust-v0.160.0）。

## 0. 结论

**用 Go，做成 `ladym hook` 子命令。** 不用 Python，不用 sh。

理由一句话：LadyM 本体就是 Go 单二进制，hook 载体零新增部署成本；Python 在目标机器上解释器版本不可控，Windows 上更是常态缺失。

## 1. 两家 harness 的 hook 机制（核实结果）

### 1.1 Claude Code hooks

- **配置位置**：`~/.claude/settings.json`（用户级）、`.claude/settings.json`（项目级，可提交）、`.claude/settings.local.json`（本地）、plugin 内 `hooks/hooks.json`。
- **事件清单**：PreToolUse、PostToolUse、UserPromptSubmit、Notification、Stop、SubagentStop、PreCompact、SessionStart、SessionEnd（+较新的 PermissionRequest、PostToolUseFailure）。
- **执行方式**：`type: "command"` 的 hook 经 shell 执行，默认 bash，可选 powershell。Windows 上对 `.sh` 命令自动加 bash 前缀（依赖 Git Bash）。另有 exec form：`"command": "node", "args": [...]`。
- **stdin 输入**：JSON。通用字段：`session_id`、`cwd`、`hook_event_name`、`transcript_path`、`permission_mode`；工具类事件另有 `tool_name`、`tool_input`（PostToolUse 还有 `tool_response`）。
- **输出约定**：exit 0 = 成功（stdout 可为 JSON 控制输出，如 `hookSpecificOutput.permissionDecision`、`additionalContext`）；exit 2 = 阻断，stderr 给 Claude；其他 exit code = 非阻断错误。
- **超时**：默认 60 秒，可配。多个匹配 hook 并行执行。
- **占位符**：`${CLAUDE_PROJECT_DIR}`、`${CLAUDE_PLUGIN_ROOT}`、`${CLAUDE_PLUGIN_DATA}`。
- **另一种类型 `type: "http"`**：把事件 JSON POST 到 URL，2xx 响应体带 decision。这一点对 LadyM 有特殊价值，见 §3.4。

### 1.2 Codex hooks（2026 年起）

Codex 已内置 "Claude-style lifecycle hooks"，结构与 Claude Code 高度同构：

- **配置位置**：`~/.codex/hooks.json` 或 `~/.codex/config.toml` 内 `[hooks]` 表；项目级 `.codex/hooks.json`（仅在项目被信任时加载）。两层同用会警告，建议每层只选一种表示。
- **事件清单**：PreToolUse、PermissionRequest、PostToolUse、PreCompact、PostCompact、UserPromptSubmit、SubagentStop、Stop、SessionStart、SubagentStart、SessionEnd。主线程结束才有 SessionEnd，子 agent 不触发。
- **配置形状**：与 Claude Code 相同的三层结构（事件 → matcher 组 → handler 数组）。matcher 用正则（如 `"^Bash$"`）。handler 有 `type: "command"`、`command`、`timeout`、`statusMessage`。
- **stdin 输入**：同 Claude Code，JSON 走 stdin。
- **信任机制（Codex 独有）**：非托管 hook 必须先在 `/hooks` 里 review 并 trust，trust 记录绑定 hook 定义当前的 hash。hook 变了就重新标待审、跳过执行。这是 LadyM 适配时要注意的差异：安装器要引导用户跑一次 `/hooks`。
- **plugin 机制**：Codex plugin 可通过 manifest 或默认 `hooks/hooks.json` 捆绑 lifecycle hooks，走同样的 trust-review 流程。
- **另有一条独立的 `notify` 通道**：`~codex/config.toml` 顶层 `notify = ["prog", "arg"]`，事件 JSON 作为**第一个命令行参数**（不是 stdin）传入，目前只覆盖 agent-turn-complete。注意 `notify` 在项目级 config 中被忽略，只能配用户级。它可以作为 SessionEnd 的兜底信号源。

### 1.3 两家协议的公共子集

对 LadyM 适配最有利的事实：**两家的事件名、matcher 结构、command handler、stdin JSON、exit code 语义几乎一致**。一套 `ladym hook` 子命令可以同时服务两家，差异只在配置文件落点和 Codex 的 trust-review 步骤。

## 2. Go vs Python 载体对比

| 维度 | Go 二进制（`ladym hook`） | Python 脚本 |
|---|---|---|
| 部署成本 | 零。装 LadyM 即有，复用现有交叉编译产物 | 需要目标机有 python3；Windows 默认没有；版本碎片（Debian 3.9 / macOS 依赖 CLT） |
| Windows | exe 放 PATH 即可，bash/powershell 都能调起 | 命令名不统一（python/py/python3），路径引号问题多 |
| 启动延迟 | 毫秒级进程启动，直连 SQLite | 解释器启动 30-100ms+，高频事件（每次工具调用）累积拖慢 agent 循环 |
| 依赖边界 | 单二进制，标准库齐备 | 只用标准库则功能受限；连 embedding 或重逻辑就要 venv/pipx/uv，分发复杂度暴增 |
| 逻辑复用 | 直接调 engine 包：record_event 槽位归一化、确定性建边、受控词表全部现成 | 上述逻辑要跨语言复刻，还得对齐 golden vector |
| 写入路径 | 直接写 ladym.db（WAL + busy_timeout 已支持跨进程） | 要么直写（重依赖）要么走 HTTP/MCP 间接调用 |

补充一个参照：ai-memory 走的是 Rust 单二进制 + hooks 路线，写上限实测约 700 条/秒。二进制载体在高频 hook 下的性能是经过验证的模式。

**Python 只剩一个适用场景**：宿主机已确定有 Python 且不想引入二进制（比如纯脚本环境）。这个场景与 LadyM 的产品形态不重叠。

## 3. 架构草案（供下一步规格参考）

### 3.1 `ladym hook` 子命令

```
stdin JSON → 解析 hook_event_name → 事件映射器 → engine 写路径
```

- 读 stdin 全量 JSON，按 `hook_event_name` 分派。不依赖命令行参数区分事件，这样两家 harness 用同一条 command 字符串。
- exit 0 静默返回。LadyM 的 hook 是**观察者**，不做 decision/block（第一版不碰 permissionDecision，避免与用户既有 hook 打架）。
- 失败语义：内部错误 exit 1（非阻断），stderr 一行原因。绝不 exit 2。

### 3.2 事件 → 记忆层映射（初步）

| hook 事件 | LadyM 写入 |
|---|---|
| SessionStart / SessionEnd | L1 episode 边界（action=start/end），SessionEnd 可选触发 consolidate 提示 |
| UserPromptSubmit | L1 episode（action=prompt，content 截断存储） |
| PreToolUse / PostToolUse | L1 episode（action_class 按工具名映射到受控词表：Edit/Write→modify，Bash→execute，Read→read……） |
| Stop / SubagentStop | 补 outcome 槽 |
| transcript_path | 暂存到 metadata，作为后续回放/整理的源 |

PostToolUse 的 `tool_input` 里的文件路径可提取为 `resources` 槽，直接喂给 record_event 的既有确定性建边（`operates_on`）。

### 3.3 配置落点

- Claude Code：plugin 内 `hooks/hooks.json`（走 marketplace 分发）或用户级 settings.json（安装器写入）。
- Codex：plugin manifest 捆绑 `hooks/hooks.json`，或安装器写 `~/.codex/hooks.json`。安装完提示用户跑 `/hooks` 完成 trust。
- command 字符串统一写成 `ladym hook`：无管道、无 `$()`、无路径拼接，天然跨 bash/powershell。需要定位二进制时用 `${CLAUDE_PLUGIN_ROOT}` 类占位符。

### 3.4 可选的第三条接入路径：http hook

Claude Code 支持 `type: "http"` hook，直接 POST 事件 JSON。`ladym serve --http` 的数据面已经存在。服务常驻的用户可以零二进制路径配置接入。Codex 侧无对应机制，此路仅作 Claude Code 的补充，不作主路径。

### 3.5 高频写入的削峰

PostToolUse 每次工具调用都触发。两种策略：

1. 直写 SQLite（WAL 已支持，毫秒级，简单可靠）——第一版选这个。
2. 本地 spool 文件 + 批量落库（ai-memory 的断网 spooling 模式）——等实测出现瓶颈再上。

### 3.6 脱敏边界（设计时就要有，别事后补）

ai-memory 的教训：捕获在类型化隐私边界先脱敏再存储。LadyM hook 捕获 prompts 和工具输出，必须带：

- 路径排除/allowlist（配置 `[capture] ignore_paths` 等价物）。
- 环境变量与密钥形态的值打码后再落库。

## 4. 风险与待确认

1. **Codex trust-review**：安装体验多一步 `/hooks`，文档里必须写清楚。
2. **Windows 实测**：`ladym hook` 经 Git Bash 与 powershell 两种执行路径都要过一遍 CI/手工测试。Windows CI 目前没有，需要补。
3. **事件字段漂移**：两家的 stdin JSON 字段都在演进（PostToolUseFailure、PermissionRequest 都是后加的）。解析器按未知字段宽容处理，未知事件类型记一条 debug 日志直接 exit 0。
4. **60 秒超时**：单事件处理必须远低于此。直写 SQLite 无风险；将来若在 SessionEnd hook 里同步跑 consolidate 要改异步。

## 5. 下一步

按 superpowers 流程，把本结论落成规格：`ladym hook` 子命令 + 事件映射器 + 两家 harness 的 plugin/配置生成器。规格确认后再出实施计划。
