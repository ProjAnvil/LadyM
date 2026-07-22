# LadyM Agent E2E Scenarios Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在项目根 `scenarios/` 建 18 个 Markdown 剧本(README + _conventions + _template + S01–S15),供 agent 端到端验证 ladyM L1–L6 在 MCP+CLI 双路径下的行为契约。

**Architecture:** 每个剧本自包含 Given/When/Then + 分级(硬/软)断言;共享约定(db 对齐、worker 不对称、reset、命名、判定、报告)抽到 `_conventions.md`;`_template.md` 给骨架;`README.md` 索引。剧本由 agent 执行(非 pytest),通过 MCP 工具与 `! ladym` CLI 两条路径驱动。

**Tech Stack:** Markdown · ladyM MCP 工具(9 个)· ladym CLI(typer,11 子命令)· sqlite3(系统自带,用于 reset)。

## Global Constraints

- 文件夹位置:项目根 `scenarios/`(与 `tests/` 平级)。
- workspace 命名:每剧本专属 `scn-sXX`(S01→`scn-s01`,依此类推)。
- **db 对齐(C9)**:每剧本第 0 步 `mcp__ladym__stats()` 取返回 JSON 的 `db_path` 记为 `<db>`;之后所有 CLI 调用必须带 `--db <db>`(MCP server 的 db 启动时固定,CLI 默认指向另一个库)。
- **reset 机制**:统一用 sqlite3 SQL(适用所有记忆类型,含 code_symbol):先删涉及该 ws 的 edges,再删 memories。见 `_conventions.md`。
- **worker 不对称(C6)**:proceduralize/L5/L6/decay 仅 CLI `ladym worker --once`;MCP 无 worker 工具,只能 stats/recall 观察。
- 默认离线 `provider=none`;L5/L6(需 LLM)用条件分支:provider=none 验证 skip 契约,配了 LLM 验证产出。
- 语言:剧本正文中文(用户偏好);commit message 英文 conventional commits(项目惯例)。
- 复用 `tests/fixtures/sample_repo` 作为 S03/S12 索引素材,不复制。
- 本 plan 产出的所有 .md 内容均以代码块给出,执行者直接用 Write 写入对应路径。

---

## File Structure

| 文件 | 职责 |
|------|------|
| `scenarios/README.md` | 入口:总览、执行流程、剧本索引表、判定与报告 |
| `scenarios/_conventions.md` | 共享约定:db 对齐、workspace 命名、reset、worker 不对称、L5/L6 分支、判定、报告格式 |
| `scenarios/_template.md` | 剧本骨架(填写用) |
| `scenarios/S01..S15.md` | 15 个端到端剧本 |

分组:Task 1=支撑文件;Task 2=S01–S03(基础读写);Task 3=S04–S06(认知加工);Task 4=S07–S08(L5/L6);Task 5=S09–S10(gate+forget);Task 6=S11–S13(隔离/增量/空召回);Task 7=S14–S15(一致性+decay);Task 8=试跑校准。

---

### Task 1: 支撑文件(README / _conventions / _template)

**Files:**
- Create: `scenarios/README.md`
- Create: `scenarios/_conventions.md`
- Create: `scenarios/_template.md`

**Produces:** 三个所有剧本引用的基线文件(命名约定、db 对齐、reset、判定规则)。

- [ ] **Step 1: 创建 `scenarios/_conventions.md`**

用 Write 写入以下完整内容:

```markdown
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
```

- [ ] **Step 2: 创建 `scenarios/_template.md`**

```markdown
# SXX — <标题>

| 覆盖层 | L? | 路径 | MCP / CLI / worker | 需LLM | 否 / 条件分支 |

## Given
- workspace: `scn-sXX`
- 第 0 步:`mcp__ladym__stats()` 取 `db_path` 记为 `<db>`
- reset:见 _conventions §3

## When
1. ...

## Then
- [硬] ...
- [软] ...(附理由)

## Teardown
reset scn-sXX(见 _conventions §3)。
```

- [ ] **Step 3: 创建 `scenarios/README.md`**

