# LongMemEval Benchmark Harness — 设计

- **日期**: 2026-08-02
- **状态**: 草案,待 review
- **相关**: 无前置代码依赖。消费 ladyM 公共 API(`Engine` / `record_event` / `recall` / `consolidate`),复用 [[model-routing-injection]] 的 `ModelRouting` 注入 answer/consolidate LLM,复用 Secret Store(`[[secret-store]]`)取 GPT-4o judge key。从 `main` 独立分出,不改核心包。

## 背景与动机

ladyM 缺一个**可对外公布的、标准化的记忆能力评测**。当前只有 `scenarios/`(S01–S09 手写场景)和 `tests/`(单元/集成测试),无法回答"ladyM 的长期记忆到底有多好"这个产品级问题,也无法与 mem0(公开报 LongMemEval 94.4%)等竞品横向比较。

**LongMemEval**(ICLR 2025,xiaowu0162)是目前长期对话记忆的事实标准 benchmark:500 题、5 类能力(信息抽取 / 多会话推理 / 知识更新 / 时序推理 / 弃答),自带 GPT-4o 自动判分与 turn/session 两级**检索 recall** 指标,脚本现成。其"知识更新"与"弃答"两类正好打中 ladyM 的 `consolidate`(ADD/UPDATE/DELETE)与注意力门控——是 ACT-R + decay 架构的主场。

### 目标(brainstorm 确认)

- **C(两者都做)**:先 Phase A(检索质量,引擎验证),再在其上叠 Phase B(端到端 QA,可上报分数)。ingest + recall 代码共用。
- **变体 A/B(raw vs consolidated)**:同一 ingest+recall 跑两遍,量化"ladyM 的 consolidate 带来多少检索增益"。raw 变体离线、确定性、低成本;consolidated 变体走 LLM 写路径,测全管线。

### 非目标

- 不做 LoCoMo(留作第二轮)。
- 不做 `M` 难度档(~500 sessions/instance,成本高),首版只 `oracle` + `S`。
- 不内建进核心包(`ladym bench` 子命令留作后续,见 [[future-ladym-bench-subcommand]])。

## 解决思路

在 `benchmarks/longmemeval/` 下建一个**结构化、带缓存**的独立 harness:ingest 把每个 instance 的历史灌进**独立 workspace 的 LadyM DB**(隔离、可缓存),`recall()` 产出检索日志与答案,evaluation 复用官方 vendored 脚本判分。raw/consolidated 两个变体共享 ingest+recall 代码,仅在 ingest 阶段是否跑 `consolidate()` 上分叉。

## 数据契约(上游 → ladyM)

**数据源**:HuggingFace `xiaowu0162/longmemeval-cleaned`,三个 JSON 各 500 条:
- `longmemeval_oracle.json` — 仅证据 session(检索上界,最小)
- `longmemeval_s_cleaned.json` — ~115k tokens / ~40 sessions(主档,适配 128k context)
- `longmemeval_m_cleaned.json` — ~500 sessions(超长上下文,首版不测)

**每条 instance 字段**:
```
question_id        str   # 末尾 "_abs" = 弃答题
question_type      str   # single-session-user | single-session-assistant | single-session-preference
                         # | temporal-reasoning | knowledge-update | multi-session
question / answer  str
question_date      str
haystack_session_ids  list[str]     # 历史 session id(oracle/S 已按时间排序)
haystack_dates       list           # 对应时间戳
haystack_sessions    list[list[dict]]  # 每段 session 的轮次:{role, content, has_answer?}
answer_session_ids   list[str]     # GT 证据 session(检索评测用)
```

**预测产物格式**(对齐官方脚本,零改动复用):
- QA:`hypothesis.jsonl`,每行 `{question_id, hypothesis}`。
- 检索:`retrieval.jsonl`,格式需匹配 vendored `print_retrieval_metrics.py` 的输入契约(实现时读该脚本确认确切 schema,见 §风险)。

## 设计

### 总览

