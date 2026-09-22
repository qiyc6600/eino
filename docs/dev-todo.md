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
| 45 | 相关度阈值按 embedding 尺度自动取值 | 模块 05 | ✅ | 真实 embedding 的相关结果在 0.6–0.9，hash 伪嵌入低一个量级；用同一个 0.3 会让 hash 模式下的中文检索完全失效。新增 `VECTOR_MIN_SCORE`，未设置时按提供方自动取值（**hash → 0.02，真实 → 0.3**）。该阈值是**噪声下限而非相关性判据**：相关中文片段实测 0.043–0.19、无关中文片段 0.000，所以 0.02 保留全部相关召回并丢掉纯碰撞噪声；英文无关片段仍有约 0.11（任意两段英文都共用虚词），那里靠排序区分。维度与阈值的实测过程见 2026-09-22 记录 |
| 46 | MCP 外部工具接入 | 模块 03 | ✅ | stdio 传输，适配层 `eino-ext/components/tool/mcp`（客户端 `mark3labs/mcp-go`）；启动时握手并发现工具，转成 `RegisteredTool` 注册。**注册插在 `WrapAllTools` 之前**，因此与内置工具走同一条 ACL 拦截；授权按角色配置（未授予的远端工具对所有人被拒，含 admin）；审批只认本地 `MCP_REQUIRE_APPROVAL`，不读远端 annotations；自动新增 `mcp_agent` 子 Agent 承载远端工具；单个服务器连不上只记日志不阻止启动，`Close()` 终止子进程。仓库自带 `cmd/mcp-demo-server` 供离线演示与测试 |

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
| 12 | hash 伪嵌入只做词面匹配 | 模块 05 | 默认 `EMBEDDING_PROVIDER=hash` 没有语义能力：它无法把"发布流程"和"怎么上线"联系起来，只认字面重合，所以**换个说法就召不回**。但**字面重合之内的排序是可靠的**（2026-09-22 修掉了维度太小导致的排序倒挂，见该日记录）。真正的语义检索需配置 `openai` 或 `ollama` |
| 13 | MCP 只支持 stdio 传输 | 模块 03 | 不支持 HTTP / SSE 传输与 OAuth 远端鉴权；远端工具与内置工具重名时跳过并记日志（不做自动命名空间化）；不对远端工具描述做提示注入检测（只保证描述不参与安全策略） |

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
- 2026-09-21：接入 MCP 外部工具（stdio）。工具在启动时从远端服务器发现并注册，**注册位置插在 `WrapAllTools` 之前**，因此与内置工具共用同一条 ACL 拦截——这是本次最关键的一点，顺序错了就会留下绕过权限的旁路，而角色表仍然正确、只有实际调用才暴露。授权按角色配置（内置角色权限是静态列表，覆盖不到运行时出现的工具，未授予的远端工具对所有人被拒，含 admin）；审批只认本地配置，不读远端 annotations（那是服务器自报的，不是安全边界）；远端 schema 通过 `ToolMeta.ParamsOneOf` 保真，避免嵌套参数被扁平格式丢掉；自动新增 `mcp_agent` 承载远端工具（未发现工具时不创建）；单个服务器连不上只记日志并跳过，与存储"失败即拒绝启动"刻意相反。仓库自带 `cmd/mcp-demo-server` 供离线演示与测试，指向官方服务器只需改配置。ACL 覆盖通过注入验证：把注册挪到包装之后，集成测试报 `ACL bypass` 并实际返回了远端数据。因引入适配层，`go.mod` 的 `go` 指令由 1.22 升到 1.23（该模块要求 1.23.0），CI 用 `go-version-file` 自动跟随。
- 2026-09-21：MCP 工具结果剥离协议信封。原样返回的 `{"content":[{"type":"text","text":"…"}]}` 让模型每次调用都付一遍脚手架 token，读起来也是噪声；现在只提取 `content[].text` 并按序拼接。**识别不出信封则原样返回**——图片或嵌入资源不含文本，清空会静默丢掉结果。错误路径同样处理，远端报错因此保留服务器原话（实测"笔记 n3 不存在"）而不夹带协议 JSON。实机验证：工具进度帧的 `result` 与最终回答里都不再出现信封。注入验证：把提取函数改成直通，8 个断言变红。
- 2026-09-21：清理 mock 模型的提取路径。`isPreferenceExtractionRequest` 找的标记串是"偏好提取助手"，而 `memory.Service.extractWithLLM` 实际发的是"用户记忆提取助手"，因此门控恒为假——后面那个手写的 `extractPreferences`（约 60 行）从未执行过，也没有任何测试引用。**修标记串是错的**：它一旦可达就会开始写错记忆（裸 "java" 命中 JavaScript、裸 "go" 命中 Django），让 mock 用手写匹配器替模型下判断。故删除该路径，并在调用点写明"mock 把提取提示词当普通对话回答，解析必然失败，走规则兜底"是刻意行为。新增两个测试钉住：mock 不得用 JSON 数组回答提取提示词；mock 模式下偏好仍被写入且 `source=user_stated`（规则路径而非 `llm_extracted`）。注入验证：把带猜测的匹配器加回去，两个测试都变红（第二个正是"JavaScript 被写成 Java"）。
- 2026-09-21：统一 token 估算器。`internal/memory` 自带一个私有估算器（CJK 按 1 token/字），而 `contextmgr.CountText` 按约 1/1.5，两者差约四分之一——400 的记忆预算实际只注入 305 token，且 UI token 条与配置对不上。现全部改用 `contextmgr.CountText`，与上下文窗口同一单位。**方向值得记下：这类不一致不会超预算，只会少给**，所以"输出不超预算"的断言抓不住它；新测试因此同时断言预算确实被用满（≥85%）。注入验证：把私有估算器只放回预算记账处，测试报"injected 305 tokens against a 400 budget"。
- 2026-09-21：修复 mock 读取侧的同类子串缺陷（实机复验时发现，非原报告项）。`detectPreferredLangFromMessages` 扫系统提示词找语言名，`Java` 排在 `JavaScript` 之前，于是存储为 JavaScript 的用户被告知"我将使用 Java"——mock 自相矛盾。改为**直接解析注入的那一行**（`preferred_language: <值>`）而不是猜名字；`detectLanguagePreference`（读用户消息）则改为最长名优先 + 短名要求整词匹配，避免 "go" 命中 Django、"java" 命中 JavaScript。实机验证：声明 JavaScript 后询问，回答与存储都是 JavaScript（修复前两处都是 Java）。注入验证：恢复子串扫描与去整词边界，两个测试都变红。
- 2026-09-21：偏好层三处加固（针对"长期记忆是否过于简单"的评估结论，见 9.2 / 9.3）。
  1. **持续性标记成为写入硬门槛**。此前规则提取器只看关键词，把对**当前这条回答**的要求也当成长久偏好：实测"请详细说明这个函数的作用"→`answer_style=detailed`、"这个回答太长了，请简短一些"→`answer_style=concise`、"简洁点"→同上，全部永久写入该用户的**全局**偏好。来回说还会让画像随单轮措辞震荡（旧值不断进 history）。现在两条路径都要求显式持续标记（以后/今后/默认/一直/始终/每次/都要/记住/我喜欢/prefer/always/by default）。划线的依据是**数据模型没有会话级作用域**——条目一写就是全局，所以只有明确要求全局的表述才记录；"请用简洁的方式回答"与"请简洁回答"意思相同，都不落库。注入验证：把表现词重新当触发条件，5 个瞬时用例全红。
  2. **核心偏好保留席位**。选择原本纯按分数排序，而偏好上限 `importance/5+近因`≈1.6、命中查询的情景可达 `0.4×0.8+2.0`=2.32，实测 8 条情景把 `preferred_language` 完全挤出（用户问部署脚本，助手不知道他用什么语言）。现在 `preference/identity/rule` 且 `importance≥4` 先无条件注入，剩余预算才竞争；`PutPreference` 按核心重要性写入（手写即重要性声明，API 无 importance 字段）。顺带避开一个陷阱：`FormatVectorResultsWithin` 的 `maxTokens=0` 表示**不限**，预算吃满时必须跳过向量召回而非传 0。注入验证：去掉保留席位，测试复现"完全被挤出"。
  3. **关键词重合对中文可用**。`tokenize` 把连续中文串当**一个** token，中文查询只能与完全相同的片段匹配——实测自然中文查询重叠度 **0**，查询感知通道对本项目主要语言彻底失效（英文 0.5 正常）。新增 `overlapTokens` 对 CJK 输出一元组与二元组供打分使用，**刻意不改 `tokenize`**：它喂 `hashEmbed`，而 hash 模式的阈值 0.1 是针对当前分词下的分数分布实测标定的，换分词会移动整个分布、必须重新实测。因此另有测试钉住"整串中文仍是一个 token"。注入验证：换回 `tokenize`，重叠度回到 0、测试变红。
  未做（评估中提到的其余项）：偏好未进向量索引（语义召回仍不覆盖偏好，需真实 embedding 才有收益，且要做去重）、数据模型仍无置信度/有效期。附带发现：`tokenize` 的 `isWordChar` 用 `r > 0x4e00`，"一"（U+4E00）因此被当成分隔符——这是意外的，但改它同样会动到 hash 向量，故保持原样并记录在此。
