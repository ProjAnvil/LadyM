# LadyM Agent E2E Scenarios — 设计

- 日期:2026-07-22
- 状态:设计已批准,待写实现计划
- 作者:yuhaochen + Claude
- 相关:`README.md` · `ARCHITECTURE.md` · `tests/integration/test_end_to_end.py`(SDK 层 e2e)

## 1. 背景与目标

LadyM 已有 217 个 pytest 测试(`tests/`),但它们全部走 **Python SDK**(`Engine` 直调)。
真实使用中,agent 是通过 **MCP 工具**(`mcp__ladym__*`)或 **CLI**(`ladym …`)两条独立代码路径访问
记忆的——README 声称“all calling the same engine so behaviour is identical everywhere”,但这条
“等价性”断言从未在 agent 视角下被端到端验证。

本设计新增一套 **面向 agent 执行的 e2e scenario 剧本**,放在 `scenarios/` 文件夹,由 agent(如
Claude Code)逐本执行,覆盖 **L1–L6 全层 + 写入/召回/认知加工/边界情况**,以检测:

- agent 在 CLI/MCP 两条路径下的真实行为是否符合契约;
- L1–L6 各层的写入、召回、跨层加工是否正确;
- attention gate、forget、workspace 隔离、增量索引等边界行为。

## 2. 范围与非目标(YAGNI)

**范围内**:

- 15 个 Markdown 剧本(S01–S15),agent 可读可执行。
- 统一的 Given/When/Then + 分级断言格式。
- workspace 级隔离 + 可重复(reset/teardown)。
- MCP + CLI 双路径并重 + 一致性交叉。

**非目标**:

- 不做声明式 runner / 不进 CI pytest(那是 `tests/` 的职责);若将来需要自动化,可从剧本平滑加 runner 演进,但不在本次范围。
- 不造 fake LLM 基建;L5/L6 用条件分支处理(见 §4.3)。
- 不替代 `tests/unit/test_l5_extraction.py`、`test_l6_prediction.py` 等产出正确性单测;剧本只覆盖 agent 可达的 e2e 路径与契约。

## 3. 关键技术约束(探索发现)

以下均来自源码,是设计的硬约束:

| # | 约束 | 源码依据 |
|---|------|----------|
| C1 | L5 提取在 `llm is None` 时直接 `skipped=True`,不产出任何 L5 记忆 | `operations/l5.py:211-213` |
| C2 | L6 预测同上 | `operations/l6.py:63-65` |
| C3 | attention gate 在 `provider=none`(heuristic)下只产生 `drop`/`pass`,**不产生 `rewrite`**;`rewrite` 仅 LLM 模式 | `operations/attention.py:60-102` |
| C4 | gate 的 `drop` 触发:内容 `< min_chars`(默认 8)、或全部 token 命中噪声词(`lol/ok/test/asdf/foo/bar/todo` + 配置 `noise_words`)、或与 `dedup_window_s`(默认 3600s)内某条 L1 episodic 同内容 hash | `operations/attention.py:75-102`,`config.py:141-146` |
| C5 | `remember`(L2 fact)走 gate;`record_event`(L1 episodic)**绕过 gate**(显式事件日志) | `mcp/server.py:102-107`,`engine.py` `record_event` |
| C6 | 加工类操作只存在于 **CLI**:只有 `ladym worker --once`(含 consolidate + proceduralize + L5 + L6 + decay)。MCP 9 工具里**没有 worker** | `cli.py:280-323`,`mcp/server.py:69-169` |
| C7 | `consolidate` 双路径都有:MCP `consolidate` 工具 + CLI `consolidate` 命令 | `mcp/server.py:142-150`,`cli.py:191-206` |
| C8 | MCP server 的 db 文件是**启动时固定**的;MCP 工具只能换 `workspace` 参数,**不能换 db**。CLI 可任意 `--db`/`--workspace` | `mcp/server.py:54-67`,`cli.py:41-60` |
| C9 | **MCP 默认库与 CLI 默认库不同**:MCP server 连的是其启动配置的 db(当前会话为 `e2e.ladym.db`/ws `e2e`);`! ladym` 不带 `--db` 时走 `Config.load()` → `./ladym.db`/ws `default` | `config.py:28-33,211-225` |
| C10 | proceduralize(L3)触发阈值:成功 episode(`outcome ∈ {success, ok, done}`)数 ≥ `min_cluster_size`(默认 3),相似度 ≥ 0.55;纯 embedding,**无需 LLM** | `operations/proceduralize.py:38-84`,`config.py` System2 |
| C11 | system2 cycle 触发 L5/L6 还要求 `_count_recent_episodes ≥ min_episodes_to_run`(默认 3) | `operations/system2.py:34-43`,`config.py:124` |
| C12 | `link`/`forget` **按 memory id 操作,无 workspace 参数**(跨 workspace);其余主要工具都有 `workspace` 参数 | `mcp/server.py:158-169` |