```markdown
# LadyM Agent E2E Scenarios

面向 agent 执行的端到端剧本,验证 ladyM 在 **MCP + CLI** 两条路径下的 **L1–L6** 行为契约。

## 这是什么
15 个 Markdown 剧本(S01–S15),每个自包含(Given/When/Then + 分级断言)。由 agent(如 Claude Code)逐本执行,据断言判定 pass/fail,最后汇总。与 `tests/`(pytest SDK 测试)互补:这里测的是 agent 真实使用的 CLI/MCP 路径。

## 如何执行一个剧本
1. 读它的 Given/When/Then。
2. 第 0 步 `mcp__ladym__stats()` 取 `<db>`;之后所有 CLI 调用带 `--db <db>`(见 _conventions §1)。
3. 逐步执行 When,每步记录返回值(id 等)。
4. 对每个 Then 断言判定 ✅/❌(硬断言用工具结果核对;软断言给理由)。
5. Teardown:reset workspace(见 _conventions §3)。
6. 全部跑完输出汇总表(见 _conventions §7)。

## 剧本索引
| # | 标题 | 覆盖 | 路径 | 需LLM |
|---|------|------|------|------|
| S01 | 写入→召回闭环(fact) | L2 | MCP+CLI | 否 |
| S02 | episodic 记录与召回 | L1 | MCP+CLI | 否 |
| S03 | 代码索引与符号召回 | L2(code) | MCP+CLI | 否 |
| S04 | consolidate L1→L2 | L1→L2 | MCP+CLI | 否 |
| S05 | proceduralize→playbook | L3 | CLI worker | 否 |
| S06 | link + tier2 扩展 | L4 | MCP+CLI | 否 |
| S07 | L5 mental model(条件) | L5 | CLI worker | 条件 |
| S08 | L6 forward-intent(条件) | L6 | CLI worker | 条件 |
| S09 | attention gate:drop+pass | gate | MCP+CLI | 否 |
| S10 | forget 删除 | 维护 | MCP+CLI | 否 |
| S11 | workspace 隔离 | 隔离 | MCP+CLI | 否 |
| S12 | 增量索引 | L2(code) | MCP+CLI | 否 |
| S13 | 空召回/不崩溃 | 健壮性 | MCP+CLI | 否 |
| S14 | MCP↔CLI 一致性 | 一致性 | 跨路径 | 否 |
| S15 | decay 不伤 L2/L3 | 衰减 | CLI worker | 否 |

## 约定与骨架
- 共享约定(db 对齐 / worker 不对称 / reset / 命名 / 判定 / 报告):见 `_conventions.md`。
- 剧本骨架:见 `_template.md`。

## 相关
- 设计:`docs/superpowers/specs/2026-07-22-agent-e2e-scenarios-design.md`
- SDK 层 e2e 测试:`tests/integration/test_end_to_end.py`
```

- [ ] **Step 4: 校验 CLI 子命令与剧本引用一致**

Run: `uv run ladym --help`
Expected: 输出含 `remember record recall index consolidate stats forget link serve worker config`(与 README/剧本引用一致)。

- [ ] **Step 5: Commit**

```bash
git add scenarios/README.md scenarios/_conventions.md scenarios/_template.md
git commit -m "docs(scenarios): add README, conventions, and template scaffolding" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: S01–S03 基础读写闭环

**Files:**
- Create: `scenarios/S01-write-recall-fact.md`
- Create: `scenarios/S02-episodic-record.md`
- Create: `scenarios/S03-code-index.md`

- [ ] **Step 1: 创建 `scenarios/S01-write-recall-fact.md`**

```markdown
# S01 — 写入→召回闭环(fact)

| 覆盖层 | L2 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s01`;第 0 步 `mcp__ladym__stats()` 取 `<db>`;reset(_conventions §3)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s01 认证模块使用 JWT,有效期 24 小时", tags=["auth"], source="s01", workspace="scn-s01")` → 记返回 `id_a`
2. [CLI] `! ladym remember "scn-s01 密码用 bcrypt 加盐哈希存储" -w scn-s01 --db <db> --tags auth` → 从输出 `id=...` 取 `id_b`
3. [MCP] `mcp__ladym__recall(query="scn-s01 认证 JWT 过期", workspace="scn-s01")` → 看结果
4. [CLI] `! ladym recall "scn-s01 密码哈希" -w scn-s01 --db <db>` → 看结果
5. [MCP] `mcp__ladym__stats(workspace="scn-s01")` → 记 L2 计数

