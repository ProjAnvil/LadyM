# Attention Gate:noise/dup 前置 + LLM 语义层(配置无关)

> 日期:2026-07-22
> 状态:设计已确认,待写实现计划
> 相关:SPEC §2.7(`docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md`)、`scenarios/S09-attention-gate.md`、`scenarios/_conventions.md §8.1`

## 背景与动机

当前 attention gate(`src/ladym/operations/attention.py`)是"两模式互斥":

- `provider="none"` → heuristic 走 `too short / noise / recent-duplicate` 三条
- 配了 LLM → 完全交给 `_llm_gate`(`complete_structured`),heuristic 三条**全跳过**(`attention.py:71-73`)

后果:真实部署配了 LLM 时,gate 行为不可预测——"hi" 存不存由 LLM 当场决定,heuristic 的噪声防护形同虚设。
这导致 `S09` scenario 的 drop 矩阵断言无法在两种配置下都成立(e2e 必须能**配置无关地 pass**:无论 `provider=none` 还是配了真实 LLM,scenario 都该通过)。

根因不在 gate 结构(两模式互斥是合理的设计),而在两点:

1. heuristic 里 `too short` 把"长度"当成噪声代理,但 "hi" 是合法自然语言——**太短 ≠ 没价值**。
2. LLM gate 的 system prompt(`attention.py:117-122`)只说"判断值不值得存",**没编码任何具体判断原则**,LLM 自由发挥、行为不可测。

## 设计

**核心:把 gate 拆成"确定性硬事实层"和"语义判断层"。前者(noise/dup)前置、代码层确定性;后者交 LLM 用自然语言 prompt 判断。两模式按同一套规则精神走,scenario 配置无关。**

### gate 新逻辑

```python
def attention_gate(content: str, *, engine, layer: Layer) -> GateDecision:
    cfg = engine.config
    if layer == Layer.WORKING:
        return GateDecision(action="pass", reason="working memory never gated")

    stripped = content.strip()

    # ----- heuristic 前置:确定性硬规则,LLM 判不了/不需要 LLM -----
    # (1) noise:纯噪声词表命中
    tokens = {w.lower() for w in stripped.split()}
    noise = _BUILTIN_NOISE | set(cfg.attention.noise_words)
    if tokens and tokens <= noise:
        return GateDecision(action="drop", reason="noise")

    # (2) recent-duplicate:dedup_window_s 内与某 L1 同 content hash
    if _is_recent_duplicate(engine, content, cfg):
        return GateDecision(action="drop", reason="recent duplicate")

    # ----- 过了 heuristic:语义判断层 -----
    agent = engine._get_agent("attention_gate")
    if agent is not None:
        return _llm_gate(agent, content)
    return GateDecision(action="pass")
```

**关键变化:**

- **移除 `too short` 规则**(原 `len(stripped) < min_chars → drop`)。"hi" 不再被长度拦截。
- **noise + recent-duplicate 前置到 LLM 之前**:它们是确定性硬事实——
  - `noise` 是词表匹配,无需语义;
  - `recent-duplicate` 是 hash 级精确比对,LLM 看不到近期记录也判不了。
- **太短但语义可能有效的内容(如 "hi")交给 LLM**:配了 LLM 就让 LLM 判断语义价值;没配就直接 pass。
- 副产品:LLM 模式下,前置 noise/dup 省掉了对纯噪声/重复内容的 LLM 调用(省 token)。

### LLM system prompt 重写

当前(`attention.py:117-122`):

> "Decide if the user content is worth storing long-term. Reply JSON {action, content?, reason}..."

改为编码具体判断原则(自然语言),**不硬编码长度**:

> 你是 attention gate,判断内容是否值得作为长期记忆存储。
> - `pass`:有信息量的内容——事实、决策、事件、偏好、代码知识等。
> - `drop`:无信息量的内容——打招呼(hi/hey)、确认词(ok/sure)、寒暄、碎片、纯情绪等。
> - `rewrite`:内容有价值但措辞混乱,返回清理后的文本。
> 返回 JSON `{action, content?, reason}`。

prompt 描述的是 heuristic **想表达但代码表达不了**的"语义噪声"判断。它与 heuristic 的 noise 词表**精神一致**(都过滤无信息量内容),但覆盖词表判不出的语义噪声(如 "hi there"、"嗯好的")。

### `min_chars` 配置:删除

