# `[codeindex]` Extra — 默认精简安装 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 tree-sitter 系依赖从核心 `dependencies` 移入可选 `[codeindex]` extra,使下游包 `pip install ladym` 默认得到精简的记忆核心(无代码解析);需要代码索引时才装 `[codeindex]`。

**Architecture:** tree-sitter 已是懒加载(仅 `index_code` 路径触发),记忆路径零依赖。改动分三处:(1) `pyproject.toml` 依赖重组;(2) `indexer.py` 加一次性运行时守卫,缺 `[codeindex]` 时给出引导;(3) README 安装文档同步。配套加打包回归测试 + 守卫单测。

**Tech Stack:** Python ≥3.11、hatchling、tomllib(stdlib)、pytest、tree-sitter(移为可选)。

## Global Constraints

- **分发约定**:所有安装命令必须用 `git+https://github.com/ProjAnvil/LadyM.git`(ladym 不在 PyPI)。README 里的命令同样遵循,绝不写成 `pip install 'ladym[...]'`。
- **extra 命名**:`codeindex`(小写、单词、无连字符,对齐 `Engine.index_code()`)。
- **懒加载红线**:改动不得让 `import ladym` / `Engine()` / `recall()` / `remember()` 触发 tree-sitter 导入。守卫只在 `index_codebase` 入口一次性检查。
- **既有降级保留**:`get_parser()`(`languages.py:158`)对"tree-sitter 已装但某语言无 grammar"的逐语言 `None` 降级行为**保持不变**;守卫只针对"tree-sitter 整个没装"。
- **守卫位置**:`src/ladym/code/indexer.py` 的 `index_codebase()` 开头(spec A2 原写 `languages.py`,实测该处会被 `indexer.py:104` 的宽泛 `except` 二次吞掉,故上移到入口做一次性检查)。

**已在分支**:`feat/codeindex-extra`(spec 已提交)。

---

### Task 1: pyproject.toml 依赖重组 + 打包回归测试

**Files:**
- Modify: `pyproject.toml:21-31`(core deps)、`pyproject.toml:33-54`(optional-dependencies)、`pyproject.toml:55-61`(`[dev]`)
- Test: `tests/unit/test_packaging.py`(新建)

**Interfaces:**
- Produces: `pyproject.toml` 中 `[codeindex]` extra(含 `tree-sitter>=0.23`、`tree-sitter-language-pack>=0.1`);`[all]` 与 `[dev]` 均聚合 `codeindex`;core `dependencies` 不含 tree-sitter。

- [ ] **Step 1: 写失败测试** —— 新建 `tests/unit/test_packaging.py`

```python
"""Guards the dependency structure: tree-sitter must stay optional ([codeindex]),
never leak back into core dependencies."""

import tomllib
from pathlib import Path

PYPROJECT = Path(__file__).resolve().parents[2] / "pyproject.toml"


def _load() -> dict:
    with PYPROJECT.open("rb") as f:
        return tomllib.load(f)


def test_tree_sitter_not_in_core_dependencies():
    """tree-sitter must be optional, never a hard dependency."""
    core = _load()["project"]["dependencies"]
    leaked = [d for d in core if "tree-sitter" in d]
    assert not leaked, f"tree-sitter leaked into core deps: {leaked}"


def test_codeindex_extra_holds_tree_sitter():
    opt = _load()["project"]["optional-dependencies"]
    codeindex = " ".join(opt.get("codeindex", []))
    assert "tree-sitter" in codeindex
    assert "tree-sitter-language-pack" in codeindex


def test_all_and_dev_aggregate_codeindex():
    opt = _load()["project"]["optional-dependencies"]
    assert any("codeindex" in s for s in opt["all"]), "[all] must include codeindex"
    assert any("codeindex" in s for s in opt["dev"]), "[dev] must include codeindex"
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_packaging.py -v`
Expected: 3 个 FAIL —— `test_tree_sitter_not_in_core_dependencies` 因 tree-sitter 仍在 core(当前 `pyproject.toml:25-27`);`test_codeindex_extra_holds_tree_sitter` / `test_all_and_dev_aggregate_codeindex` 因 `codeindex` extra 尚不存在(KeyError 或 assert 失败)。

- [ ] **Step 3: 改 pyproject.toml**

(a) **core deps 删除两行**(`pyproject.toml:25-26`):

```toml
dependencies = [
    "pydantic>=2.6",
    "typer>=0.12",
    "rich>=13.7",
    "sqlite-vec>=0.1.6",
    "numpy>=1.26",
    "sqlmodel>=0.0.16",
    "cryptography>=42.0",
]
```
（删除 `"tree-sitter>=0.23",` 和 `"tree-sitter-language-pack>=0.1",` 两行）

