# LadyM R2–R4：可信学习、知识导航与预算维护

状态：R2–R4 全部实施；六个任务审查、全分支审查与修复复审、最终双版本真实 PG 验证通过
基线：`44f55c0`；沿用 [R1 总体规格](2026-11-08-trusted-agent-memory-design.md)

实现/恢复指南与实际 B01–B14 命令和 live-PG leaf counts：
[memory maintenance](../../memory-maintenance.md)。离线 fixture 不声称 QA 或 LLM 收益。

## 1. 范围与方式

本轮交付 R2、R3、R4 的可运行路径，以及真实 PostgreSQL parity。
保留模块化单体、两种 edition、统一 Engine、零 cgo 的生产二进制。
不增加数据库/队列/服务、不自主执行来源中的命令、不自动导入历史 transcript，
不改用户现有库、主分支和其他工作区；所有新功能有回归测试。

选定方式是复用 memories/edges 和 R1 原子批次。拒绝独立知识平台或新的 Agent runtime：
它们不解决证据真实性，增加迁移与运维成本。主题视图显式生成，后台维护 opt-in，
不默认给整个仓库建设 wiki，不宣称未经真实任务量测的 QA 改善。

使用证据对象、feedback、topic 和持久化 job，是产品所需的生命周期实体，不是增加认知层。
当前 API/CLI/MCP/Go/Python/TS SDK 的共同契约必须贯通；UI 不新增页面。
模型 tokenizer 由宿主注入为纯本地函数；默认 byte 模式明确标单位，不冒充 token。

## 2. 通用数据与事务不变量

新增 MemoryType：claim、evidence、topic_view、maintenance_job、task_state。
前三种位于 L2，job 位于 L1，显式 task_state 实现位于 L2；
job/task_state 均作为系统类别排除，不挤占普通 recall。
旧记录兼容可检索；新类型必须带明确的 metadata 结构和验证。
claim/view/job 的 revision 为正整数、metadata 持久化；每次内容/状态修订递增，
访问 Touch 不改变 revision。来源 fingerprint 为 SHA256(content)；
原始 source 的摘要变化不作为内容证据变更，content 变化必须使派生知识失效。

MemoryBatch 新增 AbsentIDs guards，支持第一次确定 ID topic/job 的竞争创建。
原 guard 快照仍以 R1 equalKnowledge 为准，revision 包含在 metadata。
所有知识批次、PutMemory、UpdateMemoryContent、Retire、Delete：

1. 内容变化、usable→unusable、verified→非verified 是失效触发；
2. 在同一个 tx、同 workspace 中递归访问 produced_from、abstracts、
   source_episode/source_episodes 依赖，循环 visited 去重；
3. 将所有依赖标 stale/evidence_missing、increment managed revision，
   不删除不相关 knowledge、不跨 workspace、保留已丢失 source ID；
4. deleted 根不可恢复正文，派生 view/cache/job 不可继续提供删除内容；
5. tx 失败全部回滚，包括根写入、下游状态和任务检查点。

Stale 节点也参与传播，不因它已经 stale 就停止后续子图。
source content fingerprint 验证和 guard 在提交前都执行，防止查证后来源改变。
同批次的新 topic/claim 只能引用提出状态中仍可用的来源；不能用自身或循环引用晋升 verified。
job/task_state 不作为普通知识保护 Decay，job 不应因引用 metadata 保留所有日志。

## 3. R2：断言与反馈

### 3.1 EvidenceRef

字段：memory_id、fingerprint、start_rune、end_rune、kind。
范围为零基 rune 半开区间，0/0 明确表示整个 content。
fingerprint 校验整段 source content；片段边界必须在内容范围内。
kind 是 observation、test_result、tool_result、user_confirmation。
来源必须同 workspace、usable，不能是 claim/view/job/prediction；
verification_kind 在来源 metadata 与请求 kind 一致才能作为验证证据。
引用只在返回时按需提供 excerpt，不复制整个来源进 claim。

