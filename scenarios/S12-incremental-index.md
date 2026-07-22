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
