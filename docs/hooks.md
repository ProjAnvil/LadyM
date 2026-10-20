# Hook 静默捕获：安装指引（个人版）

## 一句话结论

装好 hook 后，harness（Claude Code / Codex）会在会话事件发生时自动调用 `ladym hook`，把事件写进本地记忆库。agent 全程无感知，不消耗一个 token。

## 安装

前置条件：`ladym` 可执行文件已在 PATH 上。

```bash
ladym hook install --claude   # 写入 ~/.claude/settings.json
ladym hook install --codex    # 写入 ~/.codex/hooks.json
ladym hook install --all      # 两个都装
```

安装是幂等的：重复执行不会产生重复条目。已有的用户 hook 原样保留，首次写入前会备份原文件为 `*.ladym-bak`。

安装后的动作：

- Claude Code：重启会话（新开一个 session）即生效。
- Codex：在会话里运行 `/hooks`，审查并 trust LadyM 的 hook 条目。

卸载：

```bash
ladym hook uninstall --claude
ladym hook uninstall --codex
ladym hook uninstall --all
```

卸载只删 LadyM 自己的条目，用户 hook 不受影响。

## db 定址：零配置默认与统一库

零配置（推荐起步）：每个项目目录一个 `ladym.db`。hook 进程继承 harness 的 cwd，所以在哪个项目里跑会话，事件就写进那个项目目录下的 `ladym.db`。项目之间天然隔离。

想用一个统一的库跨项目沉淀记忆：设置环境变量 `LADYM_DB` 指向同一个 db 文件，或在 `ladym.toml` 里配置。改库不需要重装 hook——hook 每次启动时重新定址。

## workspace

每次事件到来，`ladym hook` 从 stdin JSON 的 `cwd` 取目录名作为 workspace（如 `/Users/x/proj/ladyM` → `ladyM`）。零配置模式下它就是项目名；统一库场景下它是隔离键——不同项目的记忆互不可见。需要强制指定时用 `-w` flag 覆盖推断值。

## 捕获的事件与去向

| 事件 | ActionClass | Resources | Observation |
|---|---|---|---|
| SessionStart | create | — | source（如 `resume`） |
| SessionEnd | — | — | — |
| UserPromptSubmit | — | — | prompt 原文（截断至 2000 runes） |
| PostToolUse | 由工具名映射（read/write/update/execute/search/query/analyze） | tool_input 中文件路径的 base name | tool_response（截断至 500 runes），outcome=success |
| PostToolUseFailure | 同上 | 同上 | 同上，outcome=fail |

Claude Code 捕获全部五类（PostToolUseFailure 独立成事件）；Codex 捕获前四类（无 PostToolUseFailure）。工具名映射不到受控词表时，raw action 字符串保留，留给 worker 后续回填。

所有事件走 record_event 管线入库：受控词表约束、文件名解析成 resource 实体并建 operates_on 边。

## 验证

```bash
ladym recall-ops --resource main.go   # 查某个文件的操作历史
ladym stats                           # 看库里记了多少
```

在项目目录里跑一会儿会话后执行，能看到记录即安装成功。

## plugin 安装途径（与 install 命令二选一）

除了 `ladym hook install`，也可以走 plugin market：

- Claude Code：会话里运行 `/plugin`，添加本仓库的 `plugins/` 目录作为 `ladym-plugins` marketplace，再安装 `ladym` plugin。本地校验可用 `claude plugin validate plugins/`。
- Codex：运行 `/plugins` 安装。

plugin 与 install 命令效果等价，装一个即可。区别：`ladym hook uninstall` 提供一键卸载便利，plugin 途径需要自行从 harness 配置里移除。

## 故障排查

| 症状 | 原因与处理 |
|---|---|
| install 时警告 `ladym is not on PATH` | hook 进程找不到 `ladym`。把可执行文件放进 PATH 后重试。 |
| Codex 装了但没记录 | hook 未 trust。在 Codex 里运行 `/hooks` 审查并信任。 |
| db 被锁 | WAL 模式 + busy_timeout 下罕见。等一下重试即可。 |
| 事件没记录 | `ladym hook` 失败时向 stderr 输出一行原因并 exit 1，harness 会把这段显示出来；按提示处理。 |

## 已知取舍

脱敏 V1 未做。捕获的 prompt 与工具输出可能包含密钥，`ladym.db` 是本地文件，注意不要把它提交进仓库或同步到不受信位置。后续版本计划增加 `[capture]` 排除规则与敏感字段打码。