RecordEvidence(kind,content,...) 是显式 caller 提交的观测，不自动运行或验证来源。
test_result/tool_result 表示调用者报告的结果，必须 outcome=success，
user_confirmation 是明确用户确认；系统不能从普通 hook outcome 自动推导验证。
验证的可信边界是有 workspace 写权限的调用者，而不是模型自评。
HTTP actor 来自已认证 username，auth-off 本地为 local；不接受正文提供的 actor。
MCP/CLI 本地 actor 分别 mcp/cli；Go SDK actor host 可在 options 传入。

### 3.2 Claim

字段：memory record、revision、status、evidence_refs、counter_evidence、
scope（environment、code_version、conditions）、verification（method、actor、
checked_at、evidence_refs）、valid_until。
status=observed/user_asserted/inferred/verified/stale/rejected。
CreateClaim 默认 inferred；可选 user_asserted，但禁止请求直接写 verified/stale/rejected。
content 非空，refs <=64，conditions<=32，TTL >=0 有限；引用重复拒绝或稳定去重，
至少一个证据才允许 verified，不能仅 confidence 数字晋升。

ApplyFeedback 使用 memory_id+expected_revision、action、replacement、
evidence_refs、method 和 reason。action 为 verify/correct/reject。

- verify：已存在的证据引用内容一致，且至少一种显式验证证据满足上述条件；
  保持正文，状态 verified，verification 信息写入，revision++。
- correct：replacement 非空、新 refs 有效且有验证凭据；同 ID 原子修正、
  revision++、verified，先使所有 downstream stale；修正旧 verified
  不能只凭 inferred 文本覆盖。
- reject：reason 非空，可带反例 refs；状态 rejected、revision++，原子失效 downstream。

并发反馈同 revision 只一个提交；其他返回 ErrMemoryConflict。
source 修改/删除后下一读不能把旧证据 claim 当 verified。
有效期过期 claim 不能回溯 trusted；维护将其 stale，读路径立即检查 TTL。
GET claim 返回证据验证是否仍成立及安全引用，不自动读取外部路径。

### 3.3 内容归档

提供显式 ArchiveEvidence API/CLI，默认 disabled（[archive].enabled=false）。
只接受调用者提供的字符串，不接受文件路径、URL 或目录扫描；
单条 <=64KiB、workspace总 archive text <=8MiB、默认 retention 30 天，
精确 byte 而非 token。容量检查与插入同事务，并发不能超限。
入库前移除常见凭据字段/Authorization Bearer/API key/password assignments，
archive正文元信息不保留 original。规则文档明确 best-effort，不能声称全量 PII 删除。
meta valid_until 用于 retention，expired archive 默认不作为新验证证据；
尚被有效 claim/view 引用的 source 可 pin 避免自动删除；显式删除不受 pin 阻止。
重试不通过 silent truncate 规避容量，错误明确返回。

## 4. R3：主题视图与上下文

### 4.1 构建与读取

BuildTopic(topic,input_ids,expected_revision) 只接受 1..64 个有效同 workspace 来源，
topic 非空<=128 runes。确定 ID=SHA256(workspace+NUL+topic)。
输入可为 code/普通事实/verified claim/playbook；不允许 job/prediction/自身/topic。
claim 未verified 默认不能进入视图；普通 legacy 事实可进入但明确未验证。
不调用 LLM，生成 source-grounded 导航视图：
topic 标题、来源 ID/类型、精确有限片段、scope、已验证与未验证标注。
不是语义推断引擎，不虚构流程、结论、调用关系。总正文<=16KiB rune安全，
每个输入至少完整 ID 链接；只截 excerpt，不能丢弃输入 provenance。
metadata存 input fingerprints、generation version、revision、topic、
source_episodes；produced_from 对每个输入建边。
创建 expected_revision=0，使用 MustNotExist；更新必须准确 CAS 旧 revision。
并发首次写一个 winner；任何 source 变更使 view stale，读取返回 usable=false，
不返回 stale正文作参考。GetTopic 按需查源，引用失效立即拒绝使用。

### 4.2 HybridRecall