- 2026-09-21：偏好层再加两处（评估里排第 1、2 的项，见 9.2 / 9.3）。
  1. **来源权威性**。`sourceAuthority` 定级（user_stated 3 > consolidated 2 > llm_extracted 1）；值仍最后写入者胜出（后来的表述可能是改变主意，一刀切禁止覆盖会让真实变化永远写不进去），但 **`importance` 只能被不低于"历史最强证据"的写入修改**——否则一次默认重要性的 LLM 提取就能把手写偏好踢出核心档，而那正是保证它被注入的机制。`entryAuthority` 取当前 `Source` 与 `History` 链的最大值，**必须读 History 而不是只看当前 Source**：后者只能挡住第一次弱写入，第一次之后 Source 已变成 llm_extracted，第二次同级就畅通了。注入验证恰好证明了这点——朴素比较在第二次写入时失守，测试因此把第二次写入也纳入断言。`Source` 则如实记录当前值的写入者、不做保留：让标签粘住高权威会使其不再表示"这个值是谁写的"（第一版实现犯了这个错，被 `TestExtractAndSave_LLMOwnsTurnOverRules` 抓出）。
  2. **会话级作用域**。存储模型没有别的办法表达"只在这个会话里"，所以无持续标记的表现类请求此前只能二选一：全局记录（错）或丢弃。现在写入 `__thread_<threadID>__<key>`（复用保留键，自动继承隔离与后端切换），提取路由由 `memoryScopeFor` 统一判定"作用域与准入"——这两个问题其实是同一个：这句话管多远。身份类（语言/框架/编辑器/城市）无标记仍不写入，因为没有合理的会话级读法。注入按"会话级 → 核心偏好 → 向量召回 → 竞争"排序，会话级渲染在独立小节 `本次会话的偏好：`，避免模型把为一次对话提的要求读成关于用户的事实。**两条清理路径缺一不可**：`DeleteThreadContext` 按线程 ID 精确删除；保留期清理按条目自身 `UpdatedAt`（`PruneThreadsBefore` 只返回数量不返回线程集合，为它加返回值要动四个实现而收益为零）。注入验证：写入端忽略 scope → 会话级测试全红。
  实机验证（mock，全新进程）：一次性请求不进长期记忆；持续请求写入；手写偏好经弱写入后 importance 仍为 4；删除会话后用户级记忆完好。**未能实机验证的**：会话级条目的注入本身——它按设计不经 API 暴露，mock 也不读 `answer_style`，而 `/tokens` 只报总数没有分项，所以任何线程间比较都被对话历史混淆（我先写了这个比较，发现混淆后删掉了它）。这一项的证据是单测（断言 `本次会话的偏好` 小节在本会话出现、在别的会话不出现）加注入验证。
  仍未做：偏好未进向量索引；数据模型仍无置信度/有效期。