## 4. 设计决策

### 4.1 形态:Markdown 剧本(给 agent 执行)

每个 scenario 是一个 `.md`,写成 agent 可读的剧本:步骤 + 期望。agent 执行时调 `mcp__ladym__*` 或
`! ladym` 命令,据期望判定 pass/fail。最贴近“针对 agent 的行为契约”。

### 4.2 执行路径:MCP + CLI 并重 + 一致性交叉

每个核心操作 MCP 与 CLI 各跑一遍,并设专项(S14)做“同库同 workspace 下两条路径互见”的一致性
检查——这本身就是在验证 README 的“same engine”声明。

### 4.3 L5/L6:条件分支

L5/L6 scenario 带**前置条件**:

- `provider=none`(默认/离线):验证 **skip 契约**——`! ladym worker --once` 不崩溃、报告 `l5.skipped=True` / `l6.skipped=True`、库内无 L5/L6 记忆(可审计)。
- 配置了真实 LLM(`LADYM_LLM_PROVIDER` 非 none):同一剧本走 **产出验证**分支——断言产出 `mental_model` / `forward_intent` 记忆(L5 另带 `abstracts` 边;L6 带 `valid_to` 元数据)。

### 4.4 隔离:workspace 级

每个 scenario 专属 workspace `scn-<slug>`,在 MCP server 所连 db 内隔离(因 C8,MCP 无法换 db)。
MCP 调用带 `workspace="scn-<slug>"`;CLI 调用带 `--workspace scn-<slug> --db <MCP db 路径>`(路径由
剧本第一步 `stats` 取得)。保证 MCP 与 CLI **同库同 workspace**,一致性交叉成立。

### 4.5 判定:Given/When/Then + 分级断言

- **硬断言**(客观,agent 直接判 ✅/❌):`stats` 计数、某 id 存在/不存在、`layer`/`type`/`tags`、`metadata.gated` 标记、`tier_reached`、worker 报告字段。
- **软断言**(语义,agent 判 + 必给理由):召回排名(如“目标记忆应在前 3”)。
- 每 scenario 产出 check 清单;全部跑完输出汇总表(scenario × 通过/失败/跳过 + 失败原因)。

## 5. 文件夹结构

放项目根 `scenarios/`(与 `tests/` 平级,职责不重叠)。复用 `tests/fixtures/sample_repo/` 作为
S03/S12 的索引素材,**不复制**。

```
scenarios/
├── README.md            # 入口:总览、如何执行、判定规则、结果报告格式
├── _conventions.md      # 共享约定:db 对齐(C9)、worker 不对称(C6)、ws 命名、reset 步骤
├── _template.md         # scenario 骨架
├── S01-write-recall-fact.md
├── S02-episodic-record.md
├── S03-code-index.md
├── S04-consolidate.md
├── S05-proceduralize.md
├── S06-link-tier2.md
├── S07-l5-mental-model.md
├── S08-l6-forward-intent.md
├── S09-attention-gate.md
├── S10-forget.md
├── S11-workspace-isolation.md
├── S12-incremental-index.md
├── S13-empty-recall.md
├── S14-mcp-cli-consistency.md
└── S15-decay.md
```