## Then
- [硬] 步骤1/2 返回 memory 的 `layer=L2_semantic`、`type=fact`
- [硬] 步骤3 结果含 `id_a`;步骤4 结果含 `id_b`
- [硬] 步骤5 该 ws `L2_semantic` 计数 ≥ 2
- [软] 用异措辞 `mcp__ladym__recall(query="scn-s01 登录令牌时效", workspace="scn-s01")` 召回时,步骤1 记忆仍在前 3(给理由:JWT/令牌/时效 与认证令牌语义相近)

## Teardown
reset scn-s01。
```

- [ ] **Step 2: 创建 `scenarios/S02-episodic-record.md`**

```markdown
# S02 — episodic 记录与召回

| 覆盖层 | L1 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s02`;取 `<db>`;reset。注意:`record_event` 绕过 attention gate(C5),必定持久化。

## When
1. [MCP] `mcp__ladym__record_event(agent="claude", action="scn-s02 fixed login bug", observation="jwt expiry was wrong", outcome="success", workspace="scn-s02")` → 记 `id_a`、返回的 layer/type
2. [CLI] `! ladym record --agent claude --action "scn-s02 deployed v2" --observation "green build" --outcome success -w scn-s02 --db <db>` → 记 `id_b`
3. [MCP] `mcp__ladym__recall(query="scn-s02 login bug", workspace="scn-s02")`
4. [MCP] `mcp__ladym__stats(workspace="scn-s02")`

## Then
- [硬] 步骤1/2 返回 `layer=L1_episodic`、`type=event`
- [硬] 步骤4 `L1_episodic` 计数 ≥ 2
- [硬] 步骤3 能召回到含 `scn-s02` 的事件(绕 gate,已持久化)

## Teardown
reset scn-s02。
```

- [ ] **Step 3: 创建 `scenarios/S03-code-index.md`**

```markdown
# S03 — 代码索引与符号召回

| 覆盖层 | L2(code) | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s03`;取 `<db>`;reset。
- 索引素材:仓库内 `tests/fixtures/sample_repo`(含 `auth/service.py` 的 `verify_password`、`store/cache.py`)。

## When
1. [MCP] `mcp__ladym__index_code(root="tests/fixtures/sample_repo", workspace="scn-s03")` → 记 `symbols_written`
2. [CLI] `! ladym index tests/fixtures/sample_repo -w scn-s03 --db <db>` → 增量,应跳过已索引文件
3. [MCP] `mcp__ladym__search_code(query="verify password hash", workspace="scn-s03")` → 看结果
4. [CLI] `! ladym recall "verify_password" -w scn-s03 --db <db> --code`
5. [MCP] `mcp__ladym__stats(workspace="scn-s03")`

## Then
- [硬] 步骤1 `symbols_written > 0`
- [硬] 步骤3/4 结果含 `type=code_symbol`、`source` 含 `auth/service.py`、content 含 `verify_password`
- [硬] 步骤5 `code_symbols > 0`
- [软] 召回的符号 signature 含 `password` 参数(给理由:索引保留函数签名)

## Teardown
reset scn-s03(代码符号同为 memories 行,SQL reset 一并清除)。
```

- [ ] **Step 4: Commit**

```bash
git add scenarios/S01-write-recall-fact.md scenarios/S02-episodic-record.md scenarios/S03-code-index.md
git commit -m "docs(scenarios): add S01-S03 basic read/write playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: S04–S06 认知加工(consolidate / proceduralize / link)

**Files:**
- Create: `scenarios/S04-consolidate.md`
- Create: `scenarios/S05-proceduralize.md`
- Create: `scenarios/S06-link-tier2.md`

- [ ] **Step 1: 创建 `scenarios/S04-consolidate.md`**