- 2026-09-21：修掉上一轮会话级作用域引入的三处问题（第三处是正确性缺陷，不是性能）。
  1. **检索每轮读两次命名空间**。`RetrieveRelevant` 已 List 一次，随后调用的 `ListThreadPreferences` 又 List 一次，而会话级条目本来就在第一次的结果里（只是被 `IsReservedKey` 挡在竞争池外）。PostgreSQL 的 `List` 是 `SELECT data FROM agent_memories WHERE user_id=$1`，会把每一行含文档片段正文都取回反序列化，所以这是每轮把整个语料读两遍。改为纯函数 `scopedEntriesIn` 作用于已取到的切片。**顺带消掉一个一致性瑕疵**：两次独立读取之间若有并发写入，用户级与会话级会来自不同快照，注入内容自相矛盾。测试用计数存储包装器断言恰好一次。
  2. **保留期清理每轮全量扫描**。开启 `THREAD_RETENTION` 后清理在写路径每轮执行一次，而保留期以小时/天计——等于每轮读一遍整个命名空间去删"还不可能过期"的条目。加按用户节流（`DefaultThreadPruneInterval`，默认 1 小时，`SetThreadPruneInterval` 可改，0 = 每次），状态在成功清理后写入，失败的下轮重试。线程存储那侧不受影响：`PruneThreadsBefore` 是带索引的单条 DELETE，不把数据取回应用。
  3. **清理按创建时间判断年龄，会删掉活跃会话的条目**。这是正确性缺陷：注入时的强化只更新 `LastAccessedAt`，不动 `UpdatedAt`，所以一个每轮都在用、但创建于保留期之前的会话级偏好会被静默删除。`scoreEntry` 早已按"最后触碰"计算近因，清理却没跟上。改为同一取法（`LastAccessedAt` 优先）。
  另补两处**集成接线的测试**——`DeleteThread → DeleteThreadPreferences` 与 `pruneThreads → PruneThreadPreferences` 此前完全没有测试，而"忘掉这次调用"正是最容易犯且最难发现的错（症状是慢性泄漏，不是失败）。保留期那条用退化窗口（1ns）让所有条目都算陈旧，避免跨包改写时间戳。四处注入验证均确认测试会变红（恢复第二次读 → 报"读了 2 次"；去掉节流 → 第二次清理未被抑制；按创建时间 → 活跃条目被删；去掉接线 → 条目残留）。
- 2026-09-21：前端 API 契约审计（逐端点核对前端读取的字段与后端 JSON tag）。**结论：绝大多数读取都是对的**——`/api/models` 的 `ModelProfileInfo` 有完整 tag、SSE 帧的 `contextTokens`/`actualTokens`、interrupt 的 snake_case、`/api/memory` 的 snake_case、决策请求体全部吻合。特别记录一处**看似不匹配其实正确**的：`/api/approvals` 返回的字段是大写的（`InterruptID`/`Status`/`ThreadID`…），因为 `hitl.ApprovalRequest` 大多数字段没有 JSON tag，Go 默认输出 Go 字段名——前端读大写是对的，不要"顺手改成 snake_case"。
  修掉三处真问题：
  1. **前端抄了一份后端 RBAC 表**。`renderTools` 按角色硬编码工具名，`renderUserInfo` 硬编码分母 6。MCP 接入后远端工具是启动时授予角色的，于是 admin 的工具面板把 `read_notes`/`delete_note` 显示成 🚫 不可用，计数也停在 "6/6"。`/api/auth/me` 本来就返回 `tools`，改为直接用它，分母用 `/api/tools` 的实际条数。实机验证：admin 显示 8/8 且两个 MCP 工具 ✅，visitor 显示 3/8 且 MCP 工具 🚫。
  2. **非流式回退把 `error`/`cancelled` 误标成越权**。流式路径正确区分了四种状态，回退路径只判断 `interrupted`/`completed`，其余一律渲染成 `acl-denied`——而回退路径恰恰是在流式已经失败时才走的。改为与流式路径同样的区分。
  3. **回退路径读了一个不存在的字段**：`data.memory`。非流式响应是 `ChatRunResult`，没有 memory 列表（只有 SSE 的 done 帧会附加）。它有 `setTimeout(refreshMemory, 500)` 兜底，所以没有可见症状，但读取是死代码。顺带删掉前端对 `error` SSE 事件的处理分支——服务端只发 `chunk`/`tool_call`/`done`，失败通过 `done.status` 表达，那个分支永远不可达（并在帧分发处写明帧集合）。
  新增 `TestIntegration_FrontendContract` 与 `TestIntegration_FrontendSSEContract`：用真实路由驱动每个端点，断言前端读取的每个键确实存在，**并且该键确实出现在 app.js 里**（防止清单腐化成断言一个没人用的契约）。这个自检第一次运行就抓出我列表里三个前端根本不读的键（`created_at`、`superseded_at`、`last_accessed_at`）。注入验证：把 `risk_level` 改名 → 报"does not contain risk_level, but the page reads it"；把 `has_api_key` 的 tag 改名 → 同样报错。
  另记录一处**未改的观感问题**：`acl-denied` 这个消息角色被用于两处通用请求失败（`pushMessage(threadId, 'acl-denied', '❌ 请求失败…')`），红色气泡的视觉效果合适但名字误导。改它要动 CSS 与调用点，属观感而非契约问题，故留着并记录。