## 6. scenario 文件格式(`_template.md`)

````markdown
# SXX — <标题>

| 字段 | 值 |
|---|---|
| 覆盖层 | L? |
| 触发路径 | MCP / CLI / worker |
| 需 LLM | 否 / 条件分支 |
| 前置 | provider=none / 配了 LLM |

## Given
- workspace: `scn-<slug>`(先 reset:清理该 ws 残留)
- 初始数据 / 前置条件: …

## When
1. [MCP] `mcp__ladym__<tool>(...)` → 记录返回值(id 等)
2. [CLI] `! ladym <cmd> … --workspace scn-<slug> --db <db_path>`
3. …
> 第 0 步固定:`mcp__ladym__stats()` 取 `db_path`,供 CLI `--db` 对齐(见 _conventions)

## Then
- [硬] …
- [软] …(附理由)

## Teardown
- forget 本次写入的 id / reset workspace
````

## 7. 执行与判定机制

1. **db 对齐**:每个 scenario 第 0 步 `mcp__ladym__stats()` 取 MCP server 的 `db_path`;之后所有 CLI 调用带 `--db <该路径>`(解决 C9)。
2. **隔离**:专属 `scn-<slug>`;Given 段 reset、Teardown 段清新数据 → 可重复。**reset 机制**(MCP/CLI 无“列出某 ws 全部记忆”的接口):约定每个 scenario 写入的 content 一律带可检索标记 `<SLUG>`(如 `scn-s01`);reset/teardown 时 `recall("<SLUG>")` 召回本场景残留,逐条 `forget`(按 C12,forget 按 id 操作)。
3. **判定**:硬断言用工具结果核对;软断言 agent 判排名须给一句理由。
4. **结果报告**:agent 全部跑完输出汇总表。L5/L6 在 `provider=none` 时记“skip 契约通过”,配 LLM 才跑产出分支。
5. **worker 不对称**:S05/S07/S08/S15 用 `! ladym worker --once --workspace scn-<slug> --db <path>` 触发;MCP 侧只能 `stats`/`recall` 观察(解决 C6)。

## 8. 覆盖矩阵(S01–S15 规格)

> 下列为每个 scenario 的设计规格,作为实现计划展开 15 个 .md 的依据。`<db>` = 第 0 步从 stats 取得的路径。

### S01 — 写入→召回闭环(fact)
- 覆盖:L2 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s01`,reset。
- When:[MCP] `remember(content="…独特标记…", workspace="scn-s01")` 拿 id_a;[CLI] `! ladym remember "…同主题异措辞…" -w scn-s01 --db <db>` 拿 id_b;分别 `recall` 用原措辞与异措辞。
- Then:[硬] 两条 `layer=L2_semantic`/`type=fact`;recall 结果含对应 id;`stats` 该 ws L2 计数 +2。[软] 异措辞召回目标仍在前 3。

### S02 — episodic 记录与召回
- 覆盖:L1 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s02`,reset。
- When:[MCP] `record_event(agent, action, observation, outcome, workspace="scn-s02")`;[CLI] `! ladym record --agent … --action … -w scn-s02 --db <db>`;`recall` 事件主题。
- Then:[硬] 记录返回 `layer=L1_episodic`/`type=event`;`stats` L1 计数 +2;recall 能命中。注意 C5:record 绕过 gate。

