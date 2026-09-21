# Agent Eino Demo

基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 构建的全功能 Agent 框架演示项目，涵盖登录权限、人机协同、多 Agent 调度、上下文管理与记忆系统五大模块。

---

## ✨ 核心特性

| 模块 | 能力 |
|------|------|
| 🔐 登录与权限 | Session 认证、TTL 滑动续期、RBAC 多角色、工具级 ACL 统一拦截、多用户数据隔离 |
| 🤝 人机协同 | 工具级中断（高危操作审批）、节点级中断（执行计划审批）、Checkpoint 状态恢复、SSE 实时进度与中途停止 |
| 🧠 多 Agent | Supervisor 三子 Agent 路由、ReAct 循环（MaxStep=20）、6 个示例工具、答案逐 token 流式输出 |
| 📐 上下文管理 | 按消息数/Token 数裁剪、LLM 摘要压缩、Tool 配对保护、完整历史与模型上下文分离、工具结果长度上限 |
| 💾 记忆系统 | 短期记忆 CheckpointStore + 长期记忆 MemoryStore、跨会话偏好提取、向量检索、记忆开关（禁止记忆） |

---

## 🚀 快速开始

```bash
# 克隆项目
cd agent-eino-demo

# 方式一：Mock 模式（首次启动必须显式设置管理员）
BOOTSTRAP_ADMIN_USERNAME=admin \
BOOTSTRAP_ADMIN_PASSWORD='replace-with-a-long-password' \
go run ./cmd/server

# 方式二：接入真实 LLM
MODEL_PROVIDER=openai \
OPENAI_API_KEY=sk-xxx \
OPENAI_MODEL=gpt-4o \
BOOTSTRAP_ADMIN_USERNAME=admin \
BOOTSTRAP_ADMIN_PASSWORD='replace-with-a-long-password' \
go run ./cmd/server

# 浏览器访问
open http://localhost:8080
```

### 初始管理员

项目不再内置账号或密码。空用户库首次启动时必须同时设置 `BOOTSTRAP_ADMIN_USERNAME` 与 `BOOTSTRAP_ADMIN_PASSWORD`，密码至少 12 个字符。账户创建后不会在后续启动中覆盖其密码或角色；使用 PostgreSQL 持久化用户时，可以在确认管理员已落库后移除这两个引导变量。

需要演示 visitor 权限时，先用管理员通过 `POST /api/users` 创建 visitor 角色账户。visitor 调用 admin 工具时会被 ACL 拦截，拒绝结果回灌 LLM 使其重新规划。

### 会话如何传递

浏览器端使用 **HttpOnly + SameSite=Strict 的会话 Cookie**（`agent_session`）：页面脚本读不到它，XSS 拿不到会话，刷新页面也不会退出登录。非浏览器客户端仍可用 `Authorization: Bearer <sessionId>` 请求头，两者同时存在时**请求头优先**。

Cookie 的 `Secure` 标记由 `SESSION_COOKIE_SECURE` 控制，默认开启。浏览器只在 HTTPS 或 localhost/127.0.0.1 下保存 Secure Cookie，因此用普通 HTTP 在局域网 IP 上演示时需要设为 `false`，否则 Cookie 会被静默丢弃、每次请求都显示未认证。

---

## 🏗️ 架构总览

```
用户消息
    │
    ▼
┌──────────────────────────────────────────────┐
│           SteppedRunner (ReAct 逐步执行)       │
│                                              │
│  ┌─ Supervisor Agent (路由决策) ───────────┐  │
│  │                                         │  │
│  │  ┌─ math_agent ── calculator            │  │
│  │  ├─ search_agent ── weather + grep      │  │
│  │  └─ general_agent ── query_order        │  │
│  │                    delete_order (需审批) │  │
│  │                    send_email   (需审批) │  │
│  └─────────────────────────────────────────┘  │
│                                              │
│  ┌─ 中断门 ───────────────────────────────┐  │
│  │  节点级中断 → 执行计划审批 (请求显式标志) │  │
│  │  工具级中断 → 高危工具审批 (自动触发)    │  │
│  └─────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
    │                              │
    │                              ▼
    │                    EventRecorder → ProgressSink
    │                    （路由/工具/审批进度 + 答案片段）
    ▼                              │
ChatModel (Mock / OpenAI / DeepSeek / Ark / ...)   │
    │  Stream / Generate           │              │
    ▼                              ▼              ▼
┌──────────────────────────────────────────────┐
│  上下文管理                    记忆系统       │
│  TrimByToken (按 token 裁剪)  CheckpointStore│
│  Summarizer (LLM 摘要)        MemoryStore    │
│  GuardToolPairs               VectorStore    │
│  完整历史 / 模型上下文分离     记忆开关        │
└──────────────────────────────────────────────┘
```

**执行流程**：用户消息 → 上下文压缩（仅作用于模型视图）→ LLM 流式推理 → 中断门检查 → 工具执行 → 结果回灌 → 继续推理 → 最终回复；全过程的路由、工具与审批进度实时推送到前端。

---

## 🤝 人机协同（HITL）

本项目实现了两种中断类型，均采用**异步审批模式**（中断→返回→API 恢复）：

### 工具级中断

高危工具（`delete_order`、`send_email`）执行前自动触发中断，等待人工审批。

```
Agent 调用 delete_order → 中断 → 返回审批卡片 → 人工批准/拒绝 → Resume 继续执行
```

### 节点级中断