- 2026-09-21：补齐契约测试的缺口（被问"全盖完了吗"时自查发现）。上一提交只钉住了**响应**字段，以下都只是"读代码确认过"而没有守卫，现补上：
  1. **请求体**（`TestIntegration_FrontendRequestContract`）。登录、聊天（含非流式回退的四个字段）、记忆写入、文档上传、切换模型、审批决策——用前端自己的载荷键名发一遍并断言服务端接受。**这个方向比响应方向更容易悄悄坏掉**：改一个请求字段名会让处理器解出空字符串，调用要么被拒要么静默做错事，而所有响应断言仍然全绿。
  2. **interrupt 对象**的键（`interrupt_id`/`tool_name`/`message`/`arguments`/`type`），通过一次真实中断的聊天取到；`plan` 是 omitempty（节点级中断才有），只做"前端确实读它"的方向。
  3. **`tool_call` 帧的字段**与 `phase` 取值集合。同时把 SSE 测试的输入从"你好"改成"计算 1+1"，因为问候只路由不调工具，不会产生 `tool_call` 帧。
  4. **`/api/models/switch` 的响应** `current_model`。
  自查过程中发现**自己的断言比以为的弱**：第一版只保留每种帧的**第一个**载荷，而 `tool_call` 帧是 route→start→end 顺序，改 start 的键不会被看到——注入验证因此"通过"了。改为收集每种帧的**全部**载荷逐个检查后，注入立刻被抓出（报 frame #1 缺 `tool`、phase 变成未知值）。这条记下来是因为它说明注入验证的价值：如果只跑一遍绿灯就收工，这个弱断言会一直留着。
  注入验证（两条）：把 `risk_level` 改名 → 报 "does not contain risk_level, but the page reads it"；把 `has_api_key` 的 tag 改名 → 同样报错。
- 2026-09-22：修 `isWordChar` 的 `r > 0x4e00`，并顺带查出 hash 伪嵌入的维度太小（这是评估里排第 3 的项）。
  1. **`isWordChar` 两个方向都错**。U+4E00 正是"一"，`>` 把它排除在外——`tokenize("一二三")` 得到 `["二三"]`，含"一"的文档直接丢掉这个字。而阈值以上的**所有**码位都算词字符，于是全角标点被粘进 token：`"写，注意先切流量"` 成了一个词。改用 `unicode.IsLetter/IsDigit`，与 `contextmgr` 里同名函数的写法一致（两处现在定义相同）。
  2. **重测分数分布时发现真正的元凶是维度**。修完分词后中文排序反而更差，于是做了维度扫描：哈希特征碰撞后无法区分，而一个 125 字符的中文片段贡献约 140 个特征（每词一个 + 每对相邻字符一个），在 `dim=128` 下占用率 **73%**，碰撞主导相似度。实测九组配对（中英文、长短文本）：128 维下**无关片段反超相关片段**（最差 −0.28），2048 维仍有一组倒挂（−0.060），4096 维一组（−0.025），**8192 维全部无倒挂**，16384 维无进一步改善。倒挂比噪声更糟——它会主动误导检索。改为 `hashEmbeddingDim = 8192`（占用率 2%，代价是每条索引 32KB 与 O(dim) 余弦）。
  3. **阈值随之重标定**：`hashEmbeddingMinScore` 由 0.1 改为 **0.02**。8192 维下相关中文片段落在 0.043–0.19，无关中文片段实测 **0.000**，所以 0.02 保留全部相关召回、丢掉纯碰撞噪声。旧的 0.1 是针对 128 维那个更嘈杂的分布标定的，会连同噪声一起丢掉相关的中文召回（0.043）。**并明确写下它是噪声下限而非相关性判据**：英文无关片段仍有约 0.11（任意两段英文都共用虚词），那里靠排序区分而不是阈值。
  新增三个测试：`tokenize` 的四组行为（"一"保留、全角标点分隔、ASCII 分词、全角句号分隔）；**九组配对的排序守卫**（相关必须高于无关——这条会在有人调小维度时失败）；**片段占用率守卫**（片段特征数占维度不得超过 10%，128 维时是 73%）。
  注入验证：把 `dim` 改回 128 → 排序守卫报出 `zh short deploy: the unrelated passage outscored the relevant one (0.3111 vs 0.0333)`，占用率守卫报 `a chunk fills 73% of the 128 dimensions`；把 `isWordChar` 改回 `r > 0x4e00` → tokenize 测试报 `tokenize("一二三") = ["二三"]`。补 fixture 时发现排序守卫在 128 维下**原本不会失败**（长文本配对恰好没倒挂），于是补上当初大倒挂的两组短文本配对，让这条守卫独立成立而不是只靠占用率那条。
  实机验证：阈值日志变为 `Vector relevance cut-off: 0.02 (embedding provider "hash")`，上传两份文档→切分→索引→提问全流程正常。**未能实机验证排序本身**——注入内容不经任何 API 暴露，且只有两三个片段时全都会被注入（topK=5、预算 800），排序无从观察；这条的证据是单测加注入验证。