保留普通 recall 默认行为，新增显式 hybrid 模式/API。
来源三组：词法 exact token/symbol/path 匹配、范围内向量 topK、有效 topic/source 图。
先 workspace/usable/type/TTL 过滤后分别 topK，再稳定 RRF 合并（k=60，
相同得分按 ID），图节点预算沿用128。exact source memory 可无 embedding。
job/task_state/evidence/prediction不挤占普通知识 slots；evidence 只按明确 source 引用拿取。
输入 query 空白明确错误，limit 1..200。Elapsed、sources、sufficient 启发式如实返回，
无 provider/SQL 错误空成功；读路径生成 LLM 调用数为0。

### 4.3 Bootstrap

BootstrapRequest: topic_ids、environment、code_version、max_tokens、
task_id 可选、unit 默认bytes。tokens 模式宿主注入
TokenCounter(name, Count(text) (int,error))，同时给出 model 名；无counter报错。
默认 bytes上限4096；tokens上限默认512、最大1024，明确使用counter而非rune估计。
候选为匹配 scope且未过期的 verified claims + 有效 topic入口链接；
topic只注入简短索引，细节通过get/hybrid。旧 generic/inferred fact 不自动注入。
task_state 由用户显式保存 goal/next_step、TTL 默认24小时，
不得把预测意图转成 task，scope/workspace隔离。
完整输出包含 reference-not-instructions 提示及完整来源 ID，在rune边界裁剪，
逐次Count整串保证真实 token预算，不依赖加和；nil/negative count/error显式失败。
不足一个完整条目时只返回头或空、不会出预算；tokenizer无网络。
LangGraph 新 CreateBootstrapNode 调用同Engine方法；旧 recall node 4096bytes不变。
HTTP/MCP/CLI 只提供bytes；tokens 模式经 Go SDK宿主注入，不假设远端模型tokenizer。

### 4.4 评估门

增加离线可运行的知识导航scenario，记录 source coverage、missing/stale 使用率、
read elapsed、bootstrap计量。四variant：stateless/direct_source/r1/r2_topic。
fixture任务有跨文件关系和精确符号；topic不强迫简单查找绕行。
fixture结果只证明契约/来源导航，不称为真实QA准确率。
真实LLM对比保留现有LongMemEval入口，必须显式 --limit/--model/--budget-seconds；
budget 为有界 wall seconds，不是美元或 billing tokens。仅环境 credentials 不授权网络。

## 5. R4：持久化任务与预算

### 5.1 Job

存 maintenance_job Memory，deterministic id(workspace,operation,input_fingerprints)；
metadata operation、input_ids、fingerprints、state、revision、attempt、
priority、not_before、lease_until、error_code、checkpoint、budget。
state=pending/running/done/failed/superseded。input ids 不以 legacy provenance方式 pin，
不在 recall/graph 结果。priority delete/revoke无需异步执行（同步失效），
feedback/source_changed/expired_verified/new_episode/normal_topic顺序。

发现待处理项形成 idempotent job；重复 unchanged cycle不重复 embedding/LLM。
claim/view mutation同步失效，再产生/发现刷新任务，不靠异步保证删除安全。
Lease job 在AtomicBatch中 guard revision，不同worker winner唯一；
默认60秒，心跳可续期；crash后expired lease下一worker接管，
done过期仍作为已消费去重，输入fingerprints变化生成新job。
job完成、处理输入checkpoint和派生memory/edges 在同事务；
provider异常 job失败可重试，最多3 attempt，错误显式报告。

### 5.2 Worker集成

新增 [system2].budgeted_enabled=false。保持旧worker行为默认兼容，
开启后 CLI/Engine 使用 RunBudgetedCycle(ctx, workspace)，
候选<=50，单周期LLM调用上限8、token上限8192、wall<=30秒、并发默认1最大4。
已运行的provider支持ctx取消；现接口无ctx时不得后台泄漏goroutine，
预算模式拒绝不能取消的provider或只运行无LLM deterministic jobs；
提供ContextLLMProvider/ContextEmbeddingProvider 可选扩展，
HTTP providers用request context，SDK注入未实现ctx时报显式error。

