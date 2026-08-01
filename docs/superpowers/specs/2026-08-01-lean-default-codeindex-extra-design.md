# 默认精简安装 + `[codeindex]` 可选 extra — 设计

- **日期**: 2026-08-01
- **状态**: 草案,待 review
- **相关**: 无直接前置;改的是 `pyproject.toml` 打包元数据与一处运行时守卫。背景决策依据:`git+url` 分发约定(2026-07-25,[[ladym-distribution-git-url]]),tree-sitter 懒加载现状。

## 背景与动机

外部开发者想把 ladyM 作为**依赖内嵌进自己的 Python 包**(该包同样 git-only,不发 PyPI)。诉求:用户 `pip install` 他们的包时,ladyM 被 pip 自动拉取,**用户无需手动安装 ladyM**,也不希望 ladyM 拖入与"记忆"无关的重量级依赖。

当前现状(grep / Read 确认):

1. **ladyM 核心依赖里硬绑了 tree-sitter 系**——`pyproject.toml:25-27` 把 `tree-sitter>=0.23` 和 `tree-sitter-language-pack>=0.1` 放在 `dependencies`(非可选)。后者是**编译型解析器集合,体积数十 MB**。任何依赖 ladyM 的包都会被迫安装全套语言解析器。

2. **但 tree-sitter 实际是懒加载的**,记忆路径完全不依赖它:
   - `import ladym` / `Engine()` / `recall()` / `remember()` —— **零 tree-sitter import**(`src/ladym/__init__.py`、`src/ladym/engine.py` 顶层均不导入)。
   - tree-sitter 只在 `src/ladym/code/languages.py:143-149` 函数体内导入,且仅由 `Engine.index_code()`(→ `engine.py:371` 懒导入 `.code.indexer`)触发。
   - 也就是说,移出核心依赖对记忆 / 巩固 / 关联 / 统计等全部功能**无任何影响**。

3. **memory-only 消费者被白白拖入几十 MB 死重**——这正是内嵌场景的痛点。

### 解决思路

把安装分两档:**默认即精简**(仅记忆核心),代码索引变成**可选增项**。内嵌方声明裸依赖即拿到精简版;需要给代码建 RAG 时再带 extra。

## 设计

### 总览

| 安装方式 | 得到什么 | 适用场景 |
|----------|---------|---------|
| `pip install ladym`(默认) | 精简记忆核心:recall / remember / record_event / stats / consolidate / link / forget / search_code(仅搜已索引的) | **内嵌进其他程序**(本次主场景) |
| `pip install 'ladym[codeindex]'` | 上述 + 代码索引:`index_code()` 可用 | 需要给代码库建 RAG |

`all` 聚合 extra 仍包含 `codeindex`,`[all]` 用户行为无变化。

### Part A —— ladyM 打包改动(3 处)

#### A1. `pyproject.toml`:tree-sitter 系移入 `[codeindex]`

```toml
dependencies = [
    "pydantic>=2.6",
    "typer>=0.12",
    "rich>=13.7",
    "sqlite-vec>=0.1.6",
    # tree-sitter / tree-sitter-language-pack 移除 → 移到 [codeindex]
    "numpy>=1.26",
    "sqlmodel>=0.0.16",
    "cryptography>=42.0",
]

[project.optional-dependencies]
# ...既有 local/openai/anthropic/mcp/langgraph/llm/web 不变...
codeindex = ["tree-sitter>=0.23", "tree-sitter-language-pack>=0.1"]
all = ["ladym[web,llm,local,mcp,openai,anthropic,langgraph,codeindex]"]
```

命名依据:`[codeindex]` 对齐 `Engine.index_code()` 用户入口,自解释为"代码索引能力";规避了 `[codebase]` 会让人误以为"安装 ladyM 源码"的歧义,也比 `[treesitter]`(对外部不透明)更友好。

#### A2. 友好运行时守卫

`src/ladym/code/languages.py:143` 处的 `import tree_sitter` 失败时,当前会抛裸 `ImportError`。改为项目既有风格的引导消息(对齐 `mcp/server.py:60-63` 对 `mcp` extra 的处理):

```python
# 在 languages.py 的解析函数里,把现有的 import 块的 except 改为:
except ImportError as e:
    raise ImportError(
        "code indexing requires the [codeindex] extra. "
        "Install with: pip install 'ladym[codeindex]'"
    ) from e
```

注意:`search_code()` 本身**不**需要 tree-sitter(它只查已索引的 `vec_memories` / 符号表),故守卫只放在 `index_code` 路径(`languages.py`),不影响无 extra 时对历史已索引代码的检索。

#### A3. 测试与 CI 适配

代码索引用例(`tests/integration/test_code_indexer.py` 等)依赖 tree-sitter。移出核心后:
- `[dev]` extra **需补上 `codeindex`**(当前 `[dev]` 只含测试工具,不含功能 extra),否则 `pip install -e '.[dev]'` 后代码索引测试会 import 失败。
- 或者:CI 直接装 `[all]`(已含 `codeindex`)。两者择一,推荐 `[dev]` 加 `codeindex`,保证 `pip install -e '.[dev]'` 一条命令即可跑全量测试。

### Part B —— 内嵌方的接入模式(消费者侧,非 ladyM 仓库改动)

内嵌方的 `pyproject.toml` 一行声明裸依赖(不带 extra = 拿到精简版):

```toml
dependencies = [
    "ladym @ git+https://github.com/ProjAnvil/LadyM.git@v0.2.0",
    # ...
]
```

- 标签锁定 `@v0.2.0` 保证可复现(`uv.lock` 会锁到具体 commit)。
- 仓库公开可达,无需 git 凭据配置。
- 用户 `pip install git+<内嵌方仓库>` → pip 自动拉精简 ladyM,**不装 tree-sitter / language-pack**,用户全程不感知 ladyM。
- 日后内嵌方也需要代码索引,把依赖改为 `ladym[codeindex] @ git+...` 即可。

## 范围与非目标

- ✅ **本次做**:tree-sitter 系 → `[codeindex]` extra;默认精简;`languages.py` 运行时守卫;`[dev]` 补 `codeindex`;README 安装段文档更新(遵循 `git+url` 约定)。
- ⏸️ **非目标,仅记录**:
  - `typer`/`rich` 是 CLI 专用,纯库内嵌时同样是死重,但它们是纯 Python、体积小,**本次不拆**(可作后续 `[cli]` extra)。
  - `sqlite-vec` 是记忆检索核心(持久向量搜索),虽有 `InMemoryVectorIndex` 回退但持久化语义依赖它,**留在核心依赖**。
  - 发布到 PyPI —— 仍维持 `git+url` 分发约定,不在本次范围。

## 验证标准

1. 全新 venv 中 `pip install 'ladym @ git+https://github.com/ProjAnvil/LadyM.git@v0.2.0'`(不带 extra)后:
   - `from ladym import Engine; eng = Engine(); eng.recall("x")` 成功,**且** `pip list` 中**无** `tree-sitter` / `tree-sitter-language-pack`。
   - 此时调 `eng.index_code("./src")` → 抛出带 `[codeindex]` 引导消息的 `ImportError`。
2. 同一 venv `pip install 'ladym[codeindex] @ git+...'` 后,`eng.index_code("./src")` 正常工作。
3. `pip install -e '.[dev]'` 后全量测试通过(含代码索引用例)。
4. `pip install -e '.[all]'` 行为与改动前一致。
