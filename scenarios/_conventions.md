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
- **[软]**:agent 判排名/相关性,**必须给一句理由**。
每剧本产出 check 清单(✅/❌ + 证据)。

## 7. 结果报告
全部剧本跑完,输出汇总表:

| Scenario | 结果 | 失败原因 |
|---|---|---|

结果 ∈ {通过, 失败, 跳过(条件不满足,如 L5/L6 未配 LLM 时的产出分支)}。