token budget按入参JSON+预留输出上限计量，默认 conservative UTF8bytes上界，
真实 tokenizer可注入；不是账单token，标reserved_units和measured单位。
provider的maxTokens约束输出。完成后Count真实输出若超cap拒绝发布并report error，
不声称保证对方API实际账单。wall在每工作单元及provider I/O检查/取消，
sqlite/PG请求上下文也不得无界等待。指标 budget停止不是成功完成未执行job。
操作：待处理episode typed extraction（现classifier可作为compat，
新 structured decision带target_id/action/evidence_ids），
source失效topic刷新、expired claim处理、已有显式verified反馈不自动再验证。
读取stored embedding（新增Store.GetEmbedding）复用L5/聚类，
无内容变化和模型指纹变化不重嵌入。

### 5.3 重嵌入迁移

fingerprint = provider/model/dim/tokenizer，injected SDK需明确 provider identity/version，
不以dim相等推断同空间。存 configured fingerprint、migration target、
cursor(ID)、state。相同 fingerprint 不调用 stored Embed；
不同 fingerprint 默认fail-fast，allow_dim_change显式迁移。
迁移必须copy-on-write：旧 embedding active 到最后，stage新向量到临时table，
保存cursor与向量同tx，crash后同目标继续；
目标改变不能继承别的cursor。完成原子切换维度/index/meta，
失败保持旧空间可恢复，next同配置resume；已成功迁移可用保留旧向量rollback
直到显式Finalize，不能silent空向量成功。
path entities保持无向量；migration进行中禁止不同空间查询，
engine.New返回explicit mismatch/migration status不混用。
不删除旧embedding直到Finalize，容量代价明确。

### 5.4 观测

HTTP metrics与worker report增加低基数job state/op counters、pending数量、
attempts、reserved/measured units、cycle预算stop reason、source追溯数、
stale拒绝数、topic膨胀比、elapsed。禁止workspace/文本/ID作Prometheus label。
token统计明确unit与模型，避免伪装原token省量。report可持久化读取；
已有metrics契约保持，新增字段，不改本任务无关业务。

## 6. 验收与回滚

B01 同revision verify/correct/reject并发仅一个winner；无有效验证证据不能verified。
B02 原文修改/删除/reject递归使claim→topic→入口失效；循环终止、跨ws不影响、故障回滚。
B03 CJK/rune证据片段、fingerprint变化、TTL边界正确；访问不改变knowledge revision。
B04 archive disabled默认、redaction、64KiB/8MiB并发容量、retention/pin/delete均测试。
B05 scoped HTTP/MCP/CLI/Go/Python/TS能写读claims/feedback，actor不可伪造，权限无跨ws。
B06 topic来源完整、无虚构、第一次/更新CAS、来源变化无法返回陈旧正文。
B07 hybrid独立topK、无embedding exact源码、相同score稳定、job/prediction不挤占。
B08 bytes4096与token512/1024分别实测；恶意/异常counter、无效UTF8、scope/TTL/source删除安全。
B09 job重启/lease接管/并发winner/atomiccheckpoint、不变输入不重复provider、失败attempt<=3。
B10 wall/calls/reserved budget生效；ctx真实取消；不支持ctx provider显式拒绝、不泄漏后台调用。
B11 embedding fingerprint同dim不同模型检测，crashresume、回滚、finalize/path保持。
B12 quality eval四variant输出来源/成本shape，不声称fixtureQA收益。
B13 临时真实pgvector集群personal/enterprise存储和新路径测试通过，清理仅自建cluster。
B14 全量personal、enterprise、race、SDK、short eval、零cgobuild、comment linter有证据。

旧R1接口保留，新增功能默认opt-in。新metadata类型通过schema guard验证；
降级忽略新增类型但旧默认recall必须隔离system项，未来降级文档提醒。
新迁移表幂等、事务创建，两backends一致，migration rollback不破坏用户正文。
主spec的“非本轮”是R1交付历史；本addendum把用户授权剩余阶段纳入当前实施。

## 7. 独立审计后的绑定决策

1. verified 是 caller-attested 的可追溯证据验证，不称独立实验验证。
   新 managed claim 禁止 generic Remember/CRUD 直接写 verified metadata 或改正文；
   typed feedback 才能修正，MCP 的 actor=mcp 不是认证人类身份。