- 2026-09-22：清掉 `internal/hitl` 里**无生产调用且是隐患**的那部分审批 API（该包 5 个源文件原先只有 6 个测试，而它是唯一一处"出错就让危险操作未经审批执行或重复执行"的地方）。删掉四个方法，每个都满足"生产零调用 + 一旦被调用就绕过活路径的守卫"：
  - `InterruptManager.Resume` + `Service.Approve`：**没有属主校验**（它把自己刚查到的 `req.UserID` 传回去，所以校验恒真）、**没有 `Phase == "running"` 检查**（崩溃后恢复会重复执行；活路径会拒绝并提示"上次执行结果未确认"）、**没有 `len(req.Result) > 0` 短路**（忽略已记录的结果）。另外它在数据库路径上**会空指针崩溃**：postgres 的 `Claim` 在行不存在时返回 `(nil, false, nil)`，下一行就解引用 `claimed.Decision`——活路径（`Runner.ResumeContext`）对 `req == nil` 有显式处理，这条没有。
  - `InterruptManager.GetPending`：`userID == ""` 会**绕过过滤返回所有用户的待审批**（零调用，但它是 `/api/approvals` 背后的包里的一个无守卫跨用户读取）。顺带把 `GetPendingForUserE` 里的 `userID == "" ||` 分支去掉——它只为 `GetPending` 而存在。
  - `Service.ExecuteApprovedTool`：**第二套内存版幂等**（`map[runID:toolCallID]`），与活路径的持久化幂等键不是一回事。它唯一的使用者是自己的测试，那条测试因此给"幂等性"制造了虚假信心；真正的幂等覆盖在 `tools/order_test.go`、`tools/email_test.go` 与真实库的 `postgres_test.go`（删前已确认）。
  重写 `service_test.go` 改走受守卫的 API（`RequestToolInterrupt` / `ClaimApproval` / `CompleteApproval`），并补上原先只存在于 Runner 层的四条守卫断言：**认领只能有一个赢家**（同决定、反决定都不行）、**认领必须属于本人**（非属主拿不到请求体，也不确认其存在）、**完成必须持有当前 claim**（过期 token 被拒）、**空 userID 不等于所有人**。四条注入验证均确认测试会变红（去掉单一赢家守卫 → "a second claim must not win"；去掉属主校验 → "another user claimed an approval they do not own"；去掉 token 守卫 → "completing with a stale claim token must be refused"；恢复 `userID == ""` 分支 → "an empty userID must not return other users' approvals"）。
  **探测方法上犯过一个错并已修正**：我第一版死代码扫描用的是定义文本（`Service) GetApproval(`）而不是调用形式（`.GetApproval(`），于是把被 HTTP 处理器使用的 `GetApproval` 也报成"无调用者"，差点删掉活代码。改用 `.方法名(` 重新扫描后才得到可信结论。
  仍留在包里但**未删**的死方法（报告而非动手，属风格取舍而非隐患）：`ExecuteWithApproval`（另一种创建中断的写法，Runner 自己构造）、`ExecuteRejectedTool` 与 `tools.HITLDisapprovedResult`（生产代码在 `HandleApproval` 里自己拼拒绝文本，从不产生 `disapproved` 状态）、`RequestNodeInterrupt`（Runner 直接构造节点中断请求，绕过了这个服务方法）、`SaveApproval` / `InterruptManager.Save`（`SaveContext` 的薄包装）。后两组值得单独决定：要么删掉，要么让 Runner 改走它们。

- 2026-09-22：收掉 `internal/hitl` 里剩余的**生产死方法**（上一提交报告、本次动手）。每个都有一个生产实际在用的等价路径，删掉不留缺口：
  | 删掉的 | 生产实际走哪条 |
  |--------|----------------|
  | `Service.RequestNodeInterrupt` | Runner 在 `stepped_runner.go` 里**自己构造**节点中断请求，因为它同时要存完整的 `SteppedRunState`；服务方法只能存一个桩状态 |
  | `Service.ExecuteWithApproval` | 工具中断由 dispatch 路径经 `RequestToolInterrupt` 创建 |
  | `Service.ExecuteRejectedTool` + `tools.HITLDisapprovedResult` | `HandleApproval` 自己拼 `"用户拒绝执行该操作：" + reason` 作为工具消息回灌给模型（`stepped_runner.go:622`）。**没有任何代码产生或读取 `disapproved` 状态**，删掉的只是一个没人产生的状态构造器 |
  | `Service.SaveApproval` + `InterruptManager.Save` | `SaveApprovalContext` / `SaveContext`——带 context 的版本，`CheckUserScope` 需要它 |
  删前逐个核实过引用：`RequestNodeInterrupt` 零引用；其余只在自身测试或一个测试调用点里出现（`execution_test.go` 已改用 `SaveApprovalContext`）。另确认服务方法里那个**桩 checkpoint 不会被误当成恢复快照**：它没设 `Snapshot`，而 `LoadSnapshot` 要求 `cp.Snapshot` 为真（`memory/service.go:961`），所以只是次无用写入而非缺陷。
  删完 `internal/hitl` 从 6 个方法（其中 4 个无守卫）变成 12 个全部有生产调用或有测试守卫的方法。`grep` 确认 `ExecuteWithApproval|ExecuteRejectedTool|RequestNodeInterrupt|HITLDisapprovedResult|disapproved` 在整个 Go 代码里**零残留**。
  过程记录：这次用正则按函数名删代码，第一版正则对多行函数体截断错误、留下了残片（构建报 `expected declaration, found payload`），改为精确文本匹配后正常。教训与上一轮同类——**用文本模式做结构性修改时，先确认匹配粒度**。
  验证：check.sh 全绿，集成测试在真实 PostgreSQL 下通过。

