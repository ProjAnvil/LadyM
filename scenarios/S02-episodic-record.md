# S02 — episodic 记录与召回

| 覆盖层 | L1 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s02`;取 `<db>`;reset。注意:`record_event` 绕过 attention gate(C5),必定持久化。

## When
1. [MCP] `mcp__ladym__record_event(agent="claude", action="scn-s02 fixed login bug", observation="jwt expiry was wrong", outcome="success", workspace="scn-s02")` → 记 `id_a`、返回的 layer/type
2. [CLI] `! ladym record --agent claude --action "scn-s02 deployed v2" --observation "green build" --outcome success -w scn-s02 --db <db>` → 记 `id_b`
3. [MCP] `mcp__ladym__recall(query="scn-s02 login bug", workspace="scn-s02")`
4. [CLI] `! ladym stats -w scn-s02 --db <db>` → 记该 ws 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)

## Then
- [硬] 步骤1/2 返回 `layer=L1_episodic`、`type=event`
- [硬] 步骤4 `L1_episodic` 计数 ≥ 2
- [硬] 步骤3 能召回到含 `scn-s02` 的事件(绕 gate,已持久化)

## Teardown
reset scn-s02。