```markdown
# S04 — consolidate L1→L2

| 覆盖层 | L1→L2 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s04`;取 `<db>`;reset。
- 先灌 ≥ `min_episodes_to_trigger`(默认 3)条同主题 L1 事件:

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s04 deploy to prod", observation="ran deploy.sh release N", outcome="success", workspace="scn-s04")`(N=1,2,3)
2. [MCP] `mcp__ladym__consolidate(workspace="scn-s04")` → 记报告 JSON(`promoted_to_semantic`、`actions`)
3. [MCP] `mcp__ladym__stats(workspace="scn-s04")`
4. [CLI] `! ladym consolidate -w scn-s04 --db <db>`(二次,应多为 NOOP)

## Then
- [硬] 步骤2 `promoted_to_semantic >= 1`;`actions` 含 `ADD/UPDATE/DELETE/NOOP` 键
- [硬] 步骤3 `L2_semantic` 出现 fact(consolidate 产物)
- [硬] 步骤4 第二次后 ADD 不再增长(幂等,NOOP 占主导)

## Teardown
reset scn-s04。
```

- [ ] **Step 2: 创建 `scenarios/S05-proceduralize.md`**

```markdown
# S05 — proceduralize → L3 playbook

| 覆盖层 | L3 | 路径 | CLI worker(MCP observe) | 需LLM | 否 |

## Given
- workspace `scn-s05`;取 `<db>`;reset。
- 先灌 ≥3 条 `outcome=success` 的同类 L1 事件(C10:相似度阈值 0.55,`min_cluster_size=3`):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s05 deploy to prod", observation="ran deploy.sh", outcome="success", workspace="scn-s05")`
2. [CLI] `! ladym worker --once -w scn-s05 --db <db>`(触发 consolidate + proceduralize + L5/L6 skip + decay)
3. [MCP] `mcp__ladym__stats(workspace="scn-s05")`
4. [MCP] `mcp__ladym__recall(query="scn-s05 deploy playbook", workspace="scn-s05")`

## Then
- [硬] 步骤2 worker 退出码 0(不报错)
- [硬] 步骤3 出现 `L3_procedural` 层 / `type=playbook`
- [硬] 步骤4 召回到 playbook,其 summary/content 含动作词 `deploy` + `(3 episodes)`

## Teardown
reset scn-s05。
```

- [ ] **Step 3: 创建 `scenarios/S06-link-tier2.md`**

```markdown
# S06 — link + tier2 扩展

| 覆盖层 | L4 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s06`;取 `<db>`;reset。
- 先建两条相关 fact:

## When
1. [MCP] `mcp__ladym__remember(content="scn-s06 锚点事实 xyzzy 含义模糊", workspace="scn-s06")` → `id_a`
2. [MCP] `mcp__ladym__remember(content="scn-s06 xyzzy 的邻居阐述其真实含义", workspace="scn-s06")` → `id_b`
3. [MCP] `mcp__ladym__link(src=id_a, dst=id_b, relation="elaborates")` → 记 edge id
4. [MCP] `mcp__ladym__stats(workspace="scn-s06")` → 记 edges 数
5. [MCP] `mcp__ladym__recall(query="scn-s06 xyzzy", workspace="scn-s06")` → 看 `tier_reached` 与结果

## Then
- [硬] 步骤3 返回 edge 含 `src=id_a`、`dst=id_b`、`relation=elaborates`
- [硬] 步骤4 `edges >= 1`
- [硬] 步骤5 结果含 `id_a`(锚点被召回);若启用 tier2,`tier_reached` 可为 2

## Teardown
reset scn-s06。
```

- [ ] **Step 4: Commit**

```bash
git add scenarios/S04-consolidate.md scenarios/S05-proceduralize.md scenarios/S06-link-tier2.md
git commit -m "docs(scenarios): add S04-S06 cognitive-cycle playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: S07–S08 L5/L6 条件分支

**Files:**
- Create: `scenarios/S07-l5-mental-model.md`
- Create: `scenarios/S08-l6-forward-intent.md`

- [ ] **Step 1: 创建 `scenarios/S07-l5-mental-model.md`**

```markdown
# S07 — L5 mental model(条件分支)

| 覆盖层 | L5 | 路径 | CLI worker(MCP observe) | 需LLM | 条件分支 |