- 2026-09-22：补上请求体大小上限，并把所有 JSON 解码统一到一个入口（"输入校验"这一层里唯一真正缺失的一项）。
  **问题**：全代码库没有 `http.MaxBytesReader`，11 处 `json.NewDecoder(r.Body).Decode(...)` 都会把 body 完整读进内存。文档那个 200,000 字符上限（`maxDocumentChars`）**是在解码之后才检查的**——上限防住了存储，没防住内存。最直接的后果不是"超大文档被拒"，而是"超大 `name` 被接受并入库"：`name` 没有字段级上限，实测一个 5MB `name` + 2 字符 `content` 的 body 在没有上限时返回 **200**（文档已存）。
  **改动**：`maxRequestBytes = 4 << 20`（4MB）+ `limitRequestBody` 中间件**包住整个 mux**，因此同时覆盖受保护路由与唯一无需会话的 `/api/auth/login`。数值由最大合法 body 推导：文档上限 200k 字符约 600KB（UTF-8），若客户端对每个字符用 `\uXXXX` 转义则约 1.2MB；4MB 留出字段与信封余量。新增 `decodeBody(w, r, dst)` 统一解码，并把**超大与畸形区分开**：超限返回 413（客户端该减数据重试），畸形返回 400（重试无意义）——原来两者都是 400。
  顺带修掉一处**忽略解码错误**的地方：`CreateThread` 直接 `Decode(&req)` 不看错误，畸形 body 会静默走到"生成随机 threadId"分支，创建一个没人要的线程。
  **测试**：`TestLimitRequestBody`（中间件单元，含"超限 body 不会被静默截断成一个更短但合法的文档"）、`TestMaxRequestBytesAccommodatesTheDocumentCap`（推导守卫：上限必须高于文档上限的最坏编码，否则合法上传会被拒）、`TestIntegration_RequestBodyBound`（走真实路由，因为中间件的单元测试**发现不了"router 不再装它"**）。
  三处注入验证：router 不再包装 → 受保护路由返回 **200**（5MB name 入库）、登录路由 401 而非 413；去掉 413 分支 → 变成 400；上限调到 4KB → 推导守卫报 `the body limit (4096) is not above the worst-case encoding of a document at maxDocumentChars (1200000)`。
  过程中修正了自己测试的一个弱点：第一版集成测试用 5MB 的 `content`，而字段级上限也会返回 413，所以它**无法区分"上限生效"与"字段检查生效"**——注入"去掉上限"时受保护路由那条照样通过（只有登录路由抓到）。改成"body 超限但每个字段都合法"（超大 `name` + 合法 `content`）后才成为真正的判别性测试。
  未做（评估为低价值）：入站 `Content-Type` 校验（`json.Decode` 本就不看它，不解决实际问题）、chat 消息长度上限（token 预算已处理）、路径参数的 URL 解码（一致性而非安全）。

- 2026-09-22：修会话在固定时刻失效（用户实机报 `unauthorized: missing session`）。
  **症状分辨**：这条消息来自 `extractSessionID` 返回空——**请求里根本没有凭证**；若是凭证无效，消息会是 `unauthorized: invalid session`。所以不是服务重启（那会是 invalid），而是浏览器没带 Cookie。
  **根因**：服务器端会话每次校验成功都滑动续期（`SESSION_TTL`，默认 30m），但 `SetSessionCookie` **只在登录时调用一次**，Cookie 的 `MaxAge` 在浏览器侧是绝对时间。于是浏览器在**登录后固定 30 分钟**无条件删掉 Cookie，与用户是否一直在用无关——文档里写的"滑动续期"在浏览器路径上被这个固定 Cookie 寿命完全架空，活跃用户照样被登出。
  **修复**：`AuthMiddleware` 在每次校验成功后重发 Cookie（在 `next.ServeHTTP` 之前写头，否则 handler 已经写出 body 就太晚）。只对 Cookie 携带的会话重发——用 `Authorization` 头的客户端没有 Cookie 可滑动，给它发一个会凭空造出浏览器凭证。`SessionCookieConfig` 从 `httpapi` 移到 `auth`（中间件在 auth 包里需要它），`httpapi` 保留**类型别名**，因此现有引用与测试零改动。
  **前端**：401 此前被当作聊天里的错误文本显示（`❌ 请求失败：unauthorized: ...`），用户只能自己猜要重新登录。新增 `handleSessionExpired()`：隐藏主界面、显示登录页、提示"会话已过期，请重新登录"；`api()` 与两个流式路径（`chatStream`/`decideApproval` 直接 fetch、读流，绕不过 `api()`）都接上；`deleteThread` 此前**完全忽略响应**，会话失效时本地删除看着成功而服务端线程还在，也一并修掉。登录成功时清空提示并重置"已处理"标志，否则第二次过期不会再提示。登录接口本身豁免——密码错误也是 401，不该被说成会话过期。
  **验证**：`TestAuthMiddleware_SlidesTheSessionCookie`（单元：Cookie 会话拿到全额 TTL 的新 Cookie、保留 HttpOnly/Secure/SameSite、Authorization 头不拿 Cookie、被拒请求不拿 Cookie）。实机三项：curl 确认带 Cookie 的认证请求返回 `Set-Cookie ... Max-Age=120` 而用 Authorization 头的不返回；**时序验证**——TTL 设 120 秒，持续活动下 160 秒后仍认证成功（修复前会在第 120 秒被浏览器丢 Cookie）；浏览器里会话失效后发消息 → 主界面隐藏、登录页出现、提示文案正确，重新登录后提示清空且界面恢复。

