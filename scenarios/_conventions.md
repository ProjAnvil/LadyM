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
! sqlite3 <db> "DELETE FROM edges WHERE src_id IN (SELECT id FROM memories WHERE workspace='scn-sXX') OR dst_id IN (SELECT id FROM memories WHERE workspace='scn-sXX'); DELETE FROM memories WHERE workspace='scn-sXX'; DELETE FROM code_symbols WHERE memory_id NOT IN (SELECT id FROM memories);"
```

- 末句清孤儿 `code_symbols`:sqlite3 CLI 默认不开外键级联(`PRAGMA foreign_keys` 默认为 off),删 memories 后 `code_symbols` 行会残留,必须用 `memory_id NOT IN (SELECT id FROM memories)` 兜底删除(列名见 `storage/store.go` 的 schemaSQL)。
- **注意:`index_state` / `code_refs` 无 workspace 列**,按"相对索引根的路径"全局 keyed(`code/indexer.go` 用 `filepath.Rel(root, path)` 存 slash 路径),上面的 workspace 过滤对它们**完全无效**。S03/S12 类索引剧本的 teardown 必须按 fixture 的相对路径手动清 `index_state`,例如 S03(根 `tests/fixtures/sample_repo`):
  ```
  ! sqlite3 <db> "DELETE FROM index_state WHERE file_path IN ('auth/service.py', 'store/cache.py');"
  ```
  (同前缀可用 `file_path LIKE '<相对前缀>%'`。)**重跑剧本前必须清**,否则增量判定会把文件误判为 unchanged(直接 `files_skipped_unchanged` 全跳过),S03 步骤2 / S12 步骤3 的断言失真。`code_refs` 同理按符号名全局,如剧本断言涉及 refs 计数,teardown 一并按 `src_symbol`/`dst_symbol` 前缀清。

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
  - 注意:agent 路径(MCP/CLI)`remember` 在 **pass** 时返回 `id`/`hash`、在 **drop** 时返回 `{"id":null,"gated":"dropped","reason":…}`(CLI 打印 `dropped reason=…`,不打印假 id);均不含 `layer`/`type`——要确认 layer/type 须 `recall` 回查。MCP `stats(workspace=)` 仍忽略参数——详见 §8.2,据此选择断言路径。
- **[软]**:agent 判排名/相关性,**必须给一句理由**。
每剧本产出 check 清单(✅/❌ + 证据)。

## 7. 结果报告
全部剧本跑完,输出汇总表:

| Scenario | 结果 | 失败原因 |
|---|---|---|

结果 ∈ {通过, 失败, 跳过(条件不满足,如 L5/L6 未配 LLM 时的产出分支)}。

## 8. 校准发现:agent 写路径与 SPEC/SDK 的真实差异(2026-07-22 试跑 S01/S09/S14)

试跑发现 MCP/CLI agent 路径与 SPEC 声明 / SDK 行为存在三处关键差异,执行剧本时务必据此调整断言。

### 8.1 MCP/CLI `remember` 现路由经 `eng.remember`(已于 2026-07-22 修复)
- 历史发现(2026-07-22 试跑):MCP/CLI `remember` 曾直调 `eng.semantic.put_fact`、绕过 attention gate(与 SPEC C5 不符),too short / noise / recent-duplicate 内容均被持久化。
- **已修复**(CLI commit b1647e9、MCP commit f717d64):MCP/CLI `remember` 改调 `eng.remember`,gate 在 agent 写路径上**生效**;drop 行为现三路径(SDK/MCP/CLI)一致,由 `tests/unit/test_attention_gate.py` + `test_cli.py` + `test_mcp_server.py` 覆盖。
- `remember` 返回值契约(修复后):
  - **pass**:MCP `{"id","hash"}`、CLI `remembered id=<32hex> hash=<8hex>`(无 `gated` 键)。
  - **drop**:MCP `{"id":null,"hash":null,"gated":"dropped","reason":<noise|recent duplicate>}`、CLI 打印 `dropped reason=<reason> (gated; not persisted)`(不打印假 id——drop 返回的 Memory 未持久化,id 是生成的假 UUID)。
  - `layer`/`type` 仍不含——要确认须 `recall` 回查。
- workspace 一致性:MCP `remember` 现同步设 `eng.config.workspace` 与 `eng.semantic.workspace`;CLI 经 `Config.load` 烤入(本就一致)。
- 副作用:MCP `stats()` 现反映最近一次 `remember` 的 workspace(因 remember 改了 `eng.config.workspace`)——取某 ws 计数仍用 CLI `ladym stats -w`(§8.2)。S09 据此改回正向 drop 断言。
- LLM 开销:gate 修复后,配了 LLM provider 的部署里 MCP/CLI `remember` 现会触发一次 attention_gate 的 LLM 调用(修复前绕过 gate 不调;SDK `eng.remember` 本就如此)。heuristic 模式(`llm_provider=none`)无此开销——S09 的 drop 矩阵只在 heuristic 下断言。

### 8.1a gate 两层结构(2026-07-22 重构)
- **heuristic 前置**(确定性,LLM 调用前):noise(纯噪声词表)、recent-duplicate(近期 L1 同 content hash)→ drop。
- **语义层**:配了 LLM 交 LLM 判(pass/rewrite/drop);否则 pass。
- `too short` 规则已移除——"hi" 这类短内容不再被确定性 drop(none 模式 pass;LLM 模式交 prompt 判)。
- **对剧本的影响**:noise/dup 断言两种配置都成立(不再需要 `export LADYM_LLM_PROVIDER=none`);"hi" 类断言改为配置相关软断言。详见 S09。

### 8.2 MCP `stats(workspace=)` 仍不 honor workspace 参数
- MCP `stats` 工具内部调 `eng.stats()`,用 `eng.config.workspace`;其 `workspace=` 参数**仍被忽略**。注意:自 §8.1 修复后,`remember` 会改动 `eng.config.workspace`,故 `stats()` 返回的是"最近一次 remember 的 ws"而非固定的 server 默认——值不确定。
- 取**某 ws 的计数**一律用 CLI:`! ladym stats -w <ws> --db <db>`(会过滤),或 sqlite:
  `! sqlite3 <db> "SELECT layer, count(*) FROM memories WHERE workspace='<ws>' GROUP BY layer;"`
- 凡剧本写"`stats(workspace=…)` 计数 = N"的,一律改用上述 CLI/sqlite 路径断言(MCP stats 只用于第 0 步取 `db_path`)。

### 8.3 `record_event` 内容格式与可用性
- L1 episodic 的 content 字符串固定为管道拼接:`agent=<a> | action=<b> | observation=<c> | outcome=<d>`(recent-duplicate 判定按此串的 hash 比对)。
- 运行中的 MCP server 可能未暴露 `record_event`(旧版部署);此时用 CLI `! ladym record --agent … --action … --observation … --outcome … -w <ws> --db <db>`,产出相同的 L1 content 格式。CLI `record` 输出含 `layer`/`type`(比 MCP `remember` 信息更全)。
