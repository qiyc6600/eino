# 开发待办与完成状态

> 本文档追踪任务要求的实现进度。已完成项标注 ✅，部分完成标注 ⚠️。

---

## 已完成项

| # | 项目 | 涉及模块 | 状态 | 实现位置 |
|---|------|----------|------|----------|
| 1 | 摘要压缩使用 LLM | 模块 04 | ✅ | `contextmgr/summarizer.go` — `summarizeWithLLM()` |
| 2 | 框架对比分析文档 | 模块 03 | ✅ | `docs/framework-comparison.md`（381 行） |
| 3 | 单元测试 + 集成测试 | 全局 | ✅ | 14 个测试文件，110+ 测试用例 + 8 个集成测试 |
| 4 | docs/ 目录（design/api/demo-script） | 全局 | ✅ | 4 份文档共 1700+ 行 |
| 5 | 恢复语义文档化 | 模块 02 | ✅ | `docs/design.md` 第 6 节 + README |
| 6 | CreateUser / UpdateUserRoles 路由 | 模块 01 | ✅ | `httpapi/router.go` — `handleUsers` + `handleUsersSub` |
| 7 | send_email 添加发送记录 | 模块 03 | ✅ | `tools/email.go` — `EmailStore` 内存记录 |
| 8 | 节点级中断集成 SteppedRunner | 模块 02 | ✅ | `agent/stepped_runner.go` — `NodeInterruptConfig` + 意图检测 |
| 9 | Checkpoint 保存完整 Agent 状态 | 模块 02 | ✅ | `SteppedRunState.Serialize()` 保存完整对话+步骤 |
| 10 | Resume 优先从 Checkpoint 恢复 | 模块 02 | ✅ | `runner.Resume()` 优先 `LoadFromCheckpoint`，降级为线程重建 |
| 11 | 多用户隔离框架强制 | 模块 01 | ✅ | 类型化 ToolIdentity + threadStore/run/审批属主校验 + 存储层 `CheckUserScope` |
| 12 | 会话过期/续期 TTL（进阶档） | 模块 01 | ✅ | `Session.ExpiresAt` + `ValidateSession` 滑动续期，`SESSION_TTL` 可配置 |
| 13 | 用户账户并发存储 | 模块 01 | ✅ | `UserStore` 抽象；内存实现加锁，PostgreSQL 实现支持多实例共享 |
| 14 | 节点级中断显式触发 | 模块 02 | ✅ | Chat 请求 `confirmBeforeExecute` 标志（前端开关）替代关键词检测，RunStep 按参数传递无共享状态污染 |
| 15 | 持久化存储后端（进阶档） | 全局 | ✅ | 三个存储接口的 file（JSON 装饰器）实现 + 环境变量切换，隔离校验自动继承，重启后状态不丢 |
| 16 | 记忆系统 v2 | 模块 05 | ✅ | 结构化条目 + 冲突消解（历史修订链）+ RetrieveRelevant 统一打分检索（预算/衰减/命中强化）+ Consolidate 遗忘归档/LLM 整合画像/情景沉淀 |
| 17 | 对话线程历史持久化 | 全局 | ✅ | `ThreadStore` 接口 + `FileThreadStore` 装饰器（`THREAD_STORE=file`），重启后对话历史不丢，token 条与界面内容一致 |
| 18 | PostgreSQL 多实例后端 | 全局 | ✅ | 用户、会话、线程、检查点、记忆、审批共享；线程 advisory lock；审批原子 claim |
| 19 | 示例业务持久化与幂等 | 模块 03 | ✅ | PostgreSQL 订单软删除、邮件记录、工具调用幂等凭据；跨实例重放返回原结果 |
| 20 | PostgreSQL 版本化迁移 | 全局 | ✅ | 内置 SQL 迁移、schema 版本表、校验和、事务与跨实例 advisory lock |
| 21 | 生产健康检查 | 全局 | ✅ | `/healthz` 存活探针；`/readyz` 检查 PostgreSQL 与完整迁移历史，带独立超时 |
| 22 | 认证安全加固 | 模块 01 | ✅ | Argon2id 随机盐密码；旧 SHA-256 登录升级；角色变更批量撤销会话并校验当前角色 |
| 23 | 管理员安全引导 | 模块 01 | ✅ | 移除编译期默认凭证；空用户库通过环境变量创建管理员；弱密码和不完整配置启动失败 |
| 24 | 登录失败限流 | 模块 01 | ✅ | 用户名/IP 双维度；PostgreSQL 多实例共享计数；429 + Retry-After |
| 25 | PostgreSQL 运行事件持久化 | 全局 | ✅ | 跨实例按用户读取，审批恢复追加事件，保留期与过期清理 |
| 26 | 审批创建事务一致性 | 模块 02 | ✅ | 全 PostgreSQL 配置下 Checkpoint、线程历史和审批记录同一事务提交，故障时整体回滚 |
| 27 | 审批恢复收尾事务一致性 | 模块 02 | ✅ | 完成或连续审批时，旧审批凭据、线程/新审批状态与运行事件同一 PostgreSQL 事务提交 |
| 28 | 流式输出（事件级 + token 级） | 模块 02/03 | ✅ | `EventRecorder` 增加 `ProgressSink` 出口；`generate` 改走 Eino `Stream`，片段实时下发并用 `ConcatMessages` 合并；SSE 新增 `tool_call` 进度帧与 `streamed` 标记；前端进度行 + 停止按钮（AbortController） |
| 29 | 完整历史与模型上下文分离 | 模块 04 | ✅ | `SteppedRunState.ModelContext` 承载压缩后的模型视图，`Messages` 成为权威完整历史并落盘；未压缩时二者合一，行为与体积不变 |
| 30 | Token 计费覆盖完整请求 | 模块 04 | ✅ | 计数补上工具调用参数与工具名；工具定义 schema 开销计入；新增 `RESERVE_OUTPUT_TOKENS` 与 `MAX_TOOL_RESULT_CHARS`；采集 `ResponseMeta.Usage` 并在界面区分估算与实际 |
| 31 | 记忆开关（禁止记忆） | 模块 05 | ✅ | 保留键 `__settings` 存每用户开关；`ExtractAndSave` / `RetrieveRelevant` / `Consolidate` 内部强制拦截；`GET`/`PUT /api/memory/settings`；前端开关与置灰提示 |
| 32 | 事件补全与自描述 | 模块 02/03 | ✅ | 补齐 `model_call_start/end`、`tool_call_start`、`acl_denied`、`hitl_interrupt` 的记录点；`Record` 从 metadata 提取 `ToolName`/`AgentName` |
| 33 | 持续集成与统一检查脚本 | 全局 | ✅ | `scripts/check.sh`（gofmt / vet / build / test -race / 前端语法）作为本地与 CI 的单一事实来源；`.github/workflows/ci.yml` 两个并行任务；前端语法检查在缺少 Node 时只能显式跳过 |
| 34 | 追加式写入（消除写放大） | 模块 04/05 | ✅ | `ThreadHistoryAppender` 带长度守卫的追加，守卫在 SQL 内用 `jsonb_array_length` 求值，不读取既有 blob，无需 schema 迁移；只有纯追加的 run 走该路径，前缀被修复或守卫不匹配时回退全量替换。实测 6 轮对话：追加 2538 字节 vs 每轮替换 15558 字节 |
| 35 | 行尾统一为 LF | 全局 | ✅ | `.gitattributes` 强制文本文件 LF：`core.autocrlf=true` 下 Windows 检出为 CRLF，会让 gofmt 把整个仓库判为未格式化，使检查脚本在本地误报 |
| 36 | 线程历史治理开关 | 模块 04/05 | ✅ | `THREAD_HISTORY_MAX_MESSAGES`（单会话上限，裁剪后经 `GuardToolPairs` 保护 tool 配对）与 `THREAD_RETENTION`（按时间清理，写入时顺带执行、仅限当前用户）；默认均为保留；`updated_at` 索引由迁移 004 添加；未加时间戳的旧数据不删 |
| 37 | 向量记忆文本长度上限 | 模块 05 | ✅ | `FormatVectorResultsWithin` 按预算跳过装不下的条目（整条丢弃而非截断文本），`RetrieveRelevant` 用它截断向量文本后再让 KV 条目使用剩余预算；顺带把硬编码的 0.3 阈值改用既有但未被使用的 `minVectorRelevance` 常量 |
| 38 | 真实 PostgreSQL 验证 | 全局 | ✅ | 新增 `TestPostgresThreadAppendAndPrune`：直接断言迁移 004 的版本记录与索引存在，并在真实库上验证追加守卫（首次插入、长度匹配追加、过期长度拒绝且不改数据）与保留期清理。全量 `-race` 测试带真实数据库通过 |
| 39 | 按消息数裁剪接入执行路径 | 模块 04 | ✅ | `TrimByCount` 此前无生产调用方，`MAX_MESSAGES` 是只读不用的配置。现接入 `compressMessages`（先条数、后 token），默认值改为 0 保持既有行为。顺带修掉一个隐蔽缺陷：`ModelContext` 原本以 `TokenInfo.Compressed` 为赋值条件，导致窗口生效但结果被丢弃——改为按长度差判定 |
| 40 | 记忆召回独立于 KV 来源 | 模块 05 | ✅ | `RetrieveRelevant` 不再因 KV 为空而提前返回，只有向量条目的用户也能获得召回 |
| 41 | 审批恢复路径流式 | 模块 02 | ✅ | `ResumeContext` 接受 `WithProgressSink`；决策接口 `stream=true` 走 SSE（默认 JSON 响应逐字节不变）；`HandleApproval` 接受 recorder 并记录被批准工具的执行事件；claim 之后执行 context 与客户端连接解耦（`WithoutCancel` + 重新施加 2 分钟超时），断连不再消耗审批或把已发生的副作用记为取消 |
| 42 | 会话改用 HttpOnly Cookie | 模块 01 | ✅ | 登录下发 `HttpOnly` + `SameSite=Strict` 会话 Cookie，登出使其立即过期；认证中间件接受 Cookie 且请求头优先；前端不再读写 token，页面加载时探测会话以恢复登录态。`SESSION_COOKIE_SECURE` 控制 Secure 标记（默认开启，普通 HTTP 局域网演示需关闭） |
| 43 | 文档 RAG（每用户语料） | 模块 05 | ✅ | 文档按段落切分（约 400 字/块、80 字重叠）后存入记忆存储的保留键，自动获得三种后端持久化与用户隔离；独立文档向量库 + 独立预算（`DOCUMENT_BUDGET_TOKENS`）；片段带 `[n]` 序号注入并要求模型标注来源；重启后从存储懒重建索引。API 与界面（粘贴 + `.txt`/`.md`）齐备 |
| 44 | 修复 chromem 包装层的三个缺陷 | 模块 05 | ✅ | ① `Query` 在 `topK` 大于集合文档数时直接报错，而调用方吞掉错误 → 小语料下向量召回静默消失（文档库天然是小语料）；② 空查询词报错，而 token 条正是用空查询调用的；③ `DeleteUser` 以"v0.7.0 无此 API"为由不支持，实测 `DeleteCollection` 存在。该包装层此前完全没有测试，现已补上桩 embedding 的离线测试 |
| 45 | 相关度阈值按 embedding 尺度自动取值 | 模块 05 | ✅ | 真实 embedding 的相关结果在 0.6–0.9，hash 伪嵌入压缩在 0.15–0.4；用同一个 0.3 会让 hash 模式下的中文检索完全失效（实测正确文档仅 0.168，且无关文档得分更高）。新增 `VECTOR_MIN_SCORE`，未设置时按提供方自动取值（hash → 0.1，真实 → 0.3） |

