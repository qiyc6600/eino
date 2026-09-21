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

---

## 已知限制（非阻塞）

| # | 限制 | 涉及模块 | 说明 |
|---|------|----------|------|
| 1 | HITL 为异步审批模式 | 模块 02 | 中断后 run 结束，通过独立 API 调用恢复，非"挂起等待"语义 |
| 2 | grep 使用硬编码 mock 数据 | 模块 03 | 搜索日志是示例场景，数据完全合成 |
| 3 | 线程历史无保留上限 | 模块 04/05 | 历史分离后线程存储永久保留完整对话，模型输入由压缩保证有界，但存储持续增长；缺少保留期与归档策略。PostgreSQL 后端下表现为表持续膨胀。**写入成本已通过追加式写入解决（见第 34 项），此处仅剩存储总量问题** |
| 4 | 压缩后的 checkpoint 体积上升 | 模块 02/04 | 真正压缩的 run 会同时序列化完整历史与压缩上下文两份 |
| 5 | 向量记忆文本无长度上限 | 模块 05 | `FormatVectorResults` 不截断，向量文本可能单独超出记忆预算（此时 KV 条目会被跳过，但向量部分已超） |
| 6 | `MAX_MESSAGES` / `TrimByCount` 未接入 | 模块 04 | 按消息数裁剪已实现并有单测，但无生产调用方，实际只走 token 裁剪与摘要压缩 |
| 7 | 审批恢复路径不流式 | 模块 02 | 审批决策接口返回一次性 JSON，恢复期间界面无进度；恢复耗时可能与一次聊天相当 |
| 8 | 刷新页面需重新登录 | 模块 01 | sessionId 仅存于页面内存，不写入 localStorage/sessionStorage |
| 9 | ~~前端无自动化语法检查~~ | 全局 | **已修复**：新增 `scripts/check.sh` 与 `.github/workflows/ci.yml`，前端语法作为独立 CI 任务运行。`go:embed` 不校验 JS，语法错误不影响任何 Go 测试却会让整个页面失去交互——该缺陷曾真实发生一次 |

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

- 本文档原为 1960 行的详细开发计划文档，已在所有缺失项修复后精简为当前状态追踪格式。
- 原始开发计划的实现方案已全部落地，详见上方"已完成项"列表。