## Given
- workspace `scn-s07`;取 `<db>`;reset。
- 灌 ≥ `min_episodes_to_run`(默认 3)条 L1 + ≥3 条相似 L2 fact(L5 抽取对象为 L2/L3,`l5_min_cluster_size=3`、`l5_cluster_similarity=0.65`):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s07 build feature", observation="...", outcome="success", workspace="scn-s07")`
2. [MCP] `mcp__ladym__remember(content="scn-s07 认证模块使用 JWT 24h", workspace="scn-s07")`;同样再写两条高度相似的认证 fact(共 3 条 L2,易聚类)
3. 前置确认:`! echo $LADYM_LLM_PROVIDER` 据此选分支
4. [CLI] `! ladym worker --once -w scn-s07 --db <db>`
5. [MCP] `mcp__ladym__stats(workspace="scn-s07")`
6. [MCP] `mcp__ladym__recall(query="scn-s07 mental model", workspace="scn-s07")`

## Then
- **分支 A(provider 为空/none)**:[硬] 步骤4 worker 退出码 0;步骤5 无 `L5_mental` 记忆(skip 契约)。
- **分支 B(配了 LLM)**:[硬] 步骤5 出现 `L5_mental`/`type=mental_model`;步骤6 召回 mental model;该 model 经 `abstracts` 边连到成员(steps 中可用 stats `edges` 增加佐证)。

## Teardown
reset scn-s07。
```

- [ ] **Step 2: 创建 `scenarios/S08-l6-forward-intent.md`**

```markdown
# S08 — L6 forward-intent(条件分支)

| 覆盖层 | L6 | 路径 | CLI worker(MCP observe) | 需LLM | 条件分支 |

## Given
- workspace `scn-s08`;取 `<db>`;reset。
- 灌若干 L1 事件(作为预测输入):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s08 edit auth", observation="...", outcome="success", workspace="scn-s08")`
2. 前置确认:`! echo $LADYM_LLM_PROVIDER` 据此选分支
3. [CLI] `! ladym worker --once -w scn-s08 --db <db>`
4. [MCP] `mcp__ladym__stats(workspace="scn-s08")`
5. [MCP] `mcp__ladym__recall(query="scn-s08 predicted intent", workspace="scn-s08")`

## Then
- **分支 A(provider 为空/none)**:[硬] 步骤3 worker 退出码 0;步骤4 无 `L6_predictive` 记忆(skip 契约)。
- **分支 B(配了 LLM)**:[硬] 步骤4 出现 `L6_predictive`/`type=forward_intent`;其 metadata `valid_to > now`(用 `! sqlite3 <db> "SELECT json_extract(metadata,'$.valid_to') FROM memories WHERE workspace='scn-s08' AND layer='L6_predictive'"` 验证)。

## Teardown
reset scn-s08。
```

- [ ] **Step 3: Commit**

```bash
git add scenarios/S07-l5-mental-model.md scenarios/S08-l6-forward-intent.md
git commit -m "docs(scenarios): add S07-S08 L5/L6 conditional playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: S09–S10 attention gate + forget

**Files:**
- Create: `scenarios/S09-attention-gate.md`
- Create: `scenarios/S10-forget.md`

- [ ] **Step 1: 创建 `scenarios/S09-attention-gate.md`**

```markdown
# S09 — attention gate:drop 矩阵 + pass 对照

| 覆盖层 | gate | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s09`;取 `<db>`;reset。
- gate 判定顺序:too short(`< min_chars=8`)→ noise(全 token 命中噪声词)→ recent duplicate(与 `dedup_window_s`=3600s 内某 L1 同 content hash)。见 `src/ladym/operations/attention.py:75-102`。`remember` 走 gate;`record_event` 绕过(C5)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s09 hi", workspace="scn-s09")` → 长度 9 但有效内容短;改用 `content="hi"`(< 8 字符)→ too short(应 drop)
2. [MCP] `mcp__ladym__remember(content="lol ok test asdf foo", workspace="scn-s09")` → 全噪声且 ≥8 字符 → noise(应 drop)
3. [MCP] `mcp__ladym__record_event(agent="x", action="scn-s09 dup", observation="obs1", outcome="ok", workspace="scn-s09")`;该事件 content 字符串为 `agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok`;再 `mcp__ladym__remember(content="agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok", workspace="scn-s09")` → 与近 1h 内 L1 同 content hash → recent duplicate(应 drop)
4. [MCP] `mcp__ladym__remember(content="scn-s09 这是一条正常的足够长的语义记忆用于 pass 对照", workspace="scn-s09")` → pass
5. [MCP] `mcp__ladym__stats(workspace="scn-s09")`
6. [MCP] `mcp__ladym__recall(query="scn-s09", workspace="scn-s09")`