---

## 已知限制（非阻塞）

| # | 限制 | 涉及模块 | 说明 |
|---|------|----------|------|
| 1 | HITL 为异步审批模式 | 模块 02 | 中断后 run 结束，通过独立 API 调用恢复，非"挂起等待"语义 |
| 2 | grep 使用硬编码 mock 数据 | 模块 03 | 搜索日志是示例场景，数据完全合成 |
| 3 | 线程历史默认无保留上限 | 模块 04/05 | 完整对话默认永久保留，存储总量无上限。**已提供 `THREAD_HISTORY_MAX_MESSAGES`（单会话上限）与 `THREAD_RETENTION`（按时间清理）两个开关，默认均为保留**——静默丢弃用户对话属于产品决策，不适合作为默认值。写入成本已由追加式写入解决（见第 34 项）。未做：跨用户的全局清理入口、归档而非删除 |
| 4 | 压缩后的 checkpoint 体积上升 | 模块 02/04 | 真正压缩的 run 会同时序列化完整历史与压缩上下文两份 |
| 5 | ~~向量记忆文本无长度上限~~ | 模块 05 | **已修复**：向量召回文本按预算截断（整条丢弃、保留能装下的），KV 条目使用剩余预算，注入总量不再超预算。修复前实测：120 token 预算下注入 1242 token |
| 6 | ~~`MAX_MESSAGES` / `TrimByCount` 未接入~~ | 模块 04 | **已修复**：接入 `compressMessages`（在 token 裁剪之前），默认值由 30 改为 0 以保持既有行为——窗口设得过小会使摘要压缩永不触发。注意开启条数窗口会关闭追加式写入的收益，两者取舍见 design.md 3.4.2 |
| 7 | ~~审批恢复路径不流式~~ | 模块 02 | **已修复**：`ResumeContext` 接受进度出口，决策接口 `stream=true` 时复用与聊天相同的 SSE 帧；被批准的工具执行现在也记录事件（此前 `HandleApproval` 直接调用工具、不记录任何事件，在实时流与运行事件里都是空白）。另：claim 之后恢复执行与客户端连接解耦 |
| 8 | ~~刷新页面需重新登录~~ | 模块 01 | **已修复**：改用 HttpOnly + SameSite=Strict 会话 Cookie，刷新页面保持登录；顺带解决"凭证对 JS 可读"——页面脚本已读不到会话。请求头认证保留且优先，脚本与第三方客户端不受影响 |
| 9 | ~~前端无自动化语法检查~~ | 全局 | **已修复**：新增 `scripts/check.sh` 与 `.github/workflows/ci.yml`，前端语法作为独立 CI 任务运行。`go:embed` 不校验 JS，语法错误不影响任何 Go 测试却会让整个页面失去交互——该缺陷曾真实发生一次 |
| 10 | ~~记忆检索在 KV 为空时提前返回~~ | 模块 05 | **已修复**：召回改为独立于 KV 来源，KV 为空时仍查询向量存储。代价是尚无记忆的用户多一次 embedding 调用 |
| 11 | 文档 RAG 只接受纯文本 | 模块 05 | 不支持 PDF / DOCX 解析；同名文档重复上传会新建一份而非覆盖 |
| 12 | hash 伪嵌入的检索质量有限 | 模块 05 | 默认 `EMBEDDING_PROVIDER=hash` 只做词面匹配，中文排序质量差（应用已自动降低相关度阈值使其可用，但排序仍不可靠）。真正的语义检索需配置 `openai` 或 `ollama` |