(b) **新增 `[codeindex]` extra**(在 `[project.optional-dependencies]` 里 `web = [...]` 之后、`all = ...` 之前插入):

```toml
codeindex = ["tree-sitter>=0.23", "tree-sitter-language-pack>=0.1"]
```

(c) **`all` 聚合补 codeindex**:

```toml
all = ["ladym[web,llm,local,mcp,openai,anthropic,langgraph,codeindex]"]
```

(d) **`[dev]` 补 codeindex**(在 `dev = [` 列表首行加自引用,DRY —— 单一真相源,与 `all` 同模式):

```toml
dev = [
    "ladym[codeindex]",  # so `pip install -e '.[dev]'` can run code-indexing tests
    "pytest>=8.0",
    "pytest-asyncio>=0.23",
    "pytest-cov>=5.0",
    "ruff>=0.5",
    "mypy>=1.10",
]
```

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/unit/test_packaging.py -v`
Expected: 3 PASS。

- [ ] **Step 5: 提交**

```bash
git add pyproject.toml tests/unit/test_packaging.py
git commit -m "$(cat <<'EOF'
build: move tree-sitter to optional [codeindex] extra

Bare `pip install ladym` now yields a lean memory-only core (no
tree-sitter / language-pack). Code indexing is opt-in via
`pip install 'ladym[codeindex]'`. [all] and [dev] aggregate it so
existing full/dev installs are unaffected.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: index_code 运行时守卫(缺 [codeindex] 时友好报错)

**Files:**
- Modify: `src/ladym/code/indexer.py:53-64`(`index_codebase` 函数体开头)
- Test: `tests/integration/test_code_indexer.py`(追加测试)

**Interfaces:**
- Consumes: 无(Task 1 的 pyproject 改动让"缺 tree-sitter"成为真实可能态)。
- Produces: `index_codebase()` 在 tree-sitter 不可导入时抛 `ImportError`,消息含 `[codeindex]` 引导;经 `Engine.index_code()`(`engine.py:368`)透传给调用方。既有"已装但某语言无 grammar"的逐语言降级(`get_parser` 返回 None → chunk fallback)**不变**。

- [ ] **Step 1: 写失败测试** —— 在 `tests/integration/test_code_indexer.py` 末尾追加

```python
import sys


def test_index_code_without_codeindex_raises_guidance(monkeypatch, tmp_path):
    """Without the [codeindex] extra, index_code raises a helpful ImportError
    instead of silently degrading every file to chunk fallback."""
    # simulate tree-sitter not installed: None in sys.modules => import raises
    monkeypatch.setitem(sys.modules, "tree_sitter", None)
    monkeypatch.setitem(sys.modules, "tree_sitter_language_pack", None)

    eng = Engine(Config.for_testing(tmp_path))
    try:
        with pytest.raises(ImportError, match=r"\[codeindex\]"):
            eng.index_code(tmp_path)  # empty dir; guard fires before the walk
    finally:
        eng.close()
```

（文件顶部已有 `import pytest`、`from ladym.config import Config`、`from ladym.engine import Engine`,只需补 `import sys`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/integration/test_code_indexer.py::test_index_code_without_codeindex_raises_guidance -v`
Expected: FAIL —— 当前 `index_codebase` 无守卫;tree-sitter 被 monkeypatch 成 None 后,`get_parser` 的 `except Exception`(`languages.py:158`)吞掉错误返回 None,`index_code` 静默降级为 chunk fallback 而非 raise,故 `pytest.raises` 不触发。

- [ ] **Step 3: 在 index_codebase 开头加守卫**

修改 `src/ladym/code/indexer.py`,在 `def index_codebase(...)` 函数体最前面(`start = time.time()` 之前)插入:

```python
def index_codebase(
    root: Path,
    store: SQLiteStore,
    embedder: EmbeddingProvider,
    *,
    cfg: Config,
    workspace: str | None = None,
    force: bool = False,
    language_filter: list[str] | None = None,
) -> IndexReport:
    """Walk ``root`` and index every supported source file."""
    try:
        import tree_sitter  # noqa: F401  — guard: [codeindex] extra check
    except ImportError as e:
        raise ImportError(
            "code indexing requires the [codeindex] extra. "
            "Install with: pip install 'ladym[codeindex]'"
        ) from e
    start = time.time()
    # ...rest unchanged
```

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/integration/test_code_indexer.py::test_index_code_without_codeindex_raises_guidance -v`
Expected: PASS。

- [ ] **Step 5: 跑全套代码索引测试,确认既有行为没回归**

Run: `uv run pytest tests/integration/test_code_indexer.py -v`
Expected: 全 PASS(含既有的 `test_index_codebase_extracts_symbols` 等 —— dev 环境装了 `[codeindex]`,tree-sitter 可用,守卫不触发)。

- [ ] **Step 6: 提交**