勾选聊天框左侧的"⚠️ 执行前确认"开关（API 字段 `confirmBeforeExecute`）后，LLM 生成执行计划即暂停，展示计划供人工审批。触发由请求显式指定，消息文本不参与判断，且标志按请求传递，并发请求互不影响。

```
开关开启，用户: "查询北京天气"
→ LLM 决定调用 weather 工具 → 中断 → 展示执行计划 → 人工批准 → 执行工具 → 返回结果
→ 人工拒绝 → LLM 收到拒绝反馈 → 重新规划
```

### 执行可靠性

- 同一用户、同一会话的聊天和审批串行执行；重复审批返回已保存结果。存在待审批任务时，先处理审批再发送新消息。
- `CHECKPOINT_STORE=file` 同时启用审批持久化，文件位于 `<CHECKPOINT_STORE_PATH>.approvals.json`，保存审批、嵌套运行状态和恢复结果。建议同时启用 `THREAD_STORE=file` 和 `SESSION_STORE=file`。
- 支持“计划审批 → 工具审批 → 后续工具审批”，沿用原 `runId`，每次返回新的可操作 `interrupt`。
- 模型限流最多重试 3 次（最多调用 4 次），退避为 250/500/1000 ms。每次聊天或恢复限时 2 分钟，响应 HTTP 请求取消；网络工具通过 `ToolIdentity.Context` 接收取消。
- 模型错误、系统级工具错误、步骤耗尽及检查点或线程落盘失败返回 `error`；主动取消返回 `cancelled`。SSE 保留真实状态。
- 执行前保存 `running` 凭据，结束后保存结果。若进程在两次写入之间崩溃，或结果落盘失败，系统阻止自动重放并提示核对业务结果。这不是外部副作用的“恰好一次”事务，真实业务工具仍需幂等键或事务对账。
- PostgreSQL 后端共享用户、会话、线程、检查点、记忆和审批；同一用户/线程通过数据库 advisory lock 跨实例串行，审批通过原子 claim 保证只有一个实例恢复执行。
- 当 `CHECKPOINT_STORE`、`THREAD_STORE` 和 `APPROVAL_STORE` 均为 `postgres` 时，创建审批所需的 Checkpoint、线程历史和审批请求在同一数据库事务中提交；任一写入失败会全部回滚。混合后端仍按各自存储写入。
- 同一 PostgreSQL 配置下，审批恢复后的线程最终状态（或下一条待审批记录及 Checkpoint）、旧审批执行凭据和运行事件也在同一事务中提交。事务失败时保留 `running` 认领，阻止自动重放；外部工具副作用仍需幂等键或人工对账。
- JSON 文件后端仅支持单进程独占使用。`BUSINESS_STORE=postgres` 时，示例订单采用软删除并保存幂等结果，邮件记录按工具调用幂等落库；接入真实邮件供应商时仍应使用 Outbox 或供应商幂等键。
- 密码使用带随机盐的 Argon2id（19 MiB、2 次迭代）；旧版 SHA-256 账户在首次成功登录后自动升级。角色变更会撤销该用户在内存、文件或 PostgreSQL 中的全部会话；会话校验同时核对当前账户角色，拒绝并删除跨实例竞态产生的旧角色会话。
- 登录失败分别按用户名和直接连接 IP 计数。默认 15 分钟内失败 5 次后锁定 15 分钟，返回 429 和 `Retry-After`；成功登录清除对应计数。PostgreSQL 部署使用共享事务计数，内存/文件部署使用进程内计数。反向代理场景应在可信代理处限制客户端来源；应用不信任客户端提交的 `X-Forwarded-For`。

回归覆盖见 `internal/agent/execution_test.go` 和 `integration_test/reliability_test.go`。

全部检查（格式、vet、构建、测试、前端语法）统一走一个脚本，本地与 CI 共用同一份逻辑：

```bash
./scripts/check.sh            # 全部
./scripts/check.sh go         # gofmt + go vet + go build + go test -race
./scripts/check.sh frontend   # node --check web/assets/app.js
```

`go test` 带 `-count=1`，避免命中缓存后把旧结果当成当前状态。`go test -race` 需要 C 编译器（Linux 上即 gcc，`golang` 官方镜像已包含）。

**前端语法检查为什么单独存在**：`go build` 不会校验 `go:embed` 进去的 JS。一个语法错误会让整个页面失去交互（`onclick` 里的函数全部未定义），而所有 Go 测试仍然全绿——本项目就发生过一次。因此它是独立的 CI 任务，只有在环境缺少 Node 时才能通过 `SKIP_FRONTEND_CHECK=1` **显式**跳过；静默跳过正是当初漏掉这个错误的原因。

### 前端契约测试

语法检查抓不到另一类问题：字段名不匹配、读取服务端从不返回的键。这类问题**同样不影响任何 Go 测试却会让界面失效**——本项目也发生过两次（会话字段名不一致导致记忆面板空白；`data.memory` 读自一个没有该字段的响应类型）。

`integration_test/frontend_contract_test.go` 用真实路由驱动每个端点，断言前端读取的每个键确实存在。它还做反向检查：**该键必须真的出现在 `web/assets/app.js` 里**，否则测试失败——这样清单不会腐化成"断言一个没人用的契约"（第一次运行就抓出我列表里三个前端根本不读的键）。SSE 部分另外断言服务端发出的帧名恰好是前端处理的那几个，所以"服务端不再发某个帧、前端留下死分支"也会被发现。

新增前端读取字段时把它加进清单；改后端 JSON tag 时，测试会先于用户发现。