### S03 — 代码索引与符号召回
- 覆盖:L2(code) · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s03`,reset;索引素材 `tests/fixtures/sample_repo`。
- When:[MCP] `index_code(root="<fixture>", workspace="scn-s03")`;[CLI] `! ladym index <fixture> -w scn-s03 --db <db>`;`search_code`/`recall --code` 查 `verify_password` 之类。
- Then:[硬] `stats` `code_symbols` > 0;召回结果 `type=code_symbol`,`source` 含文件路径;signature/qualified_name 可见。

### S04 — consolidate L1→L2
- 覆盖:L1→L2 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s04`,reset;先灌 ≥ `min_episodes_to_trigger`(默认 3,见 config)条同主题 L1 事件。
- When:[MCP] `consolidate(workspace="scn-s04")`;[CLI] `! ladym consolidate -w scn-s04 --db <db>`(二次)。
- Then:[硬] 报告含 `ADD/UPDATE/DELETE/NOOP` 字段;首次 `promoted_to_semantic ≥ 1`;`stats` L2 fact 增加。

### S05 — proceduralize → L3 playbook
- 覆盖:L3 · 路径:**仅 CLI worker**(MCP observe) · 需LLM:否
- Given:ws `scn-s05`,reset;灌 ≥ 3 条 `outcome=success` 的同类 L1 事件(C10)。
- When:`! ladym worker --once -w scn-s05 --db <db>`;MCP 侧 `stats`/`recall`。
- Then:[硬] worker 跑通;`stats` 出现 `L3_procedural`/`type=playbook`;playbook 名含动作动词 + `(N episodes)`。

### S06 — link + tier2 扩展
- 覆盖:L4 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s06`,reset;先 `remember` 两条相关 fact A、B。
- When:`link(A.id, B.id, relation="elaborates")`;`recall` 一个低覆盖锚点查询(参照 `test_end_to_end.test_tier2_triggered_when_anchor_links_exist`)。
- Then:[硬] `stats` `edges +1`;该查询 `tier_reached=2` 或召回结果含 A。

### S07 — L5 mental model(条件分支)
- 覆盖:L5 · 路径:**仅 CLI worker**(MCP observe) · 需LLM:**条件分支**
- Given:ws `scn-s07`,reset;灌 ≥ `min_episodes_to_run`(3) 条 L1 + 若干 L2 fact(C11)。
- When:`! ladym worker --once -w scn-s07 --db <db>`。
- Then(provider=none):[硬] worker 不崩溃;报告 `l5.skipped=True`;库内无 `L5_mental` 记忆。
- Then(配 LLM):[硬] 产出 `type=mental_model` 记忆;带 `abstracts` 边指向成员。

### S08 — L6 forward-intent(条件分支)
- 覆盖:L6 · 路径:**仅 CLI worker**(MCP observe) · 需LLM:**条件分支**
- Given:ws `scn-s08`,reset;灌若干 L1 事件。
- When:`! ladym worker --once -w scn-s08 --db <db>`。
- Then(provider=none):[硬] `l6.skipped=True`;无 `L6_predictive` 记忆。
- Then(配 LLM):[硬] 产出 `type=forward_intent`;`metadata.valid_to` > now。

### S09 — attention gate:drop 矩阵 + pass 对照
- 覆盖:gate · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s09`,reset。
- When(三个 drop 子情况 + 一个 pass;gate 判定顺序为 too short → noise → recent duplicate,见 attention.py:75-102):
  - `remember` 内容长度 < 8 → too short(如 `"hi"`);
  - `remember` 全噪声词且 ≥8 字符 → noise(如 `"lol ok test asdf foo"`;若 <8 字符会先因 too short 落地,测不到 noise 分支);
  - 先 `record_event(agent, action, observation, outcome)` 一条,再 `remember` 一段 content 与该 L1 事件 content 字符串**完全相同**的文本 → recent duplicate(L1 content 为管道格式 `agent=… | action=… | …`,dedup 按 content hash 比对,故“同内容”指同字符串、非同语义);
  - `remember` 一条正常长内容(pass 对照)。
- Then:[硬] 前三条**未持久化**(stats 不增 / recall 找不到);pass 那条 `layer=L2_semantic` 且能 recall。(注:`remember` 经 MCP/CLI 走 gate;`record` 绕过——见 C5。MCP `remember` 工具返回值需确认是否暴露 gated 标记,若不暴露则用 stats 计数 + recall 间接断言。)