2. 共享tx kernel覆盖所有bulk代码删除/重索引/普通内容更新、Retire、
   batch；先遍历再关闭边。拒绝改变既存记录 workspace。
   rejected descendants保持rejected，stale也继续传播；
   views/claims过期/无证据时 generic输出只返回ID/state、不回显不可信正文。
3. topic输入保存内容fingerprint+claim/view revision；验证状态/scope/TTL变化同样
   使派生物失效，读取freshness在四条read路径共享。
4. ArchiveEvidence是特殊RecordEvidence入口、engine-owned archive marker；
   入参64KiB先校验再redact，持久化总bytes计所有未删除archive；
   pin可保留过期原文供已验证claims审计，但过期证据不能用于新的晋升。
   RecordEvidence普通非archive也受单条64KiB上限，防止避开容量目的：
   它用于短结果/确认而非完整transcript，plain总容量不由archive quota承诺。
5. Budgeted provider调用传ctx和输出单位上限；cycle unit必须一致，
   默认reserved_bytes<=8192、calls<=8，输出API token cap单独标token，
   不能与byte reservation相加。不支持ctx的注入provider明确拒绝，绝不goroutine假取消。
6. job ID包括workspace/op/target/canonical args/sorted(id,fingerprint,revision)，
   lease token每次claim随机改变，heartbeat/complete都检查owner token；
   abandoned owner即便迟到也不得发布。running crash也计attempt；done dedup记录不Decay。
   新后台goroutine保留注入routing；store context view贯通数据库等待和provider。
7. embedding迁移提供独立status/resume/rollback/finalize入口，
   能绕过正常Engine的fingerprint拒绝打开storage；显式迁移保留回滚代
   后禁止正文/vector写（包括直接SQL通过数据库migration gate），直至finalize/rollback。
   回滚窗口读旧/新active空间按各自指纹，新写不能使旧备份失真。
   兼容allow_dim_change自动迁移路径可在成功后finalize以恢复旧写行为；
   独立API/CLI迁移默认保留rollback窗口。未配置明确model identity的injected provider
   不悄悄认定同空间；旧db显式adopt指纹（无vector变化）或显式migration。
8. downgrade最低版本为支持这些新system类型隔离的当前release；
   未改R1不能保证隔离，必须导出用户记录或删除system记录后降级。

审计发现的7项问题均在此冻结，不由实施者无依据推断。

计划审计进一步冻结：
- shared freshness 位于storage，普通Recall/List也检查传递TTL，无Engine循环依赖；
- Bootstrap Conditions为调用者显式满足集合，claim条件subset，空claimenv/version为wildcard；
- migration uniquegeneration+ordered ID keyset，activation逐ID completeness而不是仅数量；
  正文/vector创建冻结，Touch允许，显式Delete取消staging并从rollback备份移除source；
- completion在数据库事务内校验ctx、lease未过期、owner/revision及input/target/generation，
  即便没有新owner takeover，expired旧任务也不得发布；
- 输出限制raw模型正文在parse前检查，ContextLLM返回RawOutputBytes，
  默认inputbytes+1024outputbytes reserve8192，API1024tokens另标，绝不混合单位；
- Job持久化Args/InputRevisions，done不衰减；L5/proc复用stored embedding；
- 新关键测试必须显式backend-parametrized且报告非零PG病例，
  PG清理使用独立boundedcontext，不以已取消t.Context清理数据库。

## 8. 交付记录

实施任务与修复均在当前会话分支持久化。最终 Go 实现 `764f607`：
全量个人版/企业版真实 PG 测试、七包 race、short eval、
三个零 cgo 生产构建与依赖隔离通过。后续 `39d77e9` 仅修复
硬截止时间测试的启动负载敏感性；生产预算仍包括 SDK 子进程启动。
最终 benchmark 69/69；Python SDK 34 与 6 个子测试、TypeScript 31/31，
真实 loopback 集成全部启用，无跳过。

全分支审查的 PG BeginTx 取消与 Engine.WithContext L0 共享遗漏已修复，
复审无未解决的重要问题。实际 leaf counts、保留的 edition-only/helper skips、
已完成 B01–B14 验收与全部控制器取舍见
[交付与决策记录](../../memory-maintenance.md)。
未执行外部 hosted LLM QA 测量，不声称准确率或账单 token 节省。