> 一处**看似不匹配其实正确**的地方，避免后人"顺手改坏"：`/api/approvals` 返回的字段是大写的（`InterruptID`、`Status`、`ThreadID`…），因为 `hitl.ApprovalRequest` 大多数字段没有 JSON tag，Go 默认输出 Go 字段名。前端读大写是对的。

### 持续集成

`.github/workflows/ci.yml` 在每次 push 和 PR 时运行两个并行任务：

| 任务 | 内容 |
|------|------|
| Go checks | `gofmt` 校验、`go vet`、`go build`、`go test -race`（按 `go.mod` 声明的版本） |
| Frontend syntax | `node --check web/assets/app.js` |

集成测试使用 `httptest` 与 mock 模型，不依赖外部服务；PostgreSQL 相关测试在未设置 `DATABASE_URL` 时自动跳过。

### 恢复语义

| 中断类型 | 恢复方式 | 说明 |
|----------|----------|------|
| 工具级 | 从审批快照加载完整状态 → 执行工具 → 继续 ReAct | 持久化执行凭据和响应，阻止重复审批执行 |
| 节点级 | 从 Checkpoint 加载完整状态 → 保留/清空 PendingToolCalls → 继续 ReAct | 拒绝时 LLM 重新规划 |

Checkpoint 与审批记录保存完整的 `SteppedRunState`（对话历史、步骤数、待执行工具和子 Agent 状态）。恢复使用该审批自己的状态快照，按 `toolCallID`、名称和参数精确匹配工具；状态缺失时返回错误，不从线程历史猜测并重新执行。

---

## 🧠 多 Agent 与工具

### Supervisor 路由

Supervisor Agent 根据用户问题语义路由到三个子 Agent：

| 子 Agent | 职责 | 内部工具 |
|----------|------|----------|
| `math_agent` | 数学计算 | calculator |
| `search_agent` | 信息搜索 | weather, grep |
| `general_agent` | 业务操作 | query_order, delete_order, send_email |

子 Agent 各自独立的 ReAct 循环，fresh context 隔离，Supervisor 汇总结果返回。

### 工具清单

| 工具 | 风险等级 | 需审批 | 说明 |
|------|----------|--------|------|
| `calculator` | low | 否 | 数学表达式计算 |
| `weather` | low | 否 | 实时天气查询 |
| `grep` | medium | 否 | 日志搜索（示例数据） |
| `query_order` | low | 否 | 查询用户订单 |
| `delete_order` | high | 是 | 删除订单（内存状态变更 + HITL 审批） |
| `send_email` | high | 是 | 发送邮件（内存发送记录 + HITL 审批） |

---

## 🌊 流式输出

聊天接口（`stream=true`）把执行过程实时推给前端，分两层：

| 层 | 内容 | SSE 帧 |
|----|------|--------|
| 事件级进度 | Supervisor 路由、工具开始/结束、ACL 拒绝、等待审批 | `tool_call`（带 `phase` 与 `tool`） |
| token 级片段 | 模型输出的增量文字 | `chunk`（带 `content`） |

审批恢复走同一条通道：`POST /api/approvals/{id}/decision` 加 `stream: true` 后，被批准工具的执行与后续推理进度同样实时推送，再次中断时终止帧携带新的审批请求。默认仍是原有的一次性 JSON 响应。

**审批一旦被 claim，恢复就与客户端连接解耦**：断连不会取消已获授权的操作，服务端执行到完成或 2 分钟超时。claim 是不可回退点——此时取消会消耗掉这次审批（无法重试）并把可能已发生的副作用记录为取消。claim 之前的断连仍会中止恢复。

实现要点：

- 出口挂在 `EventRecorder` 上（`ProgressSink`），它本来就穿透了 `RunStep → executePendingTools → 子 Agent` 全链路；整个 run 在 HTTP handler 的 goroutine 内同步执行，因此 sink 直接写响应、无需加锁。
- 模型调用改用 Eino 的 `Stream`，片段边转发边用 `schema.ConcatMessages` 合并成完整消息交给 ReAct 循环（OpenAI 兼容接口的 `tool_call` 参数是分片下发的）。
- 限流重试只在**尚未向前端发出任何片段**时进行——已经吐出内容就不再重试，避免前端看到半句话后又收到重放。
- 答案已分片下发时不再发送整段 `chunk`（否则客户端会拼接两次），`done` 帧用 `streamed: true` 标明；失败与取消不下发 `chunk`，错误文本只出现在 `done.answer`。
- 前端进度行显示实时状态，运行期间"发送"被"停止"替换；停止即断开连接，服务端通过请求 context 取消 run，已收到的片段保留并标注"⏹️ 已停止"。

---

## 📄 文档 RAG

每个用户可以上传自己的文档，提问时相关片段会带编号注入提示词，回答据此引用来源。

| 能力 | 实现 |
|------|------|
| 归属 | 按 `userId` 隔离，与记忆、会话同一套隔离红线；跨用户不可见、不可删 |
| 摄入 | 界面粘贴文本，或选择 `.txt` / `.md` 文件（浏览器内读为文本后随 JSON 提交，无 multipart） |
| 分块 | 段落优先，目标约 400 字/块、约 80 字重叠；超长段落硬切；裁剪后保证 tool 配对不被破坏 |
| 存储 | 复用记忆存储的保留键（`__doc_*` / `__chunk_*`），因此自动获得内存/文件/PostgreSQL 三种后端持久化，且不出现在记忆列表、检索与整合中 |
| 索引 | 独立的文档向量库（与记忆向量库分开，避免两者在同一 top-K 里互相挤占） |
| 注入 | `【文档片段】` 段落，每条形如 `[1] 文档名 · 片段 3：内容`，预算由 `DOCUMENT_BUDGET_TOKENS` 单独控制 |
| 引用 | 系统提示词要求模型使用文档内容时以 `[n]` 标注来源 |
| 重启 | 向量索引是进程内的，重启后首次检索会从存储里的分块**懒重建**；同一机制也修复了情景记忆重启后失去向量召回的问题 |

