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
5. [CLI] `! ladym stats -w scn-s03 --db <db>` → 记该 ws 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)

## Then
- [硬] 步骤1 `symbols_written > 0`
- [硬] 步骤3/4 结果含 `type=code_symbol`、`source` 含 `auth/service.py`、content 含 `verify_password`
- [硬] 步骤5 `code_symbols > 0`
- [软] 召回的符号 signature 含 `password` 参数(给理由:索引保留函数签名)

## Teardown
reset scn-s03(代码符号同为 memories 行,SQL reset 一并清除)。