`too short` 规则移除后,`attention.min_chars`(`config.py:144` 的 `min_chars: int = 8`)是该规则专属配置——规则删了就是死字段,**一并删除**(字段定义、`test_config_load.py:75,85` 的加载断言、SPEC config 表的 min_chars 行)。当前 `ladym.toml` 未使用它,删除无影响。(若外部 toml 残留 `min_chars`,属过时配置;`from_file` 对未知字段的容忍度是独立健壮性问题,不影响本结论。)

## 配置无关性验证(scenario 两头通)

| 内容 | `provider=none` | 配了 LLM | S09 断言 |
|------|-----------------|----------|----------|
| `"hi"` | heuristic 过 → **pass** | heuristic 过 → LLM 判(倾向 drop) | none=pass / LLM=gate 被调用(软断言) |
| `"lol ok test asdf foo"` | noise → **drop** | noise 前置 → **drop** | 两头确定性 drop ✅ |
| 近期重复 content | dup → **drop** | dup 前置 → **drop** | 两头确定性 drop ✅ |
| 正常长内容 | **pass** | **pass**(LLM 通常 pass) | 两头 pass ✅ |

noise/dup 两头确定性 drop;"hi" 两头都"过测试"(none 是 pass、LLM 是 gate 被调用)。→ **S09 配置无关地 pass**,不再需要 `export LADYM_LLM_PROVIDER=none` 关 MCP LLM。

## 连带改动

### 1. 代码
- `src/ladym/operations/attention.py`:移除 `too short` 分支;noise + recent-duplicate 移到 `_get_agent` / `_llm_gate` 调用之前;重写 `_llm_gate` 的 system prompt(如上)。(可选)`recent-duplicate` 抽出 `_is_recent_duplicate` 小函数,便于单测。
- `src/ladym/config.py:144`:删除 `min_chars: int = 8` 字段(too short 专属,规则删了即死字段)。

### 2. 单测 `tests/unit/test_attention_gate.py`
- `test_gate_drops_too_short` → 改为 `test_gate_passes_short_when_no_llm`(none 模式 "hi" pass)。
- `test_remember_drop_returns_unpersisted_memory`:不再用 "hi" 测 drop,改用 noise 或 dup 内容触发 drop。
- 现有 `test_gate_drops_noise` / `test_gate_drops_recent_duplicate` / LLM 三个测试**不受影响**(验证通过)。
- 新增:`test_llm_gate_receives_short_content`——配了 LLM 时 "hi" 进了 LLM(用 `FakeLLMProvider` 的 `called[]` 断言),证明"hi" 交给语义层。
- `tests/unit/test_config_load.py:75,85`:删除 `[attention] min_chars = 16` 的 toml 行与 `assert cfg.attention.min_chars == 16`(字段已删)。

### 3. CLI / MCP 测试
- `tests/unit/test_cli.py`:`test_cli_remember_drop_too_short`(load-bearing,证明 gate 接入 CLI)→ 改用 noise 内容证明 gate 接入。
- `tests/unit/test_mcp_server.py`:检查有无 "hi"/too short 用例,同步改。
- 验证命令:`uv run pytest tests/unit/test_attention_gate.py tests/unit/test_cli.py tests/unit/test_mcp_server.py -q`

### 4. SPEC §2.7(`2026-07-21-providers-config-control-plane-design.md:456-459`)
- 移除 `too short` 规则描述。
- `noise` + `recent-duplicate` 标为"heuristic 前置(确定性,LLM 调用前)"。
- `LLM mode` 标为"语义判断层(prompt 编码判断原则)"。

### 5. scenario
- `scenarios/S09-attention-gate.md`:drop 矩阵重定义——noise/dup 两头确定性 drop;"hi" 改为**演示 LLM 增值**(none=pass / LLM=判);删除"配 LLM 需 `export LADYM_LLM_PROVIDER=none`"的 Given。
- `scenarios/_conventions.md §8.1`:更新 gate 行为描述;清掉"export none 关 MCP LLM"的误导建议。

## 不在本次范围
- **LLM 调用异常(超时/key 失效)的降级**——保持现状(抛异常)。作为独立 follow-up。
- **LLM 不保证 100% 遵守 prompt**——scenario 断言挑明显的 case(noise/dup 确定性;"hi" 软断言)。
- **MCP server 行为验证**——代码改完需重启 MCP server 加载新代码后,再跑 S09 MCP 分支确认。