### S10 — forget 删除
- 覆盖:维护 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s10`,reset;先 `remember` 拿 id。
- When:`forget(id)`;`recall` 该内容。
- Then:[硬] forget 返回成功;`recall` 结果不含该 id;`stats` 计数 -1。

### S11 — workspace 隔离
- 覆盖:隔离 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s11a`、`scn-s11b`,均 reset。
- When:在 `scn-s11a` `remember` 一条独特 fact;`recall` 同内容但 `workspace="scn-s11b"`。
- Then:[硬] `scn-s11a` 能召回,`scn-s11b` 召回不到(参照 `test_workspace_isolation`)。MCP 与 CLI 各验证一遍(注意 CLI 用 `--workspace` 切换)。

### S12 — 增量索引(新增/跳过未变)
- 覆盖:L2(code) · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s12`,reset;一个 tmp 微型 repo(2 文件)。
- When:index 一次;新增第 3 文件;再 index。
- Then:[硬] 第二次 `files_indexed=1`、`files_skipped_unchanged=2`;新符号可 `search_code` 命中(参照 `test_incremental_index_*`)。

### S13 — 空召回 / 不崩溃
- 覆盖:健壮性 · 路径:MCP+CLI · 需LLM:否
- Given:ws `scn-s13`,reset(空)。
- When:`recall("一个绝不存在的奇异查询 zzqqxx123")`。
- Then:[硬] 返回空结果集、`tier_reached=1`、不抛异常;CLI 输出 `no memories matched`。

### S14 — MCP↔CLI 一致性专项
- 覆盖:一致性 · 路径:跨路径 · 需LLM:否
- Given:ws `scn-s14`,reset。
- When:[MCP] `remember("共享 fact X", workspace="scn-s14")` 拿 id_a;[CLI] `! ladym recall "共享 fact X" -w scn-s14 --db <db>`;再反向:[CLI] `remember` 写 id_b,[MCP] `recall` 验证。
- Then:[硬] CLI 能召回 MCP 写入的(id_a 出现),MCP 能召回 CLI 写入的(id_b 出现)——证明两条路径**同库同 ws 互见**(“same engine” 契约)。这是本套剧本的核心断言之一。

### S15 — decay 不伤 L2/L3
- 覆盖:衰减 · 路径:**仅 CLI worker**(MCP observe) · 需LLM:否
- Given:ws `scn-s15`,reset;先有若干 L2 fact / L3 playbook(借 S04/S05 手法产生)。
- When:将 L1 episodic 的 `last_access_at` 改旧(参照 `test_full_memory_lifecycle`);`! ladym worker --once -w scn-s15 --db <db>`。
- Then:[硬] worker 跑通(decay 段不抛错);`stats` 显示 L2/L3 记忆仍在(decay 不剪 L2/L3)。注:CLI 无独立 decay 命令,只能经 worker(C6)。

## 9. 验收标准

- `scenarios/` 含 README、_conventions、_template 及 S01–S15 共 18 个文件。
- 每个 scenario 自包含(Given/When/Then/Teardown 齐全),引用 `_conventions.md` 而非重复约定。
- 剧本对 C1–C12 每条约束都有对应的可执行验证(gate→C3/C4/C5;worker→C6;一致性→C9/C12;L5/L6→C1/C2/C11)。
- 默认离线(`provider=none`)下,除 S07/S08 的产出分支外,所有 scenario 可由 agent 完整执行并判定;S07/S08 在离线下走 skip 契约分支。
- README 给出执行流程、判定规则、结果汇总表格式;_conventions 给出 db 对齐、worker 不对称、ws 命名与 reset 步骤。

## 10. 后续

实现计划由 writing-plans skill 展开(拆分为:写 README/_conventions/_template → 写 S01–S15 → 用一个真实会话试跑校准剧本)。剧本落地后,可作为 ladyM 回归用的 agent 侧验收套件长期维护。
