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

零配置（推荐起步）：当前目录下的 `.ladyM/ladym.db`。hook 进程继承 harness 的 cwd；canonical 身份只改变 workspace/资源身份，不改变数据库定址。

想用一个统一的库跨项目沉淀记忆：设置环境变量 `LADYM_DB` 指向同一个 db 文件，或在 `ladym.toml` 里配置。改库不需要重装 hook——hook 每次启动时重新定址。

## workspace

默认 `legacy`：从 stdin JSON 的 `cwd` 取目录名作为 workspace（如 `/Users/x/proj/ladyM` → `ladyM`），文件资源仍取 basename。同名项目/文件可能碰撞，子目录可能产生不同 workspace。未提供 cwd 时使用 hook 进程的实际 cwd。显式 `-w` 始终优先于推断值和配置中的 workspace。

可显式选择 canonical（环境变量优先于 TOML）：

```toml
[capture]
identity_mode = "canonical" # 默认 "legacy"
```

或设置 `LADYM_CAPTURE_IDENTITY_MODE=canonical`。只接受 `legacy` / `canonical`，非法值返回 ConfigError；不会静默降级。

canonical 用清理后的绝对 git 项目根（非 git 用绝对 cwd），workspace 为 `project:` 加根路径 SHA-256 的完整十六进制值。git 查找有超时、使用参数数组而非 shell；不解析 symlink。查找会移除继承的 Git 仓库、对象/索引和 `GIT_CONFIG*` 环境覆盖，避免 harness 的 `GIT_DIR` / `GIT_WORK_TREE` 等把身份重定向到别处；仍通过正常 PATH 查找 git。同一 git 仓库的子目录共享 workspace；不同根目录、移动仓库或不同 symlink 拼写会产生不同身份。

文件资源为根相对路径（如 `engine/main.go`、`cli/main.go`），根外文件保留清理后的绝对路径。路径区分大小写，资源 ID 为 `path:` 加 `SHA256(workspace + NUL + path)`；元数据标记 `resource_identity=path`。它们是无向量、无别名的结构实体，不参加普通资源的 exact alias / fuzzy 匹配或 worker 的 LLM alias merge。读取路径只精确定位，保留文件名开头/结尾的空格（`main.go` 与 `main.go ` 不同）；仅空白的查询仍拒绝，普通 legacy 资源查询仍去除外围空白。未知路径可显式 fallback 到普通 recall，但不会 fuzzy 定位为另一条路径。

### 旧库迁移：先备份，显式选择，不自动合并

切换前停止使用该库的进程，备份整个数据库（WAL 模式应使用 SQLite backup 或在关闭连接后连同 sidecar 一起备份）。然后由用户显式修改配置/环境变量；不需要重新安装 hook。

不会移动旧记忆、自动合并旧 workspace/资源、读取 transcript 或改写 harness 用户配置。legacy basename 已丢失目录信息，两种模式不能无损推断映射；旧记忆保留在旧 workspace，查询时继续显式 `-w`。统一库存在同名项目时建议 canonical；回退到 legacy 也不会撤销已写入的 canonical 数据。

## 捕获的事件与去向

| 事件 | ActionClass | Resources | Observation |
|---|---|---|---|
| SessionStart | create | — | source（如 `resume`） |
| SessionEnd | — | — | — |
| UserPromptSubmit | — | — | prompt 原文（截断至 2000 runes） |
| Stop（harness 提供时） | — | — | 兼容旧 outcome=success，但不是任务验证 |
| PostToolUse | 由工具名映射（read/write/update/execute/search/query/analyze） | legacy: base name；canonical: 根相对/根外绝对路径 | tool_response（截断至 500 runes），outcome=success |
| PostToolUseFailure | 同上 | 同上 | 同上，outcome=fail |

Claude Code 捕获全部五类（PostToolUseFailure 独立成事件）；Codex 捕获前四类（无 PostToolUseFailure）。工具名映射不到受控词表时，raw action 字符串保留，留给 worker 后续回填。

所有事件走 record_event 管线入库：受控词表约束、文件名解析成 resource 实体并建 operates_on 边。

所有普通 hook 事件（包括 Stop）均标 `knowledge_status=observed`。PostToolUse / PostToolUseFailure 额外记录 `tool_outcome=success/fail`，兼容原 `outcome`。工具成功或回合结束不代表任务成功/已验证；tool_response 即使声称 verified 也只作为观察文本保存，不会生成 `verified_at` 或 `task_outcome`。现有程序化提取不会把仅工具成功的 hook 当作成功轨迹；只有可信 Go SDK 调用者显式提交 `knowledge_status=verified` 和 `task_outcome=success` 才满足该验证条件。

MCP recall/search_code/recall_ops 的 memory 保留原字段，并增加 workspace、created_at、updated_at 和白名单 metadata：`session_id`, `source_episode`, `source_episodes`, `knowledge_status`, `evidence_missing`, `verified_at`, `action_class`, `tool_outcome`, `task_outcome`。列表/布尔/时间值不变形。未知元数据、cwd、transcript_path、原始 tool input、密码等不回显；没有新增通用 metadata 输入接口。这不是 content/observation 的脱敏机制。

## 验证

```bash
ladym recall-ops --resource main.go   # 查某个文件的操作历史
ladym stats                           # 看库里记了多少
```

canonical 模式查询需显式指定 canonical workspace，例如 `ladym recall-ops --resource engine/main.go -w 'project:<完整根路径散列>'`；原 basename 查询仍用于 legacy workspace。

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