```bash
git add src/ladym/code/indexer.py tests/integration/test_code_indexer.py
git commit -m "$(cat <<'EOF'
feat(code): guard index_code with friendly [codeindex] hint

When tree-sitter isn't installed (the new default lean install),
index_code now raises an ImportError pointing to the [codeindex]
extra instead of silently degrading every file to chunk fallback.
Per-language graceful degradation (tree-sitter present but no
grammar) is unchanged.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: README 安装文档同步

**Files:**
- Modify: `README.md:110-163`(Install 段)、`README.md:128-130`(global CLI extras 列表)、`README.md:161-162`(sqlite-vec 说明)
- Modify: `README.zh-CN.md`(对应"安装"段,做平行改动)

**Interfaces:**
- 无代码接口。产出:文档准确反映"默认精简 + `[codeindex]` 可选",安装命令遵循 `git+url` 约定。

- [ ] **Step 1: 改 README.md「As a global CLI」段(第 128-131 行)**

在 extras 列举里加入 `[codeindex]`,并注明裸装=仅记忆:

```markdown
```bash
uv tool install .                # offline default — memory core only, no code indexing
uv tool install ".[codeindex]"   # + tree-sitter code indexing (index_code / search_code)
uv tool install ".[web,llm]"     # LLM providers + the `ladym config` web editor
# other extras compose the same way: [mcp] [local] [openai] [anthropic] [codeindex]
```
```

- [ ] **Step 2: 改 README.md「For development」段(第 152-158 行)**

在 dev extras 里补一行 `[codeindex]`:

```markdown
```bash
git clone https://github.com/ProjAnvil/LadyM.git && cd ladyM
uv venv --python 3.12
uv pip install -e ".[dev]"            # core + test/lint tooling, editable (incl. [codeindex])
# optional extras stack on top:
uv pip install -e ".[mcp]"            # MCP server (for Claude Code / Cursor)
uv pip install -e ".[local]"          # sentence-transformers embeddings
uv pip install -e ".[openai]"         # OpenAI embeddings
uv pip install -e ".[llm]"            # LLM provider support (consolidation classifier)
uv pip install -e ".[web]"            # FastAPI + HTMX `ladym config` editor
```
```
（`[dev]` 现已聚合 `[codeindex]`,所以开发安装自动含 tree-sitter;这行注释点明即可。）

- [ ] **Step 3: 改 README.md 第 161-162 行的 native-deps 说明**

```markdown
Requires Python ≥ 3.11 (uses `enum.StrEnum`). `sqlite-vec` ships as a wheel — no native
toolchain needed on macOS/Linux/Windows. `tree-sitter` is optional via the `[codeindex]`
extra; the default install is memory-only.
```

- [ ] **Step 4: 平行改动 README.zh-CN.md**

对中文 README 的"安装"段做对应翻译改动:global CLI extras 列表加 `[codeindex]`;开发安装段注明 `[dev]` 含 `[codeindex]`;native-deps 说明加一句"tree-sitter 经 `[codeindex]` 可选,默认安装仅含记忆核心"。命令本身保持英文(`git+url` 约定不变)。

- [ ] **Step 5: 人工核对无遗漏**

Run: `grep -n "tree-sitter\|tree_sitter\|codeindex\|\[all\]\|\[dev\]" README.md README.zh-CN.md`
Expected: 所有提及 tree-sitter 的地方都对应了"可选 / `[codeindex]`"语境;无残留"tree-sitter 是核心依赖"的措辞。

- [ ] **Step 6: 提交**

```bash
git add README.md README.zh-CN.md
git commit -m "$(cat <<'EOF'
docs: document [codeindex] extra and lean default install

Bare install is now memory-only; tree-sitter code indexing is opt-in
via [codeindex]. Updated both READMEs' install sections; all commands
follow the git+url convention.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## 验收(对齐 spec 验证标准)

实现全部完成后,跑一次端到端核对(手动,非自动化):

1. **精简安装可用** —— 新 venv:`uv pip install "git+https://github.com/ProjAnvil/LadyM.git@v0.2.0"`,然后 `python -c "from ladym import Engine, Config; e=Engine(Config()); e.recall('x'); e.close()"` 成功;`pip list | grep tree-sitter` **无输出**。
2. **守卫触发** —— 同 venv 调 `e.index_code('./src')` → `ImportError: code indexing requires the [codeindex] extra...`。
3. **带 extra 恢复** —— `uv pip install "git+https://github.com/ProjAnvil/LadyM.git[codeindex]"` 后 `index_code` 正常（与 README 第 122 行 `git+URL[all]` 同形式,extra 紧跟 URL）。或 clone 后 `uv pip install -e '.[codeindex]'`。
4. **全量测试** —— `uv run pytest` 全绿(含代码索引用例,因 `[dev]` 已含 `[codeindex]`)。
