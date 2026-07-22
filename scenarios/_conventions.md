# Scenario 共享约定

所有 SXX 剧本遵守本文件,不在剧本内重复。

## 1. db 对齐(C9)
MCP server 连的 db 是启动时固定的;CLI 默认走另一个库(`./ladym.db`)。故每剧本**第 0 步**固定:
`mcp__ladym__stats()` → 从返回 JSON 取 `db_path`,记为 `<db>`。
之后**所有** CLI 调用必须带 `--db <db>`,保证 MCP 与 CLI 同库同 workspace。

## 2. workspace 命名
每剧本专属 workspace `scn-sXX`(如 `scn-s01`)。MCP 调用带 `workspace="scn-sXX"`;
CLI 调用带 `-w scn-sXX`。

## 3. reset / Teardown
统一用 sqlite3 SQL(适用所有记忆类型,含 code_symbol/index 产物)。把 `<db>` 与 `scn-sXX` 替换为实际值:

```
! sqlite3 <db> "DELETE FROM edges WHERE src_id IN (SELECT id FROM memories WHERE workspace='scn-sXX') OR dst_id IN (SELECT id FROM memories WHERE workspace='scn-sXX'); DELETE FROM memories WHERE workspace='scn-sXX';"
```

执行两次或用 `changes()` 确认清空。reset 在每剧本开头(Given)与结尾(Teardown)各做一次,保证可重复。

## 4. worker 不对称(C6)
加工类只有 CLI `ladym worker --once`(含 consolidate + proceduralize + L5 + L6 + decay)。
MCP 无 worker 工具,只能 stats/recall 观察。触发:
`! ladym worker --once -w scn-sXX --db <db>`
`consolidate` 双路径都有(MCP 工具 + CLI 命令)。

## 5. L5/L6 条件分支(C1/C2)
L5/L6 在 `llm is None` 时 `skipped`,不产出。执行前确认 provider:
查看 env `LADYM_LLM_PROVIDER`(`! echo $LADYM_LLM_PROVIDER`)。
- 空/none → 验证 **skip 契约**(worker 不崩、无 L5/L6 记忆)。
- 非 none(配了 LLM)→ 验证 **产出**分支。

## 6. 判定
- **[硬]**:用工具返回值直接核对(stats 计数、id 存在/不存在、layer/type/tags、metadata.gated、tier_reached、worker 退出码)。
  - 注意:agent 路径(MCP/CLI)`remember` 仅返回 `id`/`hash`,**不含 layer/type/gated**;且 MCP `stats(workspace=)` 返回全局——详见 §8,据此选择断言路径。
- **[软]**:agent 判排名/相关性,**必须给一句理由**。
每剧本产出 check 清单(✅/❌ + 证据)。

## 7. 结果报告
全部剧本跑完,输出汇总表:

| Scenario | 结果 | 失败原因 |
|---|---|---|

结果 ∈ {通过, 失败, 跳过(条件不满足,如 L5/L6 未配 LLM 时的产出分支)}。

## 8. 校准发现:agent 写路径与 SPEC/SDK 的真实差异(2026-07-22 试跑 S01/S09/S14)

试跑发现 MCP/CLI agent 路径与 SPEC 声明 / SDK 行为存在三处关键差异,执行剧本时务必据此调整断言。

### 8.1 MCP/CLI `remember` 绕过 attention gate(与 SPEC C5 不符)
- SPEC C5 称 "`remember` 走 gate";但 **MCP `remember` 工具(`src/ladym/mcp/server.py`)与 CLI `remember` 命令(`src/ladym/cli.py`)都直接调 `eng.semantic.put_fact(...)`,不经 `eng.remember`**,故 attention gate(`src/ladym/operations/attention.py`)**在 agent 写路径上不生效**。
- 后果:too short / noise / recent-duplicate 的内容经 MCP/CLI `remember` **都会被持久化**(不被 drop)。
- gate 的 drop 行为**仅在 SDK `eng.remember()` 上生效**,由 `tests/unit/test_attention_gate.py` 覆盖(`engine.remember("hi")` → `metadata={"gated":"dropped","reason":"too short"}`)。
- `remember` 返回值:MCP 为 `{"id","hash"}`、CLI 为 `remembered id=<32hex> hash=<8hex>`,**均不含 `layer`/`type`/`gated`**——要确认 layer/type 须 `recall` 回查;`metadata.gated` 只有 SDK `eng.remember` 才返回。
- 此为**待修代码缺口**(MCP/CLI remember 应路由经 `eng.remember` 才符合 SPEC),S09 据此改写为"agent 路径绕过 gate"。

### 8.2 MCP `stats(workspace=)` 返回全局计数(忽略 workspace)
- MCP `stats` 工具内部调 `eng.stats()` **不传 workspace**,返回**全库**计数,`workspace` 参数被忽略。
- 取**某 ws 的计数**用 CLI:`! ladym stats -w <ws> --db <db>`(会过滤),或 sqlite:
  `! sqlite3 <db> "SELECT layer, count(*) FROM memories WHERE workspace='<ws>' GROUP BY layer;"`
- 凡剧本写"`stats(workspace=…)` 计数 = N"的,一律改用上述 CLI/sqlite 路径断言(MCP stats 只用于第 0 步取 `db_path`)。

### 8.3 `record_event` 内容格式与可用性
- L1 episodic 的 content 字符串固定为管道拼接:`agent=<a> | action=<b> | observation=<c> | outcome=<d>`(recent-duplicate 判定按此串的 hash 比对)。
- 运行中的 MCP server 可能未暴露 `record_event`(旧版部署);此时用 CLI `! ladym record --agent … --action … --observation … --outcome … -w <ws> --db <db>`,产出相同的 L1 content 格式。CLI `record` 输出含 `layer`/`type`(比 MCP `remember` 信息更全)。