```
download → ingest (per instance, cached by variant)
        → [Phase A] run_retrieval → retrieval.jsonl → 检索 metrics
        → [Phase B] run_qa        → hypothesis.jsonl → GPT-4o judge → QA metrics
        → evaluate → scores.md (raw vs consolidated 对比表)
```

### 文件布局

```
benchmarks/longmemeval/
├── README.md                  # 如何运行
├── requirements-lite.txt      # openai(judge), requests, ladym(editable)
├── config.py                  # BenchConfig dataclass
├── download_data.py           # HuggingFace → .cache/data/(+hash 校验)
├── ingest.py                  # JSON → 每 instance 一个 LadyM DB
├── run_retrieval.py           # recall() → retrieval.jsonl       (Phase A)
├── run_qa.py                  # recall() + answer-LLM → hypothesis.jsonl  (Phase B)
├── evaluate.py                # 包 vendored 脚本 → scores.md
└── upstream_eval/             # VENDORED,pinned(每个文件 header 注明 commit SHA)
    ├── evaluate_qa.py
    ├── print_qa_metrics.py
    └── print_retrieval_metrics.py

benchmarks/.cache/             # 全部 gitignore
├── data/   {longmemeval_oracle.json, longmemeval_s_cleaned.json}
├── db/{difficulty}/{variant}/<instance_id>.db
└── results/{difficulty}/{variant}/{retrieval,hypothesis}.jsonl + scores.md
```

### Part A —— `config.py`(配置)

```python
@dataclass
class BenchConfig:
    difficulty: Literal["oracle", "s", "m"] = "s"
    variant: Literal["raw", "consolidated"] = "raw"
    limit: int | None = None        # dev 子集(None = 全 500)
    top_k: int = 10
    base_dir: Path = Path("benchmarks/.cache")
    # 派生: data_dir / db_dir / results_dir 按 {difficulty}/{variant} 分层
    # answer-LLM + consolidate-LLM:走 ladyM Config / ModelRouting 注入
    # judge:OPENAI_API_KEY(GPT-4o,benchmark 强制)从 Secret Store 取
```

CLI 透传:`--difficulty`、`--variant`、`--limit`、`--top-k`、`--force-ingest`。

### Part B —— `download_data.py`(数据获取)

- 从 HuggingFace `resolve/main/` 下三个 JSON 到 `.cache/data/`。
- **校验**:比对已知文件大小/SHA(写在脚本常量里),不符即报错退出——防止上游静默更新导致分数不可比。
- 幂等:已存在且校验通过则跳过。

### Part C —— `ingest.py`(写入路径,核心)

每个 instance:

1. 开一个新 `Engine`,`Config(workspace=f"lme-{question_id}", db_path=db_dir/"{question_id}.db")`。
2. 按 `haystack_dates` 时间序遍历 `haystack_sessions`,每轮:
   ```python
   eng.record_event(
       agent=turn["role"],               # "user" | "assistant"
       action=turn["content"],
       metadata={
           "session_id": sid, "date": date,
           "turn_idx": i, "has_answer": turn.get("has_answer", False),
       },
   )
   ```
   - 用 `record_event`(**绕过 attention gate**,必持久化)——见 S02 场景约定。
   - **turn 级粒度**(原生 episodic);`metadata.session_id` 让检索结果能映射回 session,算 session-level recall。
3. `variant == "consolidated"` 时:ingest 完跑 `eng.consolidate()`(若 episode 足够,可选 `eng.extract_mental_models()`)——LLM 写路径,把 episodic 提升为 semantic fact、做 UPDATE/DELETE、建 link。
4. 关闭。DB 落在 cache 路径,**重跑时按"DB 存在 + memory 计数符合预期"跳过**;`--force-ingest` 重建。

**隔离原则**:500 个 instance 各自独立 workspace + DB 文件,互不污染(每个 instance 是不同用户的对话历史)。

### Part D —— `run_retrieval.py`(Phase A,读路径)

