# S09 — attention gate:drop 矩阵(MCP+CLI+SDK)

| 覆盖层 | gate | 路径 | MCP+CLI(+SDK 对照) | 需LLM | 否 |

> gate 已于 2026-07-22 修复(commit b1647e9 CLI / f717d64 MCP):MCP/CLI `remember` 现路由经 `eng.remember`,attention gate 在 agent 写路径上**生效**。下列 too short / noise / recent-duplicate 内容经 MCP/CLI `remember` **被 drop(不持久化)**——MCP 返回 `{"id":null,"gated":"dropped","reason":…}`、CLI 打印 `dropped reason=…`;SDK `eng.remember()` 同行为。历史:修复前 agent 路径绕过 gate(见 `_conventions §8.1`)。

## Given
- workspace `scn-s09`;取 `<db>`;reset。
- gate 判定顺序:too short(`< min_chars=8`)→ noise(全 token 命中噪声词 `lol/ok/test/asdf/foo/bar/todo`)→ recent duplicate(与 `dedup_window_s`=3600s 内某 L1 同 content hash)。见 `src/ladym/operations/attention.py:75-102`。
- gate 模式:本剧本验证 **heuristic** gate(`llm_provider=none`)。若部署配了 LLM(`LADYM_LLM_PROVIDER` 非 none 或 `ladym.toml [llm]`),attention_gate 改走 LLM 判定、drop 矩阵不保证——跑前 `export LADYM_LLM_PROVIDER=none` 强制 heuristic(同 §5 条件分支约定);embedding 非本地时一并 `export LADYM_EMBEDDING=hashing` 以离线跑 pass 步骤。

## When
1. [MCP] `mcp__ladym__remember(content="hi", workspace="scn-s09")` → **预期** drop:`{"id":null,"hash":null,"gated":"dropped","reason":"too short"}`
2. [MCP] `mcp__ladym__remember(content="lol ok test asdf foo", workspace="scn-s09")` → **预期** drop:`reason="noise"`
3. 先建 L1 事件:`! ladym record --agent x --action "scn-s09 dup" --observation obs1 --outcome ok -w scn-s09 --db <db>`;其 content 串为 `agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok`(见 _conventions §8.3);再 `[MCP] mcp__ladym__remember(content="agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok", workspace="scn-s09")` → **预期** recent-duplicate drop:`reason="recent duplicate"`
4. [MCP] `mcp__ladym__remember(content="scn-s09 这是一条正常的足够长的语义记忆用于 pass 对照", workspace="scn-s09")` → pass:`{"id":<32hex>,"hash":<hex>}`(持久化)
5. [CLI] `! ladym stats -w scn-s09 --db <db>` → 记 L2 计数
6. SDK 对照:`! uv run pytest tests/unit/test_attention_gate.py -q` → 确认 drop 矩阵在 `eng.remember()` 上生效

## Then
- [硬] 步骤1-3 各自返回 `gated=="dropped"` 且 `id` 为 null;`reason` 分别为 `too short` / `noise` / `recent duplicate`
- [硬] 步骤4 返回非空 `id`/`hash`(无 `gated` 键)
- [硬] 步骤5 该 ws `L2_semantic` fact 计数 = 1(仅步骤4 pass 持久化;步骤1-3 被 drop)——证明 agent 路径 `remember` 现已应用 gate
- [硬] 步骤6 SDK 单测通过:drop 矩阵三路径(SDK/MCP/CLI)一致
- [软] 给一句判断:agent 写路径的噪声防护现已与 SDK 对齐

## Teardown
reset scn-s09。