## Then
- [硬] 步骤5 该 ws `L2_semantic` fact 计数 = 1(仅步骤4 的 pass 写入;步骤1-3 均 drop)
- [硬] 步骤6 召回结果只含步骤4 那条("正常...语义记忆"),不含步骤1-3 的内容
- [软] 若 MCP `remember` 返回值含 `gated` 标记则直接对 1-3 判 ✅;若不暴露,以 stats 计数 = 1 间接证明(给理由)

## Teardown
reset scn-s09。
```

- [ ] **Step 2: 创建 `scenarios/S10-forget.md`**

```markdown
# S10 — forget 删除

| 覆盖层 | 维护 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s10`;取 `<db>`;reset。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s10 待删除的记忆 XYZ", workspace="scn-s10")` → `id_a`
2. [MCP] `mcp__ladym__stats(workspace="scn-s10")` → 记 L2 计数 N
3. [MCP] `mcp__ladym__forget(memory_id=id_a)` → 看返回
4. [MCP] `mcp__ladym__recall(query="scn-s10 待删除", workspace="scn-s10")`
5. [MCP] `mcp__ladym__stats(workspace="scn-s10")`

## Then
- [硬] 步骤3 forget 返回 `{"forgotten": id_a}`
- [硬] 步骤4 召回结果不含 `id_a`
- [硬] 步骤5 L2 计数 = N-1

## Teardown
reset scn-s10(已 forget,基本干净)。
```

- [ ] **Step 3: Commit**

```bash
git add scenarios/S09-attention-gate.md scenarios/S10-forget.md
git commit -m "docs(scenarios): add S09-S10 gate-and-forget playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: S11–S13 隔离 / 增量索引 / 空召回

**Files:**
- Create: `scenarios/S11-workspace-isolation.md`
- Create: `scenarios/S12-incremental-index.md`
- Create: `scenarios/S13-empty-recall.md`

- [ ] **Step 1: 创建 `scenarios/S11-workspace-isolation.md`**

```markdown
# S11 — workspace 隔离

| 覆盖层 | 隔离 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s11a`、`scn-s11b`;取 `<db>`;两者 reset。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s11a 团队 A 的部署密钥 abc", workspace="scn-s11a")` → `id_a`
2. [MCP] `mcp__ladym__recall(query="scn-s11 部署密钥", workspace="scn-s11a")` → 应命中
3. [MCP] `mcp__ladym__recall(query="scn-s11 部署密钥", workspace="scn-s11b")` → 应空
4. [CLI] `! ladym recall "scn-s11 部署密钥" -w scn-s11a --db <db>` → 命中
5. [CLI] `! ladym recall "scn-s11 部署密钥" -w scn-s11b --db <db>` → 空

## Then
- [硬] 步骤2/4 结果含 `id_a`(scn-s11a 可见)
- [硬] 步骤3/5 结果不含;步骤5 CLI 输出 `no memories matched`

## Teardown
reset scn-s11a、scn-s11b。
```

- [ ] **Step 2: 创建 `scenarios/S12-incremental-index.md`**

```markdown
# S12 — 增量索引