接口：`GET /api/documents`（列表，不含分块原文）、`POST /api/documents`（`{name, content}`）、`DELETE /api/documents/{id}`。

> **检索质量取决于 embedding 提供方。** 默认的 `EMBEDDING_PROVIDER=hash` 是哈希伪嵌入，只做词面匹配；实测中文查询对**正确**文档的相似度只有 0.168，低于为真实 embedding 标定的 0.3 阈值——所以应用会为 hash 模式自动把阈值降到 0.1，否则中文检索会完全失效。即便如此，hash 模式的排序质量有限：想要真正的语义检索请配置 `EMBEDDING_PROVIDER=openai`（或 `ollama`），此时阈值自动用 0.3。
>
> **未做**：PDF / DOCX 解析（只接受纯文本与 `.txt`/`.md`）；共享知识库（文档按用户隔离）；同名文档重复上传会新建一份而非覆盖。

---

## 🔌 MCP 外部工具

通过 [MCP](https://modelcontextprotocol.io) 接入外部工具服务器，让 Agent 的能力不再局限于编译期内置的工具。

| 能力 | 实现 |
|------|------|
| 接入方式 | 只做 **stdio**（启动子进程），适配层为 `eino-ext/components/tool/mcp`，客户端库为 `mark3labs/mcp-go` |
| 默认状态 | **未配置即完全不启用**，内置工具集与行为不变 |
| 工具发现 | 启动时握手 → `tools/list` → 转成框架的 `RegisteredTool` 并注册 |
| 权限 | **与内置工具走同一条 ACL 拦截**（见下） |
| 审批 | 由本地配置 `MCP_REQUIRE_APPROVAL` 决定，不读远端 annotations |
| 结果 | 只把服务器文本交给模型，剥离 `{"content":[{"type":"text",…}]}` 协议信封 |
| 可达性 | 自动新增 `mcp_agent` 子 Agent 承载远端工具；未发现工具时不创建 |
| 生命周期 | `App.Close()` 终止所有子进程；单个服务器连不上只记日志并跳过 |

```dotenv
# 仓库自带的演示服务器（离线可跑）
MCP_SERVERS=[{"name":"demo","command":"go","args":["run","./cmd/mcp-demo-server"]}]
# 或官方 filesystem 服务器（需要 Node 与网络）
MCP_SERVERS=[{"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/demo"]}]

# 哪些远端工具需要人工审批（按工具名，逗号分隔，忽略大小写）。空 = 都不需要。
MCP_REQUIRE_APPROVAL=delete_note
# 远端工具的授权：admin 默认全部（*），visitor 默认没有。
MCP_ADMIN_TOOLS=*
MCP_VISITOR_TOOLS=
```

`scripts/run-with-mcp.sh` 用上面的配置一键启动（演示服务器 + `delete_note` 需审批），用于本节的演示与集成测试。

### 权限：为什么注册顺序是关键

框架的 ACL 不是逐工具声明的，而是在启动时统一包装：先注册所有工具，再调 `aclMiddleware.WrapAllTools()` 把每个工具的 `Fn` 换成带权限校验的版本。因此 **MCP 工具必须注册在这一步之前**——顺序错了，远端工具就带着未包装的执行函数进入注册表，成为一个绕过权限的旁路，而且角色表看起来完全正常，只有实际调用才会暴露。

集成测试直接通过注册表调用远端工具来断言这一点（而不是只查角色表）：未授予角色的用户调用必须被拒。把注册挪到包装之后，该测试会立刻报出 `ACL bypass`。

**未授予任何角色的远端工具会被拒绝——包括 admin。** 内置角色的权限是静态列表，覆盖不到运行时才出现的工具，所以启动时会按 `MCP_ADMIN_TOOLS` / `MCP_VISITOR_TOOLS` 授予（`*` 表示全部）。默认 admin 拿到全部、visitor 一个都没有，因此"visitor 调用远端工具被拦截"是个天然可演示的场景。

### 审批：只认本地策略

`MCP_REQUIRE_APPROVAL` 列出需要人工审批的远端工具。**不读 MCP 的 `annotations`**（`readOnlyHint` / `destructiveHint` 等）：那是服务器自报的，一个恶意或有 bug 的服务器可以把自己标成只读。远端工具的描述同理——它会被拼进提示词供模型选择工具，但**不参与任何安全判断**。

### 结果整形：剥离协议信封

MCP 的返回是 `{"content":[{"type":"text","text":"…"}]}`。原样交给模型等于每次调用都付一遍脚手架 token，读起来也是噪声，所以转换时只提取 `content[].text` 并按序拼接。**识别不出信封就原样返回**——图片或嵌入资源不含文本，清空会静默丢掉结果。错误路径同样处理，因此远端报错保留服务器的原话（如"笔记 n3 不存在"）而不夹带协议 JSON。

### 失败策略与存储相反

单个 MCP 服务器连不上 → 记日志、跳过，应用照常启动。这与"存储后端初始化失败即拒绝启动"是刻意相反的：外部工具是可选能力，一个坏掉的服务器不该让整个框架不可用；而存储承载着不能静默丢失的数据。

### 未做

HTTP / SSE 传输（只做 stdio）；OAuth 与远端鉴权；远端工具名的自动命名空间化（与内置工具重名时跳过并记日志）；对远端工具描述做提示注入检测。

---

## 📐 上下文管理

长对话自动裁剪，确保不超出 LLM 窗口：

| 策略 | 实现 | 特点 |
|------|------|------|
| 按消息数裁剪 | `TrimByCount` | system 消息永久保留；经 `MAX_MESSAGES` 配置，默认关闭（见下） |
| 按 Token 数裁剪 | `TrimByToken` | 基于 `SimpleTokenCounter` 估算，压缩流程的最后一步调用 |
| Tool 配对保护 | `GuardToolPairs` | tool_call 与 tool_result 不拆散 |
| LLM 摘要压缩 | `Summarizer.Compress` | 超阈值触发 LLM 摘要，规则提取降级兜底 |
| 工具结果上限 | `capToolResult` | 单个结果超 `MAX_TOOL_RESULT_CHARS` 截断并标注原始长度 |
| 历史与上下文分离 | `SteppedRunState.ModelContext` | 压缩只改模型视图，完整对话仍完整落盘 |
| 追加式写入 | `ThreadHistoryAppender` | 线程历史只写新增消息，不再每轮重写整块（PostgreSQL 与内存后端；文件后端仍为全量重写） |

两种裁剪按**先条数、后 token** 的顺序作用。`MAX_MESSAGES` 默认为 0（不按条数裁剪），因为窗口设得过小会让对话永远达不到摘要阈值——**开启条数窗口可能使摘要压缩永不触发**。

**可用预算** = `MAX_TOKENS` − 工具定义 schema 开销 − `RESERVE_OUTPUT_TOKENS`（为回答预留）。压缩判定与裁剪都以它为准，而界面显示的是含基础开销的请求真实大小。token 条同时展示本地估算与服务商返回的实际用量。

**追加式写入的长度守卫**：追加请求带一个"期望长度"，由数据库在 SQL 内用 `jsonb_array_length` 求值，因此不读取既有历史。任何不匹配（并发写入、被修复过的前缀、早于该字段存在的 checkpoint）都会退化为全量替换——优化失效时最坏是慢，不会写错数据。

---

## 💾 记忆系统

双层记忆架构，接口独立、存储可替换：

```
┌─ 短期记忆 ─────────────────┐   ┌─ 长期记忆 ─────────────────┐
│  CheckpointStore            │   │  MemoryStore               │
│  · 按线程隔离 (threadID)    │   │  · 按用户隔离 (userID)     │
│  · 中断状态 / 对话快照      │   │  · KV 偏好 / 语义向量检索  │
│  · save / load / delete     │   │  · put / get / delete      │
└─────────────────────────────┘   └─────────────────────────────┘
```

**跨会话偏好**：用户在会话 A 表达"我喜欢用 Python"→ 自动提取写入长期记忆 → 会话 B 中 LLM 自动读取偏好。

**记忆系统 v2**：在 KV 之上补齐记忆系统的三块核心能力——

| 能力 | 实现 |
|------|------|
| 结构化记忆 | 条目带 `type`（preference/identity/fact/episode/rule）、`importance`（1-5）、来源线程与原句摘录 |
| 冲突消解 | 偏好变更时旧值进入修订历史（保留最近 5 版），彻底修复"过时偏好永久驻留"问题 |
| 统一检索 | `RetrieveRelevant` 按 `关键词相关性 × 重要度 × 时间衰减 × 访问频次` 打分，token 预算内取 top-N 注入（向量召回先按预算截断，KV 条目使用剩余预算，总量不会超预算）；被命中的记忆自动强化（使用即强化） |
| 遗忘 | 有效分衰减到阈值之下的旧条目被归档（可查不可注入），不再无限膨胀 |
| 整合 | 达到条目阈值后 LLM 将碎片记忆整合为 `user_profile` 用户画像，归档过时条目，并把情景沉淀为持久事实；无 LLM 时规则降级 |
| 记忆面板 | 前端按类型分组展示、重要度星标、来源 tooltip、归档折叠区、一键"🧹 整合记忆" |
| 记忆开关 | 每用户"🙈 禁止记忆"开关：关闭后既不提取也不注入，已有条目保留不删除；开关在记忆服务内部强制拦截，任何调用路径无法绕过（`GET`/`PUT /api/memory/settings`） |

**持久化存储后端（进阶档）**：会话、检查点、记忆和线程支持 `memory`、单进程 `file` 与多实例 `postgres`；用户账户和示例业务数据支持 `memory` / `postgres`，审批支持 `memory` / `file` / `postgres`。启用 PostgreSQL 后，运行结果与事件也写入数据库，跨实例可按用户读取。PostgreSQL 启动时执行内置的版本化迁移，线程使用 advisory lock 跨实例串行，审批使用条件更新原子认领，订单删除与邮件记录使用工具调用幂等键。file 版启用后可跨重启恢复，但不能由多个进程共同写入。

多实例配置示例：

```dotenv
DATABASE_URL=postgres://agent:agent_dev_password@127.0.0.1:5432/agent?sslmode=disable
USER_STORE=postgres
SESSION_STORE=postgres
CHECKPOINT_STORE=postgres
MEMORY_STORE=postgres
THREAD_STORE=postgres
APPROVAL_STORE=postgres
BUSINESS_STORE=postgres
RUN_EVENT_RETENTION=168h
```

本地可直接启动 PostgreSQL；应用首次连接时自动迁移数据库：

```bash
docker compose -f compose.postgres.yml up -d
```

迁移文件位于 `internal/postgres/migrations`。已发布的迁移由 SHA-256 校验，不能原地修改；结构变更应新增编号更大的 SQL 文件。启动时会在一条专用连接上持有 PostgreSQL advisory lock，每个待执行版本在独立事务中提交，因此多个实例可以同时启动。数据库包含当前程序未知的更高版本，或已执行迁移的校验和不匹配时，程序会拒绝启动。

`RUN_EVENT_RETENTION` 默认 168 小时。到期的运行事件不再可读，并在后续写入时从数据库清理。设置 `TEST_DATABASE_URL` 后，`go test ./integration_test -run TestPostgres` 会执行真实数据库的并发迁移、双连接池共享、运行事件隔离、审批竞争、创建／恢复两阶段的事务回滚，以及追加式写入的长度守卫与保留期清理：

```bash
docker compose -f compose.postgres.yml up -d
TEST_DATABASE_URL='postgres://agent:agent_dev_password@127.0.0.1:5432/agent?sslmode=disable' \
  go test ./integration_test -run TestPostgres -v
```

这些测试需要真实数据库，因为单元测试用的 sqlmock 只校验 SQL 语句文本，不校验 PostgreSQL 是否接受它、行为是否符合预期。未设置 `TEST_DATABASE_URL` 时它们会跳过。

> **运维注意**：迁移 004 用 `CREATE INDEX`（非 `CONCURRENTLY`）为线程保留期加索引。迁移执行器把每个版本包在事务里，而 `CREATE INDEX CONCURRENTLY` 不能在事务内运行，所以在已有大量数据的库上执行该迁移会短暂阻塞写入。演示与新建库不受影响。

生产探针无需认证：`GET /healthz` 用于进程存活检查，`GET /readyz` 用于流量就绪检查。PostgreSQL 模式下，`/readyz` 会在 2 秒超时内核对数据库连接和完整迁移历史；依赖不可用时返回 `503 {"status":"unavailable"}`。内存或文件模式没有外部数据库依赖，初始化成功后即返回 ready。

**向量检索**：三种 embedding 后端可选：

| 后端 | `EMBEDDING_PROVIDER` | 说明 |
|------|---------------------|------|
| Hash 伪向量 | `hash`（默认） | FNV-1a 哈希，零依赖 |
| Ollama | `ollama` | 本地 Ollama embedding（`nomic-embed-text`） |
| OpenAI | `openai` | 云端 OpenAI 兼容 API（`text-embedding-3-small`） |

---

## ⚙️ 模型配置

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MODEL_PROVIDER` | `mock` | 模型提供者：mock / openai / ark |
| `OPENAI_BASE_URL` | | OpenAI 兼容 API 地址 |
| `OPENAI_API_KEY` | | API Key |
| `OPENAI_MODEL` | | 模型名称 |
| `ARK_API_KEY` | | Ark API Key |
| `ARK_MODEL` | | Ark 模型 endpoint ID |
| `EMBEDDING_PROVIDER` | `hash` | Embedding 后端：hash / ollama / openai |
| `ADDR` | `:8080` | HTTP 监听地址 |
| `SESSION_TTL` | `30m` | 会话滑动过期时间（如 30m/2h），每次校验成功自动续期 |
| `SESSION_COOKIE_SECURE` | `true` | 会话 Cookie 的 Secure 标记。浏览器只在 HTTPS 或 localhost 下保存 Secure Cookie，因此用普通 HTTP 在局域网 IP 上演示时需设为 `false` |
| `BOOTSTRAP_ADMIN_USERNAME` | | 空用户库首次启动时创建的管理员用户名 |
| `BOOTSTRAP_ADMIN_PASSWORD` | | 初始管理员密码，至少 12 个字符；必须与用户名同时设置 |
| `LOGIN_MAX_FAILURES` | `5` | 限流窗口内最多允许的失败次数 |
| `LOGIN_FAILURE_WINDOW` | `15m` | 登录失败计数窗口 |
| `LOGIN_LOCKOUT` | `15m` | 达到阈值后的锁定时长 |
| `MEMORY_BUDGET_TOKENS` | `400` | 每轮注入 system prompt 的记忆 token 预算 |
| `DOCUMENT_BUDGET_TOKENS` | `800` | 文档片段的 token 预算，与记忆预算独立；`0` 关闭文档检索 |
| `VECTOR_MIN_SCORE` | `0`（自动） | 向量召回的相关度下限；自动时按 embedding 提供方选值（hash → 0.1，真实 embedding → 0.3） |
| `MEMORY_CONSOLIDATE_THRESHOLD` | `30` | 触发 LLM 记忆整合的活跃条目数阈值 |
| `MAX_TOKENS` | `8000` | 上下文窗口上限（进度条满刻度） |
| `MAX_MESSAGES` | `0` | 送给模型的消息条数上限，0 = 不按条数裁剪 |
| `SUMMARIZE_THRESHOLD_RATIO` | `0.8` | 摘要触发阈值比例（阈值 = 可用预算 × 此值） |
| `SUMMARY_TARGET_TOKENS` | `800` | 摘要目标 token 数 |
| `RESERVE_OUTPUT_TOKENS` | `1024` | 为模型回答预留的空间；可用预算 = `MAX_TOKENS` − 本项 − 工具定义开销 |
| `MAX_TOOL_RESULT_CHARS` | `8000` | 单个工具结果的字符上限，超出截断并标注原始长度 |
| `THREAD_HISTORY_MAX_MESSAGES` | `0` | 单个会话保留的消息上限，0 = 不限制 |
| `THREAD_RETENTION` | `0` | 未使用多久的会话被清理（如 `720h`），0 = 永久保留 |
| `RUN_EVENT_RETENTION` | `168h` | 运行事件保留期，到期后接口返回 404 |
| `USER_STORE` | `memory` | 用户账户存储：`memory` / `postgres` |
| `SESSION_STORE` | `memory` | 会话存储：`memory` / `file` / `postgres` |
| `CHECKPOINT_STORE` | `memory` | 检查点存储：`memory` / `file` / `postgres` |
| `MEMORY_STORE` | `memory` | 长期记忆存储：`memory` / `file` / `postgres` |
| `THREAD_STORE` | `memory` | 对话线程存储：`memory` / `file` / `postgres` |
| `APPROVAL_STORE` | 同 `CHECKPOINT_STORE` | 审批存储：`memory` / `file` / `postgres` |
| `BUSINESS_STORE` | `memory` | 示例订单、邮件记录和幂等凭据：`memory` / `postgres` |
| `*_STORE_PATH` | `data/*.json` | file 后端的数据文件路径（默认 `data/sessions.json` 等） |
| `DATABASE_URL` | | PostgreSQL DSN；任一存储选择 `postgres` 时必填 |
| `DATABASE_MAX_OPEN_CONNS` | `25` | PostgreSQL 最大打开连接数 |
| `DATABASE_MAX_IDLE_CONNS` | `5` | PostgreSQL 最大空闲连接数 |
| `DATABASE_CONN_MAX_LIFETIME` | `30m` | PostgreSQL 连接最长复用时间 |
| `MCP_SERVERS` | 空 | 外部 MCP 服务器，JSON 数组；空 = 不启用 MCP |
| `MCP_REQUIRE_APPROVAL` | 空 | 需要人工审批的远端工具名，逗号分隔；不读远端 annotations |
| `MCP_ADMIN_TOOLS` | `*` | 授予 admin 的远端工具，`*` = 全部 |
| `MCP_VISITOR_TOOLS` | 空 | 授予 visitor 的远端工具 |

### 一键接入 OpenAI 兼容 API

| 服务商 | OPENAI_BASE_URL | OPENAI_MODEL |
|--------|-----------------|--------------|
| OpenAI | （留空） | `gpt-4o` |
| DeepSeek | `https://api.deepseek.com` | `deepseek-chat` |
| Moonshot | `https://api.moonshot.cn/v1` | `moonshot-v1-8k` |
| 智谱 GLM | `https://open.bigmodel.cn/api/paas/v4` | `glm-4-flash` |
| SiliconFlow | `https://api.siliconflow.cn/v1` | `Qwen/Qwen2.5-7B-Instruct` |
| 本地 Ollama | `http://localhost:11434/v1` | `llama3` |

### 运行时模型切换

前端左侧边栏提供模型选择器，支持运行时切换（无需重启）：

- **Mock** — 内置关键词匹配，无需 API Key
- **DeepSeek** — `deepseek-chat`
- **Qwen** — `qwen-plus`
- **OpenAI** — `gpt-4o`

切换时自动重建 Supervisor、SteppedRunner、Summarizer 等全部组件。

---

## 📡 API 端点

### 认证
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/auth/login` | 登录 |
| GET | `/api/auth/me` | 当前用户信息 |
| POST | `/api/auth/logout` | 退出登录 |

### 用户与角色
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/users` | 用户列表 |
| POST | `/api/users` | 创建用户 |
| PUT | `/api/users/{userId}/roles` | 更新用户角色 |
| GET | `/api/roles` | 角色列表 |

### Agent 聊天
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/agent/chat` | 发送消息（支持 SSE 流式） |
| GET | `/api/agent/runs/{runId}/events` | 运行事件流 |
| POST | `/api/agent/resume` | 恢复中断运行 |

### 审批
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/approvals` | 待审批列表 |
| GET | `/api/approvals/{id}` | 审批详情 |
| POST | `/api/approvals/{id}/decision` | 审批决策（approve/reject） |

### 工具 / 记忆 / 模型
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tools` | 工具列表 |
| GET | `/api/memory` | 用户记忆 |
| POST | `/api/memory` | 写入记忆 |
| DELETE | `/api/memory/{key}` | 删除记忆（保留键拒绝） |
| GET | `/api/memory/settings` | 读取记忆开关 |
| PUT | `/api/memory/settings` | 设置记忆开关（禁止记忆） |
| POST | `/api/memory/consolidate` | 执行一次记忆整合（遗忘 / 画像 / 沉淀） |
| GET | `/api/models` | 可用模型列表 |
| POST | `/api/models/switch` | 切换模型 |

---

## 🎯 演示场景

聊天区右上角的"🎬 演示 ▾"下拉菜单内置 **5 个一键演示项**，覆盖全部核心功能：

| 菜单项 | 场景 | 自动操作 |
|--------|------|----------|
| 🚫 Visitor 越权 | ACL 拦截 | visitor 登录 → 删除订单 → ACL 拒绝 |
| ✅ Admin 审批 | HITL 流程 | admin 登录 → 删除订单 → 触发审批 → 批准/拒绝 |
| 🔀 不同工具路径 | 多 Agent 路由 | 依次调用不同工具展示 Supervisor 分发 |
| ✂️ 长对话裁剪 | 上下文管理 | 连续发送 10 条消息触发摘要压缩 |
| 🧠 记忆管理 | 跨会话偏好 | 表达偏好 → 验证记忆保存 → 换线程读取 |

另外三个不需要脚本、手动操作即可演示的功能：

- **流式输出**：发送任意消息，观察输入框上方的实时进度行（路由 / 工具 / 审批）与逐字出现的回复；运行期间"发送"变为"停止"，点击可中断并保留已收到的内容。
- **节点级中断**：勾选输入框左侧的"⚠️ 执行前确认"开关后输入"查询北京天气"，触发执行计划审批流程。
- **记忆开关**：勾选记忆面板底部的"🙈 禁止记忆"，验证关闭后既不提取也不注入、已有条目保留。

完整演示脚本见 [`docs/demo-script.md`](docs/demo-script.md)。

---

## 📁 项目结构

```
agent-eino-demo/
├── cmd/server/              # 入口
├── internal/
│   ├── agent/               # ReAct 循环、Supervisor 路由、SteppedRunner 中断门
│   ├── app/                 # 依赖组装、配置、模型工厂
│   ├── auth/                # 登录、会话、RBAC、ACL 中间件
│   ├── contextmgr/          # 上下文裁剪、Token 计数、LLM 摘要压缩
│   ├── hitl/                # 人机协同：中断管理、审批、Checkpoint
│   ├── httpapi/             # REST API + SSE 流式
│   ├── memory/              # 长期记忆 + Checkpoint + 向量检索
│   └── tools/               # 工具注册表、ACL 中间件、6 个示例工具
├── web/                     # 前端（vanilla JS，go:embed 嵌入）
├── docs/                    # 设计文档、API 文档、演示脚本、框架对比
├── integration_test/        # 集成测试
└── go.mod                   # Go 1.22 + Eino v0.9.12
```

---

## 🧪 测试

```bash
# 单元测试（118 个用例）
go test ./internal/...

# 集成测试（8 个用例：登录、ACL、审批、工具、记忆等）
go test ./integration_test/...

# 全量测试
go test ./...
```

| 包 | 测试文件 | 测试函数 |
|----|----------|----------|
| auth | 3 | 24 |
| contextmgr | 5 | 30 |
| memory | 4 | 28 |
| tools | 3 | 22 |
| hitl | 1 | 6 |
| integration_test | 1 | 8 |

---

## 📚 文档

| 文档 | 说明 |
|------|------|
| [`docs/agent-system-architecture.md`](docs/agent-system-architecture.md) | 从 Agent 运行时角度介绍总体架构、五大模块实现、调用链与生产化边界 |
| [`docs/design.md`](docs/design.md) | 总体架构、模块设计、接口定义、隔离策略、恢复语义 |
| [`docs/api.md`](docs/api.md) | 全部 API 端点、请求/响应示例 |
| [`docs/demo-script.md`](docs/demo-script.md) | 6 个演示场景 + 5 分钟快速版 |
| [`docs/framework-comparison.md`](docs/framework-comparison.md) | LangGraph / LangGraph4j / Eino 七维度对比 |
| [`docs/compliance-check.md`](docs/compliance-check.md) | 任务要求合规性检查报告 |
| [`docs/dev-todo.md`](docs/dev-todo.md) | 开发完成状态与已知限制 |

---

## 🔧 技术栈

| 层 | 技术 |
|----|------|
| Agent 框架 | [CloudWeGo Eino](https://github.com/cloudwego/eino) v0.9.12 — ReAct 循环 + ToolCalling |
| LLM 接入 | Eino Ext OpenAI — 兼容 OpenAI / DeepSeek / Qwen / Ollama 等 |
| 向量数据库 | [chromem-go](https://github.com/philippgille/chromem-go) v0.7.0 — 纯 Go 进程内向量库 |
| 后端 | Go 1.22 — 标准库 `net/http` + `go:embed` |
| 前端 | Vanilla HTML/JS/CSS — 嵌入二进制，零构建依赖 |

---

## 📋 已知限制

| 限制 | 说明 |
|------|------|
| HITL 为异步审批模式 | 中断后 run 结束，通过独立 API 恢复，非"挂起等待"语义 |
| grep 使用示例数据 | 搜索日志为硬编码 mock，无真实文件系统访问 |
| 线程历史默认无保留上限 | 完整对话永久保留，模型输入由压缩保证有界，但存储总量持续增长。**可通过 `THREAD_HISTORY_MAX_MESSAGES` 与 `THREAD_RETENTION` 显式开启上限或清理**（默认保持完整保留：静默丢弃用户对话属于产品决策）。写入成本已通过追加式写入解决 |
| 文件后端写入为全量重写 | 单文件 JSON 无法原地追加，成本为 O(全部会话)；写入成本敏感的场景应使用 PostgreSQL |
| 压缩后 checkpoint 体积上升 | 压缩过的 run 会同时序列化完整历史与压缩上下文两份 |

> 多用户隔离为框架强制：类型化工具身份（不可伪造）、线程/运行事件/审批属主校验、存储层 `CheckUserScope` 上下文校验，详见 `docs/design.md` 5.3 节。节点级中断由请求显式 `confirmBeforeExecute` 标志触发，不依赖消息关键词。

---

## 📄 License

本项目为演示项目，仅用于学习和参考。