> 已修复：多用户隔离非自动强制（原限制 #1）——已升级为框架强制（类型化 ToolIdentity + 线程/run/审批属主校验 + 存储层 CheckUserScope），详见 docs/design.md 5.3 节。
> 已修复：节点级中断需意图触发（原限制 #2）——改为请求显式 `confirmBeforeExecute` 标志（前端开关/API 字段），消息文本不再参与触发判断。

---

## 历史记录

- 2026-09-20：修复审批跨重启恢复、子 Agent 状态丢失、批量调用遗漏和连续审批；增加同会话串行化、持久化执行凭据、有限重试、取消与存储失败处理。新增故障注入和并发回归测试，详见 README 的“执行可靠性”。JSON 后端仍限单进程使用，外部副作用不具备跨系统事务保证。
- 2026-09-20：新增 PostgreSQL 多实例存储，覆盖用户、会话、线程、检查点、长期记忆和审批；同线程跨实例加锁，审批决定原子认领。
- 2026-09-20：订单与邮件示例接入 PostgreSQL；删除订单保存幂等结果，邮件记录按工具调用去重。真实 PostgreSQL 双实例验证通过。
- 2026-09-21：PostgreSQL 建表逻辑升级为版本化 SQL 迁移；增加迁移锁、逐版本事务、历史校验和与真实数据库并发启动测试。
- 2026-09-21：增加公开的存活/就绪探针；PostgreSQL 就绪状态覆盖连接、迁移完整性和版本兼容性。
- 2026-09-21：密码升级为 Argon2id，并兼容旧 SHA-256 账户登录迁移；角色变更会撤销内存、文件和 PostgreSQL 会话。
- 2026-09-21：移除 `admin/admin123` 与前端快速登录；初始管理员改为环境变量安全引导，多实例并发创建保持幂等。
- 2026-09-21：增加登录失败限流；用户名和直接连接 IP 双维度统计，PostgreSQL 事务共享并返回 429/Retry-After。
- 2026-09-21：运行事件接入 PostgreSQL；审批创建的三项记录改为同一事务，并验证最后一步写入失败时全部回滚。
- 2026-09-21：审批恢复收尾改为单事务；连续审批和最终完成均通过真实数据库故障注入验证回滚与禁止重放。
- 2026-09-21：实现事件级与 token 级流式输出。`EventRecorder` 增加进度出口，模型调用改走 Eino `Stream`，SSE 边跑边推路由/工具/审批进度与答案片段；限流重试收紧为"未下发片段才重试"；前端新增进度行与停止按钮。
- 2026-09-21：压缩结果不再覆盖完整历史。`SteppedRunState.ModelContext` 承载模型视图，`Messages` 作为权威完整历史落盘，消除"摘要的摘要"与原始对话丢失；未压缩时二者合一，行为不变。
- 2026-09-21：Token 计费覆盖完整请求。补上工具调用参数、工具名与工具定义 schema 开销；新增回答预留空间与工具结果长度上限两个配置；采集并透出模型实际用量，界面区分估算与实际。
- 2026-09-21：新增记忆开关（禁止记忆）。开关在记忆服务内部强制拦截写入与注入，保留键设置项复用现有存储后端，不新增迁移；配套 API 与界面开关。
- 2026-09-21：端到端浏览器走查修复三处缺陷：app.js 语法错误导致整个前端失去交互（Go 测试全绿，`go:embed` 不校验 JS）、mock 流式节奏放错位置导致无打字机效果、进度帧缺工具名；另修复记忆开关接口字段不一致导致刷新后状态丢失。README 补充前端语法检查步骤。
- 2026-09-21：新增 `scripts/check.sh` 与 GitHub Actions 流水线。检查逻辑收敛到单一脚本（gofmt / go vet / go build / go test -race / 前端语法），本地与 CI 共用；前端语法作为独立任务，因为 `go build` 不校验 `go:embed` 的 JS。脚本的两个守卫均通过注入真实缺陷验证会失败。
- 2026-09-21：线程历史改为追加式写入。`saveHistory` 此前每轮重写整块历史，PostgreSQL 侧是整块 JSONB 覆盖，写入成本随对话长度线性增长。新增带长度守卫的追加接口（守卫在 SQL 内求值，无需读取既有 blob，也无需 schema 迁移），纯追加的 run 只写新增消息，前缀被修复或守卫不匹配时回退全量替换。顺带修复 `AppendContext` 缺少 `CheckUserScope`、以及 `FileThreadStore` 会通过方法提升拿到不落盘的追加实现这两个问题。文件后端仍为全量重写。
- 2026-09-21：新增 `.gitattributes` 统一文本文件为 LF。此前 `core.autocrlf=true` 使 Windows 检出为 CRLF，gofmt 会把整个仓库判为未格式化，导致检查脚本在本地误报全部文件——正是脚本要防的"本地与 CI 不一致"。
- 2026-09-21：新增线程历史治理开关。`THREAD_HISTORY_MAX_MESSAGES` 限制单会话消息数（裁剪后经 `GuardToolPairs` 丢弃孤立 tool 结果），`THREAD_RETENTION` 按时间清理未使用的会话（写入时顺带执行，仅限当前用户以保持存储层"只触碰调用者命名空间"的不变量）。两者默认均为保留——静默丢弃用户对话属于产品决策。清理复用既有 `updated_at` 列，迁移 004 仅添加索引。实测：上限 4 时 4 轮对话只保留 4 条，默认配置保留全部 8 条。
- 2026-09-21：向量记忆文本加上长度上限。此前召回文本无任何截断，可单独超出整个记忆预算：实测 120 token 预算下注入 1242 token，且所有 KV 条目被跳过。改为按预算跳过装不下的条目（整条丢弃而非截断文本，避免历史片段被切成另一个事实），KV 条目随后使用剩余预算。顺带把 `FormatVectorResults` 里硬编码的 0.3 阈值改用 `retrieval.go` 中已定义却未被使用的 `minVectorRelevance`。两个回归测试均验证过"去掉上限即失败"。
- 2026-09-21：补上真实 PostgreSQL 验证。此前迁移 004 与新增的 SQL（`jsonb_array_length` 长度守卫、`messages || $new::jsonb` 追加、保留期 DELETE）只在 sqlmock 上验证过，而 sqlmock 只校验语句文本，不校验 PostgreSQL 是否接受与行为是否符合预期。新增 `TestPostgresThreadAppendAndPrune` 在真实库上覆盖三者，并直接断言迁移版本记录与索引存在；全量 `-race` 测试带真实数据库通过。README 补充复现命令，并注明迁移 004 在已有大量数据的库上会短暂阻塞写入（迁移执行器包事务，无法使用 `CREATE INDEX CONCURRENTLY`）。
- 2026-09-21：按消息数裁剪接入执行路径。`TrimByCount` 此前只有单测而无生产调用方，`MAX_MESSAGES` 是只读不用的配置；现接入 `compressMessages` 并在 token 裁剪之前应用，默认值由 30 改为 0 以保持既有行为（窗口设得过小会使摘要压缩永不触发，这一取舍已写入文档）。修复过程中发现一个隐蔽缺陷：`ModelContext` 原本以 `TokenInfo.Compressed` 为赋值条件，窗口生效时该标志为假，裁掉的列表被丢弃、模型仍拿到完整历史——改为按"压缩结果与完整历史的长度差"判定，并验证过恢复旧条件即测试失败。
- 2026-09-21：记忆召回不再依赖 KV 存储非空。`RetrieveRelevant` 此前在 KV 为空时提前返回，导致 KV 条目被清空而向量情景仍在的用户静默失去召回；现改为 KV 为空时仍查询向量存储，代价是尚无记忆的用户多一次 embedding 调用。
- 2026-09-21：审批恢复路径改为可流式。`ResumeContext` 接受与聊天相同的进度出口，决策接口 `stream=true` 时复用同一套 SSE 帧（默认 JSON 响应逐字节不变，现有客户端与测试不受影响）。顺带补上一个可观测性缺口：`HandleApproval` 直接调用 `executeTool` 而不记录任何事件，因此被批准的工具执行在实时流与运行事件里都是空白，现在会记录 `tool_call_start/end`。同时修正一个可靠性语义：claim 之后恢复执行与客户端连接解耦——此前浏览器断连会取消恢复，导致这次审批被消耗且无法重试（凭据已存在），并把可能已经发生的副作用记录为 `cancelled`。断连守卫通过注入缺陷验证：恢复旧行为（context 派生自请求）后测试报 `status=cancelled`。
- 2026-09-21：浏览器会话改用 HttpOnly Cookie。此前 sessionId 只存页面内存，刷新即退出登录，且凭证对 JS 可读（XSS 可窃取）。现在登录下发 `HttpOnly` + `SameSite=Strict` 会话 Cookie，登出使其立即过期；认证中间件接受 Cookie 而请求头优先，脚本与第三方客户端不受影响；前端不再读写 token，页面加载时探测会话恢复登录态。`SESSION_COOKIE_SECURE` 默认开启（浏览器只在 HTTPS 或 localhost 下保存 Secure Cookie，局域网 HTTP 演示需关闭）。实机验证：刷新后仍在应用内、`document.cookie` 读不到会话、登出后刷新回到登录页。
- 2026-09-21：新增文档 RAG。每用户可上传文档（粘贴或 `.txt`/`.md`），按段落切分为带重叠的片段，存入记忆存储的保留键从而自动获得三种后端持久化与用户隔离；检索时片段带 `[n]` 序号注入并要求模型标注来源，预算由 `DOCUMENT_BUDGET_TOKENS` 与记忆预算分开控制。文档使用独立的向量库——共用同一个 per-user 集合会让文档片段与情景记忆在同一个 top-K 里互相挤占。重启后首次检索从存储懒重建索引，同一机制顺带修复了情景记忆重启后失去向量召回的问题。实机验证：上传后上下文增加 57 token，删除文档后精确回到基线。
- 2026-09-21：修复 chromem 向量库包装层的三个缺陷（该包装层此前零测试覆盖）：`Query` 在 `topK` 大于集合文档数时报错且错误被吞掉，导致小语料下向量召回静默消失；空查询词报错，而 token 条正是用空查询调用；`DeleteUser` 误称 v0.7.0 无 `DeleteCollection`。另发现并处理一个尺度问题：相关度阈值 0.3 是为真实 embedding 标定的，hash 伪嵌入的分数压缩在 0.15–0.4，实测中文查询对正确文档只有 0.168——新增 `VECTOR_MIN_SCORE` 并在未设置时按提供方自动取值。

- 本文档原为 1960 行的详细开发计划文档，已在所有缺失项修复后精简为当前状态追踪格式。
- 原始开发计划的实现方案已全部落地，详见上方"已完成项"列表。
