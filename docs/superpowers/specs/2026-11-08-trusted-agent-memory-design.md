# LadyM Trusted Agent Memory: 完整架构规格

日期：2026-11-08
状态：R1 已实施并复审；R2–R4 已实施，T1–T5 独立审查通过，T6 验证完成后待 controller 全分支审查
适用仓库：LadyM，基线 `2c41b06`

## 1. 意图与问题

LadyM 已有统一 Engine、L0–L6、SQLite/Postgres、事件捕获、代码索引、
两级检索和 System 2。此次不是重写，而是建立明确的知识生命周期契约。

核心原则：

> 原始记录提供证据；类型化知识保存结论；派生视图负责导航；
> 小型入口负责提醒；后台维护让它们保持一致。

研究依据：[Anthropic, The components of agent memory](https://www.anthropic.com/research/components-of-agent-memory)。
文章中任务收益并不能直接归因于数据库、分层数量或 wiki；模型、反馈、
任务类型、搜索能力和后台计算量共同影响结果。LadyM 的改进必须通过自己的测试与评估证明。

## 2. 路线与本轮发布边界

此文保留 R1 发布时的历史范围；后续 R2–R4 已按
[独立规格](2026-11-08-memory-learning-and-maintenance-design.md)实施。
实际验收、恢复策略及限制见 [memory maintenance](../../memory-maintenance.md)。

| 发布 | 内容 | 本轮状态 |
|---|---|---|
| R1 | 一致检索、类型化原子整合、证据保留、身份可选规范化、来源输出、受控注入 | 已实施并复审 |
| R2 | 可操作的证据/断言/验证闭环、反馈与失效传播 | 已实施；caller-attested，不执行验证命令 |
| R3 | 小规模主题视图、词法/向量/结构混合检索、启动知识入口 | 显式接口已实施；不宣称未测量 QA 收益 |
| R4 | 预算化增量维护、基于变化的调度与质量/成本评估 | opt-in 已实施；离线来源/成本观察及真实 PG gates |

R1 为后续功能提供真实证据与稳定生命周期，而不需要先构建新的 Agent 运行时。
本轮不实现执行任意命令的验证代理，不更换数据库，不新增 L7，不新增 UI、
全量 transcript 上传、外部服务、ANN、队列或第三方依赖。

## 3. 备选方案与决策

### A. 继续增加层和图链接

改动表面较小，但错误知识、陈旧向量、候选饥饿和来源丢失会继续传播。
拒绝：解决不了已验证的根因。

### B. 更换为向量数据库/图数据库并拆微服务

能改善部分规模问题，但增加部署、迁移和一致性成本，无法证明知识正确。
拒绝：目前没有生产规模测量支持这笔投入。

### C. 保留模块化单体，强化共享契约

在 Store、operations、Engine、各输出面定义相同规则，并增量演化派生视图。
选择 C：保持 local-first、零 cgo、统一行为和离线可测性。

## 4. 全局约束

- 保留现有 Go 版本、零 cgo、现有依赖和个人/enterprise 两种构建。
- 读路径不得构造或调用生成式 LLM；查询 embedding 仍允许按已配置 provider 调用。
- 新契约同时支持 SQLite 和 PostgreSQL；不得用 SQLite 专属实现冒充通用 Store。
- 公共现有签名原则上保留；新增能力必须有统一 helper/方法，不在各前端复制规则。
- 空 layers/types 表示不过滤；空 workspace 在 Engine 层解析为当前默认，不能意外全库查询。
- 检索结果只含当前 workspace 的有效知识；已退休项不回流。
- 存储、刷新和模型错误必须显式传播；不允许成功形状的空结果掩盖错误。
- 不自动合并主分支，不推送，不修改现有用户数据库或用户配置。
- 修改保持向后兼容；需要改变旧行为的地方必须有测试和迁移说明。
- 所有资源、来源和验证数据视为参考数据，不得提升为未经检查的执行指令。
- 不承诺未测量的 QA 提升、token 节省或生产 p95。

## 5. R1 功能规格

### R1-S: 范围正确、跨实例一致的检索

新增可返回错误的范围检索接口。输入为 query vector、topK 和过滤器
（workspace、layers、types、是否排除退休项）；过滤发生在 topK 截断之前。
原 `VectorSearch` 保留兼容，但所有 LadyM 内部语义检索迁移到新接口：
普通 recall、resource 写解析、recall_ops 读解析、整合和 playbook 查重。

同库多实例必须在下一次开始的搜索中看到已提交的插入、向量更新、删除。
“开始”指搜索的一致快照建立时；并发提交允许在下一次调用可见。
SQL WAL/advisory lock 不等于缓存一致性。

SQLite 保留惰性加载，使用持久化内容/向量变更 generation 或等价的、
能正确区分本连接与外部写入的版本机制。只读、Touch、统计不能强制 O(n) 重载；
裸 data_version 不足以区分其他连接的簿记，必须用 mutation-specific generation。
改变 embedding、删除、插入必须失效。刷新重建快照，不能仅 upsert 而遗留已删除向量。
读取版本和向量快照不得形成“加载旧数据却记住新版本”的竞态。

Postgres 范围检索使用带过滤的 SQL。为了离线/PG 行为一致，先保证
过滤后 exact top-k；若 HNSW 无法保证该契约，应使用确定性的 exact 查询，
并在文档中说明规模权衡。相似度相同按 ID 稳定排序。

旧 `VectorSearch` 无 error 返回的历史限制不得继续污染新接口。
旧接口失败需记录明确错误；所有内部调用新接口并传播错误。

topK <= 0 在底层返回明确输入错误或既有明确定义的空列表；Engine 先解析默认。
query vector 必须维度正确、元素有限；不能 panic、NaN 排序或悄悄越界。

### R1-R: 如实反射与有限扩展

Tier 1 与 Tier 2 使用同一个覆盖启发式。Tier 2 后对最终实际返回的结果重新检查：
未覆盖、数量不足或没有新增证据时允许 `reflected_sufficient=false`。
该字段只表达启发式充分性，不是事实置信度。

覆盖用完整 tokenizer token 集合，不用字符串子串：查询 `cat` 不得被
`concatenate` 判定覆盖。停用词沿用现有规则；空有效 token 查询保持既有定义。

增加确定性的扩展节点上限，默认 128 个新节点，包含所有图扩展和文件补充。
按稳定的锚点、邻边顺序扩展；达到预算停止扩展，已有结果仍返回，
不会借此伪造充分性。预算用于限制候选，不改变 topK 含义。

过滤适用于所有图/文件候选，包括退休、workspace、layers/types。
退休节点不能成为传播桥梁；版本链仅以有效的新版本作为结果。
文件补充只做一次按路径的 workspace 查询/映射，不逐符号扫描整个库。

### R1-C: 类型化、原子、可重试的整合

自动整合可修改的目标只有 L2 `fact`/`note`。
代码 symbol/file、resource、L5/L6、playbook 不得成为普通事实更新目标。
已退休事实也不能成为目标或造成误判 NOOP。

相似度不是矛盾证明。离线分类只做精确内容 hash 的 NOOP，否则 ADD；
不得仅因为相似就 UPDATE/DELETE。这会增加离线事实数量，是明确的可靠性取舍。

保留现有 `LLMClassifier` 注入签名。引擎内置结构化分类器必须用
ADD/UPDATE/DELETE/NOOP enum，禁止未知 action 被记为完成。
对于旧分类器 UPDATE/DELETE/NOOP，只能在存在有效事实候选时应用；
空候选或非法 verdict 返回显式错误，不 stamping。
多候选目标选择沿用第一项以保持公共兼容，但 prompt 明确该第一项是唯一可变目标；
R2 将引入带目标 ID 的 richer 决策，不伪称 R1 已实现目标推理。

新增存储级原子 mutation/batch 契约：

1. 检查源事件仍存在、同 workspace、未完成，并 CAS 检查分类时的完整知识快照；
2. 对涉及的目标检查内容、UpdatedAt、知识 metadata 或等价 revision；
3. 写新知识、退休旧知识、关闭关联边、投影来源与资源边；
4. 写事件完成标记；
5. 一个数据库事务提交，否则所有步骤回滚。

不在事务内调用 embedding/LLM。乐观冲突返回类型化 error，事件保持待处理，
下一周期可重试；不进行无上限自动重试。
源事件和目标的 CAS 都比较 content、summary、tags、knowledge metadata、
source、workspace、layer/type、ContentHash、UpdatedAt；
LastAccessAt/AccessCount/Activation 等读取簿记不使分类失效。
分类期间源内容被编辑必须冲突，而不是用旧结论消费新内容。
并发两个整合请求只能有一个消费事件，不得产生两个活跃派生事实。
worker 锁不能替代该契约，因为 API/MCP 也能调用整合。

新派生事实标为 `knowledge_status=inferred`，保留 `source_episode`。
UPDATE 必须保留旧的全部有效来源：`source_episodes` 为去重 ID 列表，
`produced_from` 投影到每个仍存在、同 workspace 的来源。
旧来源已丢失时不构造悬挂边，记录可识别的缺失证据状态。
缺失状态以分类前来源快照为准：保留已有缺失 ID，replacement 原子标
`evidence_missing=true`；当时仍存在的来源须 guard，并发删除仍返回 conflict。
原子提交必须覆盖这些投影。

### R1-E: 证据保留与删除的区别

自动 Decay 不删除任何仍被有效非退休知识的 `produced_from` 边引用的 L1。
为兼容旧库，还检查有效知识的 `source_episode` / `source_episodes` metadata。
直接依赖包括 `produced_from`（派生→来源）、`abstracts`（模型→被抽象知识）、
以及这两个 legacy metadata 字段。新 playbook 必须投影全部源事件；
L6 的预测来源在 R1 不新增证据语义。
L5 和 playbook 的新建/合并/更新也必须 guard 来源并在同一个 batch 发布
派生物、全部来源边和相关退休标记，不能先保存派生物后补边。
仅退休知识的引用不阻止衰减。证据检查须避免每事件全库扫描。
dryRun 与真实执行使用相同选择；报告增加保护数，输出兼容旧字段。
删除前在数据库事务中再次检查引用和最后访问时间。来源消费、来源边创建和
自动删除使用一致的源行锁（Postgres）/写事务（SQLite）。
先提交引用时不能自动删除；先提交删除时整合 guard 必须失败。
报告按实际成功删除计数，dryRun 表达其快照而非承诺未来并发结果。

显式 Forget/Delete 仍删除指定记录；它不被自动保留策略阻止。
同事务使直接派生知识标为 `knowledge_status=stale`、
`evidence_missing=true`，保留已经存在的缺失来源 ID 供审计；
graph-only `produced_from` / `abstracts` 来源在删除级联前写入现有
`source_episodes` provenance metadata，并保留原 metadata 和已有来源。
stale 知识不得作为充分回答依据或自动注入。
原始删除成功与依赖标记必须原子；错误不得部分成功。
R1 只做直接依赖失效，R2 定义完整传递传播。

### R1-I: 身份与状态的诚实表达

新增 opt-in `capture.identity_mode`，值为 `legacy` 或 `canonical`，
默认 `legacy`，非法配置启动报错。保留旧公共 helper 的 basename 行为。

canonical 模式：

- workspace 使用清理后的绝对项目根路径的稳定散列身份；
- 项目根优先 git 根；非 git 使用清理后的绝对 cwd；
- 不解析路径外部内容，不自动移动已有记忆，不擅自跟随任意 transcript 路径；
- 文件资源用项目根相对路径；根外路径用清理后的绝对路径；
- 别名不能无条件把两个 `main.go` 合并成一个实体；
- 工具 cwd 未提供时使用 CLI 的实际 cwd，显式 `-w` 优先；
- 路径处理 CJK、空格、尾部分隔符，不依赖 shell 拼接命令。

旧库切换需先备份、用户显式配置；无自动旧 workspace 合并。
文档说明两种模式不可无损推断映射，同库同名项目建议 canonical。
canonical hook 资源标 `resource_identity=path`，使用 workspace+规范路径的
确定性 ID、区分大小写，不参加 fuzzy alias、向量实体合并或 worker LLM alias merge。
path entity 在 fuzzy topK 截断之前排除；支持的 embedding dimension/tokenizer
迁移保持其 structural-only，并清除旧 path vectors，不调用 embedder。
原普通资源解析也不得把 path entity 用作 fuzzy 候选；read 只能精确定位 path，
未知 path 可走明确 recall fallback，不能返回另一个路径实体。

hook `outcome=success/fail` 保留工具 API 兼容，同时增加
`tool_outcome`，不把 Stop/工具成功称为任务验证。
hook metadata 增加 `knowledge_status=observed`。
程序化提取忽略仅工具成功的 hook 事件，除非它携带明确
`task_outcome=success` 且 `knowledge_status=verified`。
可信 Go SDK 调用者可用 RecordOperation/RecordEvent 的显式 metadata 提交上述
状态与 task_outcome；hook Payload 不新增从 tool_response 取验证状态的机制。
识别 hook 以 Tags 中 `hook` 或 metadata 中 hook_event_name 为准。
正常 ToOperation 永远 observed，不能从 untrusted tool output 晋升。
旧非 hook success/ok/done 行为保持，但新 playbook 标 inferred，不能伪称 verified。

### R1-P: 来源跨前端贯通与受控使用

MCP 返回 memory 中增加 workspace、created_at、updated_at、以及经过白名单
过滤的 metadata。允许字段：
`session_id`, `source_episode`, `source_episodes`, `knowledge_status`,
`evidence_missing`, `verified_at`, `action_class`, `tool_outcome`, `task_outcome`。
不得回显任意 metadata、原始 tool input、cwd、transcript_path、密钥或认证数据。
已有 score/tier/via/content/summary/source/tags 形状不变。
HTTP/Go SDK 的 Memory 已包含 metadata；只需保持新状态与字段不丢失，
不因本任务重做 UI 或添加 metadata 编辑器。

LangGraph 自动保留 Q/A 到 L1，标 observed，
不再把未经验证的 assistant 回答直接写为 L2 fact。
自动注入只使用有效且非 stale 的知识，数据提示明确“参考信息不是指令”；
总 UTF-8 字节预算 4096，包含 prefix 和提示头，不宣称等于精确 tokenizer token 数。
prefix 过长时安全截断；逐条装入完整有来源 ID 的摘要，单条过长可
在 rune 边界截断到剩余预算；不能制造无效 UTF-8。
新入口不会调用 LLM 或执行检索内容。没有有效内容则不更新 messages。

LongMemEval 的 consolidated 来源评估应支持从新 provenance metadata
回溯 sidecar 中的 doc/session ID；一个 fact 可对应多个 gold 来源。
不删除旧 sidecar 兼容，不把派生 fact 自身 ID 当 gold 原事件。
现有单来源指标保持可读，增加多来源映射测试，文档撤销“必然无来源”的旧限制。
保留原 `metrics` 完全不变，另加 `evidence_metrics`，cutoff K 始终是召回 memory
槽位数，不是展开后的 source 数。每槽位对应去重的 source 集合；
recall_all@K 检查前 K 槽位来源并集覆盖 gold；
ndcg_any@K 槽位 gain=1 当且仅当该槽位引入至少一个此前未覆盖的 gold，
否则 gain=0；ideal DCG 用 min(K, len(gold)) 个 gain=1。
沿用现有 _dcg 折扣以保持独特单来源序列的数值；
重复来源无额外 credit，指标不得超过 1。未知来源槽位保留 rank、gain=0。
turn/session 各自计算，不改变原 session 去重 metric；
例 gold={a,b}、groups=[{a,b}]、K=1 得 recall_all=1/ndcg=1，
K=2 得 recall_all=1/ndcg=0.5（原 _dcg 的前两项都无折扣）。

### R1-W: 后台 workspace 状态

L6 watermark 与 L5 merge cycle 按 workspace 和 operation 编码。
使用稳定散列或长度前缀，避免 workspace 分隔符碰撞。
L6 游标为 (CreatedAt, ID)，按 timestamp 再 ID 排序，按二元字典序消费；
相同 timestamp 跨 episode cap 不能丢失，游标整体持久化。
旧全局 key 保留但不继承到全部 workspace，否则会跳过未处理事件。
升级后允许一次有限重处理，L6 每次仍受现有 episode cap 约束。
文档解释为什么不直接复制旧水位；相同 workspace 跨重启保持进度。
L5 provider error 必须显式返回，不静默 continue。

## 6. R2: 可信学习规格（后续已实施）

### 6.1 数据契约

KnowledgeClaim 是可复用断言，不是所有日志行。字段：

| 字段 | 语义 |
|---|---|
| status | observed/user_asserted/inferred/verified/stale/rejected |
| evidence_refs | source ID、片段位置、内容指纹、来源类型 |
| scope | 项目、代码版本、环境、适用条件 |
| verification | 方法、时间、验证者、结果及证据 |
| counter_evidence | 与断言冲突的已定位来源 |
| revision | 用于 CAS 的单调知识修订 |

禁止仅靠模型自评 confidence 把 inferred 晋升 verified。
测试输出、明确用户确认、结构化环境观测可以证明特定范围内的结论。
源 transcript 中的 Agent 叙述不是天然 ground truth。

### 6.2 决策与反馈

提取器返回 target_id、候选断言、evidence_refs、decision enum 和理由。
无证据更新不得覆盖 verified 项；冲突可共存，直到验证或明确决议。
反馈引用具体 memory/revision，而不是只投票“有帮助”。
反馈流程：提出反例 → 定位原文/当前代码 → 生成候选修订 →
验证 → CAS 提交 → 失效所有依赖视图与入口。
阅读次数用于检索活跃度，不用于知识真实性。

### 6.3 来源存储与隐私

原始来源归档必须 opt-in、脱敏、访问隔离、明确容量与保留期。
证据 pin 延长保留不应阻止用户删除。
用户删除传播到派生 claims/views/bootstrap；不可恢复原文。
不根据 metadata 自动读取用户机器任意文件。

### 6.4 验收门

可复现地纠正错误断言；再次任务不重复错误；
来源变更使 dependent claim/view 失效；
无证据断言不能晋升 verified；
不增加读取路径 LLM 次数；显式删除覆盖传递依赖。

## 7. R3: 知识地图和按需加载（后续已实施）

### 7.1 主题视图

主题视图是派生物而非事实源，初期只试点认证、部署、存储等少量主题。
包含入口、关键流程、适用条件、决策理由、陷阱、符号/测试/claim 来源。
每条重要结论必须有 evidence/claim ID。
页面记录输入 revision 集合与生成器版本；来源变化即 stale。
一个 topic revision 一个 writer，通过 CAS 提交；无共享页面 last-write-wins。
单个来源的字面事实保留原文片段，跨来源综合明确标推断。

### 7.2 混合检索

词法信号用于符号、路径、错误码；向量用于概念；结构用于关系和来源。
在 workspace/权限内分别取候选，确定性合并、去重、排序，再执行预算。
不能让同库其他 workspace、退休项或预测意图挤占有效证据名额。
缺证据时返回 source lookup 提示；wiki 不能取代当前代码读取。

### 7.3 启动入口

入口保存少量已验证规则、已知陷阱和主题链接。
任务临时状态单独保留 TTL；预测不等于用户授权的目标。
启动入口预算实验从 512–1024 tokens 起步，必须使用实际模型 tokenizer
度量后确定，不把行数/字节数冒充 tokens。
删除/撤销/来源变更立即退出入口，不能靠下次 dreaming 修复。

### 7.4 收益门

固定模型、embedding、代码版本、总 tokens 对比：
无记忆 / 原文搜索 / 当前 R1 / R2+主题视图。
只有跨文件任务显著受益且成本可控才扩大 topic 覆盖。
单文档精确查找不被强制绕道 wiki。

## 8. R4: 增量维护与预算（后续已实施，opt-in）

持久化 jobs 标识 workspace、operation、input revisions、attempt 和 budget。
触发源：新事件、来源变化、反例反馈、verified 项过期。
按错误影响、变化概率、需求排序；热度不等于真值。

每周期预算同时包含候选数、LLM tokens、墙钟时间和并发；
任务检查点提交与派生物提交保持一致。
负载大时延迟低优先级工作，不能丢弃用户删除或撤销。
复用 stored embedding；内容/模型指纹变化才重嵌入。
provider/model/dim/tokenizer 指纹迁移需可恢复 checkpoint 与回滚说明。

指标：任务成功率、错误知识使用率、来源可追溯率、
重复错误率、校正延迟、read p95、维护 tokens、图/视图膨胀比。
没有量测不得引入新的数据库、ANN、队列或水平拆分。

## 9. R1 兼容、迁移与回滚

1. 新 SQLite schema/版本机制幂等添加，保留所有现存 memories/edges。
2. 新 PG SQL/事务契约不依赖个人版 SQLite，不泄漏到 enterprise binary。
3. 白名单 metadata 为加法输出；旧客户端忽略未知字段仍工作。
4. legacy 身份默认保持；canonical 用户显式启用，文档提供回退旧模式。
5. 离线相似 UPDATE 改为 ADD；旧已有版本链不自动改写或删除。
6. 未携带新状态的旧记录按 legacy 可检索，但不自动声称 verified。
7. 自动衰减保护有效证据；可能增加保存量，后续归档设计解决容量。
8. 新 workspace 水位不复制旧全局水位；有限重处理比漏处理安全。
9. 降级二进制忽略加法 metadata/schema；原子变更已退休知识不能靠降级复活。
10. 本任务不修改任何用户的实际配置或历史数据库；只测试临时数据库。

## 10. R1 验收清单

- A01：两 Store/进程，reader warmed 后 writer insert/update/delete 下一搜索可见。
- A02：其他 workspace/type 有至少 4 个更高相似项，目标 topK=1 仍返回目标。
- A03：非法维度、NaN、Infinity、DB 关闭均显式 error，不 panic/空成功。
- A04：Tier 2 无新增证据仍未覆盖，充分性 false；token 子串不误判。
- A05：超过 128 个关联节点时预算生效；退休节点/其他 workspace 不被扩展。
- A06：事件与 code/resource 同向量，整合不修改 code/resource。
- A07：离线相似但不等价事实 ADD；精确 hash NOOP；退休匹配不阻止新 ADD。
- A08：非法 verdict/空 UPDATE target 保持事件 pending 并报错。
- A09：事务中间故障无新知识/退休/来源边/完成 stamp 残留。
- A10：并发整合同一事件仅一个成功，另一个 conflict/pending-safe，不重复活跃事实。
- A10b：分类期间修改源内容；CAS 冲突，新内容仍 pending，不提交旧推断。
- A11：有效来源关联/legacy metadata 保护 Decay；退休引用不保护；dryRun 一致。
- A11b：引用提交与自动删除两种交错；不得删除先提交的有效证据。
- A12：显式删除来源原子标记直接派生 stale，不能继续注入或判充分。
- A13：canonical 区分同名项目和不同目录 main.go；legacy 保持原身份。
- A13b：path 的 embedding 完全相同且 alias LLM 总确认，也不能合并不同路径。
- A14：hook tool success/Stop 不生成 verified 技能；明确验证任务可进入。
- A15：MCP 白名单来源字段贯通，不暴露敏感或未知 metadata。
- A16：Q/A 落 L1 observed；注入 <=4096 UTF-8 bytes，CJK 边界安全，无 stale。
- A17：同库 workspace A/B 的 L6/L5 进度互不影响；重启持久化。
- A17b：相同 timestamp 超过 L6MaxEpisodes，跨周期和重启无遗漏。
- A18：LongMemEval fact 回溯多来源 sidecar，旧 raw/单来源路径兼容。
- A19：默认 `go test ./...` 通过；关键并发包 race 通过。
- A20：enterprise 构建通过且不包含 modernc.org/sqlite；PG 测试有真实证据或明确未运行。

## 11. 风险与停止条件

最大风险为范围检索的 exact/ANN 语义、SQL 快照与缓存版本竞态、
事务 CAS 和旧错误路径测试的耦合、身份切换及来源隐私。
实现应先按存储契约、知识生命周期、捕获/输出、使用/评估分解。

R1 完成的停止条件：A01–A20 有对应证据，全部发布功能持久化、
文档明确兼容行为、审计无未解决的重要缺陷。
PG live 环境不可用时不得宣称验证通过；先尝试已有本地环境，
不擅自操作共享生产库，不编造通过记录。
R2–R4 不因本次 R1 完成而自动获准修改实际用户记忆或扩容服务。

## 12. 独立 spec 审计处置

首轮审计指出 7 个阻塞项：源 CAS、衰减竞态、依赖关系不完整、
canonical fuzzy 合并、hook 验证来源、水位同时间戳、评估 rank 语义。
均已通过本规格的明确契约和 A10b/A11b/A13b/A17b 补齐。
计划审计必须再检查这些已决事项是否有具体实现任务和数值测试。

## 13. R1 交付与验证记录

分支 `feat/trusted-agent-memory`；基线 `2c41b06`。
四个实施单元及独立审查完成；全分支审查的四项重要发现已在
`948d2fe` 修复并通过定向复审：路径候选前置过滤/迁移、缺失来源更新、
图来源删除后 ID 保留、分类器 action enum。

最终源码版本 `948d2fe` 的新验证：

| 验证 | 结果 |
|---|---|
| `CGO_ENABLED=0 go test -count=1 ./...` | 个人版全部包通过 |
| `go test -race -count=1 ./storage ./operations ./engine ./hook ./langgraph` | 五个相关并发包通过 |
| `CGO_ENABLED=0 go test -short -tags eval ./eval` | hook 评估回归通过 |
| `CGO_ENABLED=0 go build -tags enterprise ./cmd/ladym ./cmd/ladymconsole` | 两个企业版生产入口通过 |
| 企业版依赖检查 | 不包含 SQLite 或嵌入式 console |
| 当前 worktree 的 `pytest benchmarks/tests` | 49/49 通过 |

验收 A01–A18/A10b/A11b/A13b/A17b 已有对应回归测试和独立审查；
A19 通过。A20 的企业版构建/依赖隔离通过，真实 PostgreSQL 测试未运行：
本机没有可用的测试 DSN/服务，Docker daemon 不可用。不得将 PG gated skip
作为真实后端通过证据。完整 enterprise 测试另有基线 SQLite 专属测试未隔离，
本轮不修复无关测试；不声称完整 enterprise 套件通过。

Postgres 的保守全局 mutation 锁会串行 workspace 写入，普通查询不受该锁串行化。
这是 R1 正确性取舍，吞吐量未测量；细粒度锁改进必须伴随真实 PG 竞态测试。
以上为 R1 当时的历史验证结果，不是当前 PostgreSQL 验收结论。
后续 R2–R4 已实施，T6 在专用真实 pgvector server 上验证 personal/enterprise，
包括随机隔离数据库及新鲜 context 清理、四种离线 reader 和完整 gate 证据；
详见 [memory maintenance](../../memory-maintenance.md)。最终全分支审查仍待 controller。
工作区与分支保留，未 merge/push，未修改用户数据库，未启动/停止服务。