- 2026-09-22：修 mock 模型两处**编造结果**（用户实机报 `计算1+4` 得到 `1 + 1 = 2`，且回答里夹带北京天气）。
  用完整 app 复现后（不是读代码猜），一轮输入 `计算1+4` / `计算 1+4` / `计算 1 + 4` / `计算 23 乘以 19` 得到：
  `1 + 1 = 2；1 + 1 = 2；1 + 4 = 5；1 + 1 = 2`——两个缺陷叠在一起。
  1. **`extractMathExpr` 解析失败时硬编码返回 `"1 + 1"`**。旧解析器按空白切分且要求运算符自成一段，所以"计算1+4"（运算符两侧无空格）什么都找不到，于是去算 1+1，并把 `1 + 1 = 2` 当作用户问题的答案报出来。**编造一个看起来合理的结果比失败更糟**：用户必须注意到表达式被换掉才能发现。改为解析不出就返回空串，让计算器报 `unsupported expression`——可见的失败。重写解析：先把"乘以/除以/加/减/×/÷"归一成符号，再过滤出表达式字符（运算符两侧补空格，于是 `1+4` 与 `1 + 4` 等价），最后取最长的"操作数/运算符交替"片段。要求运算符两侧都是操作数，这同时挡掉从词里残留的运算符（"除了计算 3*2" 归一后开头是个孤立的 `*`，不构成表达式）。
  2. **`summarizeToolResults` 汇总整个线程的工具结果**。消息列表带着全部历史，于是每轮回答都重复之前所有工具结果——一个早先的天气查询出现在后来算术问题的回答里。改为只取最后一条 user 消息之后的工具结果（一次 run 的工具结果必然跟在发起它的 user 消息之后），子 Agent 与 supervisor 两条路径都适用。
  新增测试：`extractMathExpr` 的表驱动用例（无空格/有空格/中文运算符/链式/小数，以及"无表达式必须返回空"）；一条**属性测试**（一批无法解析的输入都不得产出可用表达式）；`summarizeToolResults` 只汇总本轮（断言本轮结果在、上一轮的天气不在、且不能因此变成空）。两处注入验证：恢复 `return "1 + 1"` → 属性测试报 `extractMathExpr("计算") invented "1 + 1"`；恢复汇总整线程 → 报 `an earlier turn's tool result leaked into this answer: ...weather：北京 23°C；calculator：1 + 4 = 5`。
  实机验证（同一线程连续三轮）：`北京天气怎么样` → 只有天气结果；`计算1+4` → `1 + 4 = 5`；`计算 23 乘以 19` → `23 * 19 = 437`，各自只报本轮结果。

- 2026-09-22：修"删除a1008"被路由到计算器（用户实机报 `math_agent → calculator：unsupported expression:`）。
  **根因**：**"删除"里含有"除"，而"除"是 math_agent 的关键词之一**。关键词按长度竞争，所以"删除订单A-1008"（"删除订单" 12 字节）能赢，但"删除a1008"只命中"除"（3 字节）→ 抢到 math_agent → 调用 calculator 时表达式为空。同类缺陷本会话出现过两次（`isWordChar` 的 `r > 0x4e00`、mock 的语言子串匹配）：**单字中文关键词匹配进无关词**。全表检查后只有 math 的 `加/减/乘/除` 有此问题（"加"在增加/添加里，"减"在减少里，"乘"在乘客里），其余关键词都是多字且具体。
  注意：**能看到这个错，正是因为上一轮把"编造 1+1"改成了"报空表达式错误"**——修复前它会在删除请求上算出 `1 + 1 = 2`，更难发现。
  三处改动：
  1. **去掉四个歧义单字关键词，改由"解析出的表达式"决定**。`extractMathExpr` 已经能精确判断有没有算式，它作为独立信号参与竞争（用表达式长度参与长度比较）。这样"3乘5"仍路由到 math，而"排除这个选项"不再被 math 抢走。
  2. **补上"删除"关键词**。只去掉歧义词还不够："删除a1008"此前只能命中"删除订单"（不匹配），去掉"除"后会落到"无工具"——用户想删订单却得到帮助文本。加上"删除"（2 字）后，更长的"删除订单"/"删除笔记"仍按长度优先。
  3. **订单号解析改为在整条消息里扫描**，不再要求"某个空白分隔的字段恰好是 A-1001"。旧实现看 `w[1]` 是否为 `-`，所以"删除a1008"（一个字段）什么都看不到。现在连字符可选、大小写不敏感，并归一成存储形式 `A-1008`——这正是人们实际的输入方式。
  新增测试：删除类请求不得路由到计算器（五种写法）；数学路由仍覆盖（含"3乘5"/"10除以2"这类只有中文运算符的）；**四句"含运算符字符但无算式"的句子必须不被任何工具认领**（"排除这个选项"/"增加一点库存"/"减少一些错误"/"乘客信息在哪里"）——这一组才是真正把歧义词挡在表外的守卫；`extractOrderID` 表驱动（粘连、无连字符、小写、B 前缀、非订单文本）。
  **过程中发现自己的测试不具判别性**：第一版删除路由测试在注入"把 除 加回关键词"后**照样通过**——因为"删除"（6 字节）本来就比"除"（3 字节）长，关键词竞争已经赢了。真正起作用的是加上"删除"，去掉歧义词是防御性的。补上那四句无竞争关键词的用例后，注入才被抓住（四句全部报 `matched "math_agent"`）。这是本会话第三次"测试通过但没测到东西"（前两次：契约测试只查每种帧的第一个、请求体上限用超大 content 与字段上限混淆）。
  三处注入验证：加回歧义词 → 四句歧义句全部命中 math_agent；关掉表达式信号 → "3乘5"/"10除以2是多少" 无路由；恢复旧订单号解析 → 五种写法全部解析为空。
  端到端（实机，走完审批）：`删除a1008` → interrupted → `delete_order` → `{"order_id":"A-1008"}` → 批准 → `订单 A-1008 已删除` → 订单列表里确实没有 A-1008。