每题:
```python
resp = eng.recall(question, top_k=cfg.top_k)
recalled_session_ids = {m.metadata["session_id"] for m in resp.results}
# 写 retrieval.jsonl(格式对齐 vendored print_retrieval_metrics.py)
```
- **raw 变体无需 LLM**——固定 embedding 下完全离线、确定性,适合回归。
- 检索指标(官方脚本):turn-level recall(是否召回到 `has_answer=True` 的轮)、session-level recall(`answer_session_ids` 是否落在召回 session 里)。弃答题(`_abs`)跳过检索评测。

### Part E —— `run_qa.py`(Phase B,端到端)

每题:
1. `eng.recall(question, top_k=cfg.top_k)` → 取召回 memory 的 content + speaker + date 拼 RAG context。
2. 喂 answer-LLM(ladyM config-driven provider,`ModelRouting` 注入;与 consolidate 共用同一 provider 配置)生成答案。
3. 写 `hypothesis.jsonl: {question_id, hypothesis}`。
4. **弃答题**(`_abs`):若 recall 最高分 < 阈值 → 指示 LLM 回"我不知道"——测注意力门控/召回质量。

### Part F —— `evaluate.py`(判分聚合)

- **QA**:调 vendored `evaluate_qa.py`(GPT-4o judge)→ `.log`,再 `print_qa_metrics.py` → 按 `question_type` 分桶准确率。
- **检索**:调 vendored `print_retrieval_metrics.py` → turn/session recall。
- **聚合** `scores.md`:行 = `question_type`(含 overall),列 = `variant × {retrieval-turn, retrieval-session, QA-accuracy}`。raw 与 consolidated 并排,直接读出"consolidate 增益"。

### 错误处理

- **per-instance 容错**:ingest/run 全程 try/except,失败记进 `ingest_report.json` / `run_report.json`,继续——1 个坏 instance 不杀掉 500 题的整跑。
- **consolidate/answer-LLM 失败**:ladyM 已内建回退到 heuristic + log;在 report 里标出哪些 instance 走了 fallback。
- **judge 限流**:openai 调用 retry + backoff,记录 partial。
- **缓存一致性**:DB 存在但 memory 计数不符(上次中断)→ 视为未完成,重建。

## 测试

`tests/benchmarks/`(快、无 API key):
- **ingest 单测**:合成 2-session fixture → 断言 episodic memory 数 = 轮次数、metadata 字段齐全(session_id/date/turn_idx)、`has_answer` 透传。
- **retrieval 映射单测**:召回结果 → `recalled_session_ids` 集合逻辑正确。
- **config 单测**:`difficulty`/`variant` 路由派生 `db_dir`/`results_dir` 正确。
- **smoke(可选,需数据)**:`--limit 2` 跑 `oracle`,断言产出格式良好的 jsonl。

vendored 脚本:仅断言可 import + SHA 已记录;不测上游代码。

## 依赖

- `requirements-lite.txt`:`openai`(judge)、`requests`(下载)、`ladym`(editable)。
- answer-LLM + consolidate-LLM:ladyM 既有 provider config(DeepSeek/OpenAI 等),key 走 Secret Store。
- judge:`OPENAI_API_KEY`(GPT-4o,benchmark 强制),Secret Store。

## 风险与开放项

1. **检索日志 schema 待确认**:vendored `print_retrieval_metrics.py` 期望特定 log 格式(字段名/结构)。实现时先读该脚本,让 `run_retrieval.py` 产出对齐格式;若官方格式依赖其自有 retrieval 管线的中间产物(而非简单 jsonl),可能需要写一个适配层把 ladyM 的召回结果转成官方期望的结构。**这是本设计最大的不确定点**,留到实现首步验证。
2. **consolidate 非确定性**:LLM 写路径跨运行结果可能略变。接受——这正是被测对象;`scores.md` 标注运行时间/embedding provider 版本以便复现。
3. **判分成本**:500 题 × GPT-4o judge,有 API 费用。`--limit` 控制开发期开销;`oracle` 档先跑全量做 sanity。
4. **后续**:验证可行后,可包成 `ladym bench longmemeval` 子命令(Approach C),随包发布。首版先验证价值。