| 覆盖层 | L2(code) | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s12`;取 `<db>`;reset。
- 建 tmp 微型 repo(2 文件):
  `! mkdir -p /tmp/scn-s12-repo`
  `! printf 'def alpha():\n    return 1\n' > /tmp/scn-s12-repo/a.py`
  `! printf 'def beta():\n    return 2\n' > /tmp/scn-s12-repo/b.py`

## When
1. [CLI] `! ladym index /tmp/scn-s12-repo -w scn-s12 --db <db>` → 记 `files_indexed=2`
2. `! printf 'def gamma():\n    return 3\n' > /tmp/scn-s12-repo/c.py`(新增第 3 文件)
3. [CLI] `! ladym index /tmp/scn-s12-repo -w scn-s12 --db <db>` → 看 `files_indexed`、`files_skipped_unchanged`
4. [MCP] `mcp__ladym__search_code(query="gamma", workspace="scn-s12")`

## Then
- [硬] 步骤1 `files_indexed=2`
- [硬] 步骤3 `files_indexed=1`、`files_skipped_unchanged=2`
- [硬] 步骤4 命中 `gamma` 符号

## Teardown
reset scn-s12;`! rm -rf /tmp/scn-s12-repo`。
```

- [ ] **Step 3: 创建 `scenarios/S13-empty-recall.md`**

```markdown
# S13 — 空召回 / 不崩溃

| 覆盖层 | 健壮性 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s13`;取 `<db>`;reset(空)。

## When
1. [MCP] `mcp__ladym__recall(query="zzqqxx123 不存在的奇异查询 scn-s13", workspace="scn-s13")` → 看 `results`、`tier_reached`
2. [CLI] `! ladym recall "zzqqxx123 不存在 scn-s13" -w scn-s13 --db <db>` → 看输出

## Then
- [硬] 步骤1 `results` 为空、`tier_reached=1`、不抛异常
- [硬] 步骤2 CLI 输出 `no memories matched`、退出码 0

## Teardown
reset scn-s13(本就空)。
```

- [ ] **Step 4: Commit**

```bash
git add scenarios/S11-workspace-isolation.md scenarios/S12-incremental-index.md scenarios/S13-empty-recall.md
git commit -m "docs(scenarios): add S11-S13 isolation playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: S14–S15 一致性 + decay

**Files:**
- Create: `scenarios/S14-mcp-cli-consistency.md`
- Create: `scenarios/S15-decay.md`

- [ ] **Step 1: 创建 `scenarios/S14-mcp-cli-consistency.md`**

```markdown
# S14 — MCP↔CLI 一致性专项

| 覆盖层 | 一致性 | 路径 | 跨路径 | 需LLM | 否 |

## Given
- workspace `scn-s14`;取 `<db>`。本剧本核心:证明 MCP 与 CLI 同库同 ws 互见(“same engine” 契约)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s14 经 MCP 写入的共享事实", workspace="scn-s14")` → `id_a`
2. [CLI] `! ladym recall "scn-s14 经 MCP 写入" -w scn-s14 --db <db>` → CLI 能否召回到 `id_a`?
3. [CLI] `! ladym remember "scn-s14 经 CLI 写入的共享事实" -w scn-s14 --db <db>` → `id_b`
4. [MCP] `mcp__ladym__recall(query="scn-s14 经 CLI 写入", workspace="scn-s14")` → MCP 能否召回到 `id_b`?

## Then
- [硬] 步骤2 CLI 结果含 `id_a`(CLI 见到 MCP 写入)
- [硬] 步骤4 MCP 结果含 `id_b`(MCP 见到 CLI 写入)
- → 两条路径同库同 ws 互见,一致性契约成立

## Teardown
reset scn-s14。
```

- [ ] **Step 2: 创建 `scenarios/S15-decay.md`**

```markdown
# S15 — decay 不伤 L2/L3

| 覆盖层 | 衰减 | 路径 | CLI worker(MCP observe) | 需LLM | 否 |

## Given
- workspace `scn-s15`;取 `<db>`;reset。
- 先造 L2 fact + L3 playbook(借 S04/S05 手法):

