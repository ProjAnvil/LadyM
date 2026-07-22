# S09 — attention gate:drop 矩阵(MCP+CLI+SDK)

| 覆盖层 | gate | 路径 | MCP+CLI(+SDK 对照) | 需LLM | 否 |

> gate 已于 2026-07-22 修复(commit b1647e9 CLI / f717d64 MCP):MCP/CLI `remember` 现路由经 `eng.remember`,attention gate 在 agent 写路径上**生效**。下列 noise / recent-duplicate 内容经 MCP/CLI `remember` **被 drop(不持久化)**——MCP 返回 `{"id":null,"gated":"dropped","reason":…}`、CLI 打印 `dropped reason=…`;SDK `eng.remember()` 同行为。历史:修复前 agent 路径绕过 gate(见 `_conventions §8.1`)。
>
> **2026-07-22 重构**:gate 改为 noise+dup 确定性前置 + LLM 语义层(详见 `docs/superpowers/specs/2026-07-22-attention-gate-noise-dup-prefix-design.md`)。`too short` 规则移除——"hi" 不再被确定性 drop。**drop 矩阵现在两种配置都成立(noise/dup 前置确定性 drop);"hi" 改为演示 LLM 增值。**

## Given
- workspace `scn-s09`;取 `<db>`;reset。
- gate 两层:heuristic 前置(noise 词表 / recent-duplicate hash,确定性 drop)+ 语义层(配了 LLM 交 LLM 判,否则 pass)。见 `src/ladym/operations/attention.py`。

## When
1. [MCP] `mcp__ladym__remember(content="hi", workspace="scn-s09")` → **预期**:**provider=none** `{"id":..,"hash":..}`(pass,持久化);**配了 LLM** 交 LLM 判(软断言:gate 被调用、返回结构化结果,drop/pass 均可接受——演示 LLM 对短内容的语义判断)。
2. [MCP] `mcp__ladym__remember(content="lol ok test asdf foo", workspace="scn-s09")` → **预期** drop:`reason="noise"`(两种配置都确定性 drop——heuristic 前置)
3. 先建 L1 事件:`! ladym record --agent x --action "scn-s09 dup" --observation obs1 --outcome ok -w scn-s09 --db <db>`;其 content 串为 `agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok`(见 _conventions §8.3);再 `[MCP] mcp__ladym__remember(content="agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok", workspace="scn-s09")` → **预期** recent-duplicate drop:`reason="recent duplicate"`(两种配置都确定性 drop——heuristic 前置)
4. [MCP] `mcp__ladym__remember(content="scn-s09 这是一条正常的足够长的语义记忆用于 pass 对照", workspace="scn-s09")` → pass:`{"id":<32hex>,"hash":<hex>}`(持久化)
5. [CLI] `! ladym stats -w scn-s09 --db <db>` → 记 L2 计数
6. SDK 对照:`! uv run pytest tests/unit/test_attention_gate.py -q` → 确认 drop 矩阵在 `eng.remember()` 上生效

## Then
- [硬] noise(步骤2)两路径返回 `gated=="dropped"`、`reason=="noise"`、id null
- [硬] recent-duplicate(步骤3)两路径返回 `gated=="dropped"`、`reason=="recent duplicate"`、id null
- [硬] "hi"(步骤1):provider=none 时 pass(持久化);配 LLM 时 gate 被调用(用返回含 `gated` 或持久化与否判定,不强求 drop/pass)
- [软] 给一句:noise/dup 是确定性硬过滤(配置无关);"hi" 的判断体现了 LLM 的语义增值(none 时 pass、LLM 时按语义)

## Teardown
reset scn-s09。