- 2026-09-22：修订单表格错位（用户实机报"查看订单表格异常显示"）。两个独立原因叠加。
  1. **按字节补齐而不是按显示宽度**。`formatOrderList` 用 `%-8s` / `%-19s` 画表格，而 Go 的 `%-Ns` 补的是**字节**：一个中文字 3 字节却只占 2 列，所以含中文的每一行都短了。边框和表头还是手写的字面量，于是三者各不相同——实测边框 **58** 列、表头 **60** 列、数据行 **61** 列，`│` 在每行落在不同位置。改为 `displayWidth`（CJK/全角/emoji 记 2 列）度量，**列宽从内容算出来、边框用同一组数字画**，因此不可能再漂移；单元格超宽时按列宽截断加省略号，不会把边框挤走。
  2. **聊天气泡用的是比例字体**（`-apple-system, ... sans-serif`），比例字体下任何 ASCII 表格都对不齐——补齐修好也看不出来。这是**表现层的问题**，所以修在前端：消息含制表符（U+2500–U+257F）时加 `msg-pre` 类用等宽渲染。判定放在渲染器而不是让工具输出 UI 标记——工具不该知道 UI 怎么渲染。
  验证：`TestFormatOrderList_ColumnsLineUp` 断言表格每行显示宽度一致（五组用例：纯 ASCII、中文、最宽行、超长描述被截断、emoji 状态），`TestFormatOrderList_KeepsEveryOrder` 断言排版不会丢行，`TestDisplayWidth`/`TestPadRight` 覆盖度量与截断。注入验证：把列宽换成手写字面量 → 报 `line 7 is 60 columns, want 61`。
  浏览器实机（读 localStorage 里真实的已存消息度量行宽）：**修复前的两条表格分别是 58/60/62/66 与 58/60/62/63/66，修复后新生成的一条是 60（全部一致）**；渲染出的消息带 `msg-pre`、computed font-family 为等宽，截图确认列对齐。
  **注意**：已存消息的文本是客户端缓存的，修复只影响新生成的输出——用户要**重新查询一次**才能看到对齐的表格（旧气泡里仍是旧文本）。

- 2026-09-22：让已打开的标签页能发现服务端换版（承接上一条"横线超出消息框"的排查）。
  **背景**：那个症状的根因是标签页在跑修复前的 JS。项目已经给 UI 设了 `Cache-Control: no-cache, must-revalidate`（重载必定拿到新资源），`router.go` 里也有注释承认"陈旧的 app.js 会静默禁用新的前端功能"——但**一个已经打开的标签页永远不会知道服务端变了**，会一直跑旧代码。每次服务端升级都会再撞一次。
  **实现**：`web.AssetVersion()` 对嵌入资源算 sha256 内容哈希（按文件名排序，避免遍历顺序影响），12 位十六进制。
  - 服务端把哈希注入 `index.html`（`{{ASSET_VERSION}}` 占位符，**全部替换**——页面里有三处，只替换第一处会让资产 URL 字面指向 `?v={{ASSET_VERSION}}`），并设 `X-App-Version` 头。
  - `/healthz` 返回 `assets` 字段。选它是因为它**无需认证且已存在**，不需要新路由；页面因此可以在会话过期后仍能发现换版。
  - 前端读取自身 meta 标签里的版本，每 60 秒与 `/healthz` 对比，不一致就在页面顶部显示"服务端已更新，当前页面仍在运行旧版本 [刷新]"。提示条放在 `#mainApp` 之外，所以登录页也能看到——会话过期后又停留很久的标签页正是最容易撞上这种情况的。
  - 顺带**去掉两个手工维护的缓存参数**（`styles.css?v=13`、`app.js?v=26`）：它们只有记得改才有效，而两者的数字本来就已经不一致了。现在两处都用内容哈希。
  **验证**：`web` 包测试（版本稳定且为 12 位十六进制、三个资产都嵌入、index.html 至少三处占位符且没有残留的手工版本号）；集成测试（页面带自身版本、占位符已被替换、两个资产 URL 带当前版本、`X-App-Version` 头、`/healthz` 的 `assets` 与 `no-store`、显式 `/index.html` 路径也是 `text/html`）。
  **测试抓到我自己的 bug**：`strings.Replace(..., 1)` 只替换第一处，meta 被替换而 script 标签保留占位符——集成测试报"the placeholder was served unsubstituted"。注入验证：改回 `Replace(..., 1)` → 同一条断言失败。
  实机端到端（保持标签页不刷新）：加载后 `served=9a6b80e7af67`、提示隐藏 → 改一个资产文件、重建、重启（`/healthz` 变成 `aba423c4d729`）→ 在未刷新的标签页里跑检查 → 提示条出现（截图确认，登录页上同样可见）→ 点刷新 → `served=aba423c4d729`、提示消失。

- 本文档原为 1960 行的详细开发计划文档，已在所有缺失项修复后精简为当前状态追踪格式。
- 原始开发计划的实现方案已全部落地，详见上方"已完成项"列表。