## When
1. [MCP] `mcp__ladym__remember(content="scn-s15 持久语义事实", workspace="scn-s15")`(L2)
2. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s15 deploy", observation="ran deploy.sh", outcome="success", workspace="scn-s15")`
3. [CLI] `! ladym worker --once -w scn-s15 --db <db>`(产出 L3 playbook)
4. [MCP] `mcp__ladym__stats(workspace="scn-s15")` → 记 L2、L3 计数
5. 将该 ws 的 L1 改旧:`! sqlite3 <db> "UPDATE memories SET last_access_at = strftime('%s','now') - 100*365*86400 WHERE workspace='scn-s15' AND layer='L1_episodic'"`
6. [CLI] `! ladym worker --once -w scn-s15 --db <db>`(再跑,含 decay)
7. [MCP] `mcp__ladym__stats(workspace="scn-s15")`

## Then
- [硬] 步骤6 worker 退出码 0
- [硬] 步骤7 `L2_semantic`、`L3_procedural` 计数与步骤4 一致(decay 不剪 L2/L3)

## Teardown
reset scn-s15。
```

- [ ] **Step 3: Commit**

```bash
git add scenarios/S14-mcp-cli-consistency.md scenarios/S15-decay.md
git commit -m "docs(scenarios): add S14-S15 consistency-and-decay playbooks" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: 试跑校准(代表性子集)

**目的:** 剧本是“给 agent 执行”的产物,必须实跑一次才能校准步骤/断言是否与真实行为吻合。选 3 个最有代表性的剧本:**S01**(基础闭环)、**S09**(gate 边界,断言最易踩坑)、**S14**(MCP↔CLI 一致性,核心契约)。

**Files:** 无新增;可能修订 S01/S09/S14。

- [ ] **Step 1: 试跑 S01**

按 `scenarios/S01-write-recall-fact.md` 逐步执行(MCP + CLI)。记录每个 Then 的 ✅/❌ + 证据。

- [ ] **Step 2: 试跑 S09**

按 `scenarios/S09-attention-gate.md` 执行。重点核对:三个 drop 子情况是否真的未持久化(stats 计数=1);步骤3 的"完全相同 content 字符串"是否触发 recent duplicate。

- [ ] **Step 3: 试跑 S14**

按 `scenarios/S14-mcp-cli-consistency.md` 执行。重点核对:CLI 是否真能召回 MCP 写入的 `id_a`(验证 db 对齐生效)。

- [ ] **Step 4: 据试跑结果修订剧本**

把试跑中发现的参数/断言/步骤表述不准之处,修订回 S01/S09/S14(以及若发现共性约定问题,修订 `_conventions.md`)。典型校准点:
- MCP `remember` 返回值是否暴露 `gated` 标记(影响 S09 断言写法);
- CLI `remember` 输出格式(`id=... hash=...`)取 id 的正则;
- record_event 的 content 拼装格式是否真是 `agent=… | action=… | observation=… | outcome=…`。

- [ ] **Step 5: Commit 修订**

```bash
git add scenarios/
git commit -m "docs(scenarios): calibrate playbooks after dry-run of S01/S09/S14" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec 覆盖:** spec §8 的 S01–S15 各有对应 Task(2–7);spec §3 的 C1–C12 约束均有可执行验证:gate→C3/C4/C5(S09)、worker→C6(S05/S07/S08/S15)、db 对齐→C9(_conventions §1 + S14)、forget 按 id→C12(_conventions §3 reset)、L5/L6 LLM-gated→C1/C2/C11(S07/S08 条件分支)、proceduralize 阈值→C10(S05)。✅

**2. 占位扫描:** 无 TBD/TODO;每个 .md 均给出完整 content;命令含确切参数。S07/S08 的"配置 LLM 分支"为条件性内容(非占位),且给出 provider 探测命令。✅

**3. 类型/名称一致:** workspace slug 全程 `scn-sXX`;MCP 工具名(remember/record_event/recall/search_code/index_code/consolidate/stats/link/forget)与 `src/ladym/mcp/server.py` 一致;CLI 子命令与 `ladym --help` 一致;`<db>` 占位在每剧本第 0 步定义。✅

**4. 已知校准风险(交由 Task 8 处理):** (a) MCP `remember` 返回是否含 `gated` 标记未在 server.py 返回值中确认(只返回 `{id, hash}`)——S09 已写"若不暴露则用 stats 间接证明"的软断言兜底;(b) record_event content 拼装格式需 Task 8 实跑确认。这两点不阻塞 plan,但 Task 8 必跑。
