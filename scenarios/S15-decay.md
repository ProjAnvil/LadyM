# S15 — decay 不伤 L2/L3

| 覆盖层 | 衰减 | 路径 | CLI worker(MCP observe) | 需LLM | 否 |

## Given
- workspace `scn-s15`;取 `<db>`;reset。
- 先造 L2 fact + L3 playbook(借 S04/S05 手法):

## When
1. [MCP] `mcp__ladym__remember(content="scn-s15 持久语义事实", workspace="scn-s15")`(L2)
2. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s15 deploy", observation="ran deploy.sh", outcome="success", workspace="scn-s15")`
3. [CLI] `! ladym worker --once -w scn-s15 --db <db>`(产出 L3 playbook)
4. [CLI] `! ladym stats -w scn-s15 --db <db>` → 记该 ws L2、L3 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)
5. 将该 ws 的 L1 改旧:`! sqlite3 <db> "UPDATE memories SET last_access_at = strftime('%s','now') - 100*365*86400 WHERE workspace='scn-s15' AND layer='L1_episodic'"`
6. [CLI] `! ladym worker --once -w scn-s15 --db <db>`(再跑,含 decay)
7. [CLI] `! ladym stats -w scn-s15 --db <db>` → 记该 ws 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)

## Then
- [硬] 步骤6 worker 退出码 0
- [硬] 步骤7 `L2_semantic`、`L3_procedural` 计数与步骤4 一致(decay 不剪 L2/L3)

## Teardown
reset scn-s15。
