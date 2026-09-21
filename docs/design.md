# 总体设计文档

> 本文档描述 Agent Eino Demo 项目的总体架构、模块设计、接口定义、多用户隔离策略和恢复语义。

---

## 1. 项目概述

### 1.1 目标

构建一个通用 Agent 基础框架，围绕以下 5 个模块完成可运行、可演示、可验收的工程实现：

1. **登录用户权限管理** — SessionStore、RBAC、工具级 ACL、多用户隔离
2. **人机协同** — 节点级/工具级中断、审批流程、恢复
3. **多 Agent 与工具调用** — ToolRegistry、Eino ReAct 循环、supervisor AgentAsTool
4. **上下文管理** — 按消息数/token 裁剪、LLM 摘要压缩、tool 配对保护
5. **记忆管理** — 短期记忆（CheckpointStore）、长期记忆（MemoryStore）、跨会话偏好

### 1.2 技术栈

| 层 | 技术 |
|----|------|
| 后端语言 | Go 1.22 |
| Agent 框架 | CloudWeGo Eino v0.9.12 |
| 模型提供 | MockChatModel / OpenAI 兼容 API |
| HTTP 框架 | net/http 标准库 |
| 前端 | 原生 HTML + JS + CSS（单页应用） |

---

## 2. 架构设计

### 2.1 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│                      Web 前端 (index.html)                   │
│  登录页 | 主页三栏(用户/角色/线程 | 聊天 | 审批/工具/记忆)     │
└──────────────────────────┬──────────────────────────────────┘
                           │ HTTP / SSE
┌──────────────────────────▼──────────────────────────────────┐
│                    HTTP API 层 (httpapi/)                     │
│  Router → AuthMiddleware → Handler (auth/agent/approval/...) │
└──────────────────────────┬──────────────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────────┐
│                    业务逻辑层                                  │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐       │
│  │  auth/   │ │  hitl/   │ │  memory/ │ │contextmgr│       │
│  │ Service  │ │ Service  │ │ Service  │ │Summarizer│       │
│  │ RBACMgr  │ │Interrupt │ │          │ │ Trim*    │       │
│  │ Session  │ │ Manager  │ │          │ │ Guard    │       │
│  └──────────┘ └──────────┘ └──────────┘ └──────────┘       │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                    agent/                              │   │
│  │  Runner → SupervisorAgent → ReactAgent (×3 子Agent)   │   │
│  │  MockChatModel | OpenAI ChatModel                     │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │                    tools/                              │   │
│  │  ToolRegistry → ACLMiddleware → EinoTool              │   │
│  │  calculator weather grep query_order delete_order     │   │
│  │  send_email                                           │   │
│  └──────────────────────────────────────────────────────┘   │
└──────────────────────────┬──────────────────────────────────┘
                           │
┌──────────────────────────▼──────────────────────────────────┐
│                    存储层（接口 + 内存实现）                     │
│  SessionStore  |  CheckpointStore  |  MemoryStore            │
│  (InMemory)    |  (InMemory)       |  (InMemory)             │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 模块依赖关系

```
app.go (组装)
  ├── auth.Service ← auth.SessionStore + auth.RBACManager
  ├── tools.ToolRegistry ← tools.*Tool + tools.ACLMiddleware
  ├── hitl.Service ← hitl.InterruptManager + hitl.CheckpointStore
  ├── memory.Service ← memory.MemoryStore
  ├── contextmgr.Summarizer ← contextmgr.TokenCounter + model.BaseChatModel
  ├── agent.SupervisorAgent ← model.ToolCallingChatModel + agent.Registry
  ├── agent.Runner ← Supervisor + HITL + Registry + Memory + Summarizer
  └── httpapi.Router ← 所有 Handler
```

### 2.3 数据流

```
用户消息 → HTTP API → AuthMiddleware(校验) → Runner.Chat()
  → 加载 Thread 历史 → 注入长期记忆 → 构建 system prompt
  → 压缩上下文(TrimByToken/Summarizer.Compress)
  → Supervisor.Run() → ReAct 循环
    → LLM 推理 → ToolCallMiddleware(ACL 检查)
      → HITL 拦截(危险工具) → 审批等待 → 工具执行
      → ACL 拒绝 → 返回拒绝结果
      → 正常工具执行
  → 结果写回 Thread → 提取偏好到长期记忆
  → 返回 ChatRunResult
```

---

## 3. 模块详细设计

### 3.1 身份与权限（auth）

#### 3.1.1 AuthContext 透传机制

`AuthContext` 是贯穿整个调用链的身份上下文：

```go
type AuthContext struct {
    SessionID string
    UserID    string
    Username  string
    Roles     []string
    ThreadID  string
}
```

**传播路径**：

```
HTTP 请求 → AuthMiddleware 提取 sessionId → ValidateSession → 构建 AuthContext
  → WithAuthContext(ctx, ac) → HTTP Handler → Runner.Chat()
  → WithToolContext(ctx, map) → Eino ReAct → ToolCallMiddleware
  → EinoTool.InvokableRun() → FromToolContext(ctx) → 工具函数
```

**安全红线**：`AuthContext` 绝不暴露给 LLM。工具上下文中的 `user_id`、`roles` 等通过 `WithToolContext` 在 Go `context.Context` 中传递，不进入 LLM 的消息历史。

#### 3.1.2 SessionStore 接口

```go
type SessionStore interface {
    Create(ctx context.Context, session Session) error
    Get(ctx context.Context, sessionID string) (Session, bool, error)
    Delete(ctx context.Context, sessionID string) error
    ListByUser(ctx context.Context, userID string) ([]Session, error)
}
```

当前实现：`InMemorySessionStore`（双索引：sessionID + userID）

#### 3.1.3 RBAC 模型

内置两个角色：

| 角色 | 可用工具 |
|------|----------|
| `admin` | calculator, weather, grep, query_order, delete_order, send_email |
| `visitor` | calculator, weather, query_order |

权限检查方法：`RBACManager.CanInvokeTool(ctx, roles, toolName) bool`

#### 3.1.4 ACL 中间件拦截链

```
工具调用 → ACLMiddleware.CheckPermission()
  → 拒绝 → 返回 ACLDeniedResult（工具从未执行）
  → 允许 → 继续执行原始工具函数
```

**中间件链顺序**：ACL → Audit → HITL → 真实执行。ACL 必须先执行，拒绝时不创建审批请求。

### 3.2 人机协同（hitl）

#### 3.2.1 中断类型

| 类型 | 触发方式 | 用途 |
|------|----------|------|
| **工具级中断** | `ToolCallMiddleware` 检测 `RequiresApproval=true` | 危险工具执行前需审批 |
| **节点级中断** | `SteppedRunner` + `NodeInterruptConfig`（意图检测触发） | LLM 决定工具调用后、执行前，暂停展示计划供人工审批 |

#### 3.2.2 审批流程（异步模式）

> **重要**：本项目采用**异步审批**模式，而非"挂起等待"模式。
> 中断后 run 立即返回 `interrupted` 状态，当前执行结束。
> 审批通过独立 API 调用完成（`POST /api/approvals/{id}/decision`），
> 恢复时从 Checkpoint 加载完整 `SteppedRunState` 继续执行。

**工具级中断流程**：
```
工具调用 → ACL 通过 → HITL 检查 RequiresApproval
  → 需要 → 创建 ApprovalRequest + 保存 SteppedRunState 到 Checkpoint
  → 返回 interrupted 结果（含 interrupt_id, type=tool）
  → 用户审批（approve/reject via API）
  → 批准 → Resume: 从 Checkpoint 加载状态 → HandleApproval → 执行工具 → 继续 ReAct 循环
  → 拒绝 → Resume: 从 Checkpoint 加载状态 → 拒绝消息喂回 LLM → LLM 重新规划
```

**节点级中断流程**：
```
用户消息含"确认后再执行" → wantsNodeInterrupt() 检测到意图
  → SetNodeInterruptConfig(Enabled=true)
  → LLM 生成工具调用 → NodeInterruptConfig 触发中断
  → 保存完整 SteppedRunState（含 PendingToolCalls）到 Checkpoint
  → 返回 interrupted 结果（含 interrupt_id, type=node, 计划摘要）
  → 用户审批（approve/reject via API）
  → 批准 → Resume: PendingToolCalls 保留 → executePendingTools → 继续 ReAct 循环
  → 拒绝 → Resume: 清空 PendingToolCalls → 拒绝消息喂回 LLM → LLM 重新规划
```

#### 3.2.3 CheckpointStore 接口

```go
type CheckpointStore interface {
    Save(ctx context.Context, checkpoint Checkpoint) error
    Load(ctx context.Context, key CheckpointKey) (Checkpoint, bool, error)
    Delete(ctx context.Context, key CheckpointKey) error
    ListByThread(ctx context.Context, userID, threadID string) ([]Checkpoint, error)
}
```

当前实现：`InMemoryCheckpointStore`（按 `userID:threadID:runID` 三级键）

#### 3.2.4 幂等性保证

同一会话串行执行，审批执行前保存 `running` 凭据，完成后保存响应。重复审批返回原响应。重启后存在 `running` 且无响应的记录时，阻止自动重放并要求核对业务结果；外部副作用仍需业务系统的幂等或事务支持。

### 3.3 多 Agent 与工具（agent + tools）

#### 3.3.1 ToolRegistry 设计

```go
type ToolFunc func(ctx map[string]any, arguments string) ToolResult

type RegisteredTool struct {
    Meta ToolMeta   // 名称、描述、风险等级、是否需要审批、参数 Schema
    Fn   ToolFunc   // 工具执行函数
}
```

6 个内置工具：

| 工具 | 风险等级 | 需要审批 | 权限 |
|------|----------|----------|------|
| calculator | low | 否 | admin + visitor |
| weather | low | 否 | admin + visitor |
| query_order | low | 否 | admin + visitor |
| grep | medium | 否 | admin only |
| delete_order | high | 是 | admin only |
| send_email | high | 是 | admin only |

#### 3.3.2 Eino ReAct 集成

```
ReactAgent = react.NewAgent(config)
  config.ToolCallingModel = chatModel (MockChatModel / OpenAI)
  config.ToolsConfig = {Tools: einoTools}
  config.MaxStep = 20 (内置兜底)
```

ReAct 循环：Reason → Act (Tool Call) → Observe → Reason → ...

#### 3.3.3 Supervisor AgentAsTool 模式

```
SupervisorAgent
  ├── math_agent (calculator)         ← AgentAsTool
  ├── search_agent (weather, grep)    ← AgentAsTool
  └── general_agent (query_order, delete_order, send_email) ← AgentAsTool
```

子 Agent 通过 `agentToolWrapper` 包装为 `tool.InvokableTool`，supervisor 使用 ReAct 循环决定调用哪个子 Agent。每个子 Agent 有独立的 fresh context（只包含自己的工具）。

#### 3.3.4 双模式部署

| 模式 | 配置 | 说明 |
|------|------|------|
| Mock 模式 | `MODEL_PROVIDER=mock` | 3 子 Agent supervisor，关键词匹配 |
| 真实 LLM | `MODEL_PROVIDER=openai` | 单 Agent 全量工具，更可靠 |

### 3.4 上下文管理（contextmgr）

#### 3.4.1 消息模型

```go
type Message struct {
    Role      string         // system, user, assistant, tool
    Content   string
    Name      string
    ToolID    string
    ToolCalls []ToolCallRef
    IsSystem  bool
    IsSummary bool
    Metadata  map[string]any
}
```

#### 3.4.2 裁剪策略

| 策略 | 实现 | 说明 |
|------|------|------|
| 按消息数裁剪 | `TrimByCount(msgs, maxN)` | 保留最近 N 条非 system 消息 |
| 按 token 裁剪 | `TrimByToken(msgs, maxTokens, counter)` | 保留最近 N token 的消息 |
| tool 配对保护 | `GuardToolPairs(msgs)` | 确保 assistant+tool_call 和 tool+result 不被拆散 |
| system 永久保留 | TrimByCount/TrimByToken 内置 | system 消息不纳入裁剪预算 |

#### 3.4.3 LLM 摘要压缩

```
消息历史超过阈值(80% maxTokens)
  → 分离 system 消息
  → 保留最近 1/3 非系统消息
  → 旧 2/3 调用 LLM 生成语义摘要（Summarizer.summarizeWithLLM）
  → LLM 失败时降级到规则提取（Summarizer.summarizeWithRules）
  → 创建 summary 消息（role=assistant, metadata.summary=true）
  → 重组：system + summary + recent
  → 最终 TrimByToken 裁剪
```

### 3.5 记忆管理（memory）

#### 3.5.1 短期记忆 vs 长期记忆

| 维度 | 短期记忆 (CheckpointStore) | 长期记忆 (MemoryStore) |
|------|---------------------------|----------------------|
| 数据模型 | 状态快照（Agent 执行状态） | 事实条目（用户偏好） |
| 生命周期 | 单次 Agent 运行 | 跨会话持久 |
| 隔离键 | `userID + threadID + runID` | `userID`（跨 thread 共享） |
| 接口 | `CheckpointStore` | `MemoryStore` |

**两套接口必须独立定义，不可混用**。数据模型与生命周期截然不同。

#### 3.5.2 MemoryStore 接口

```go
type MemoryStore interface {
    Put(ctx context.Context, entry MemoryEntry) error
    Get(ctx context.Context, userID, key string) (MemoryEntry, bool, error)
    List(ctx context.Context, userID string) ([]MemoryEntry, error)
    Delete(ctx context.Context, userID, key string) error
}
```

当前实现：`InMemoryMemoryStore`（双索引：`map[userID]map[key]MemoryEntry`）

#### 3.5.3 偏好提取与注入

```
用户消息 → memory.Service.ExtractAndSave()
  → 规则匹配（"我喜欢用Python" → preferred_language=Python）
  → 写入 MemoryStore（userId 命名空间）

下次对话 → Runner.Chat() → memory.Service.BuildMemoryContext()
  → 读取用户所有偏好 → 格式化为文本 → 注入 system prompt
```

---

## 4. 接口清单

### 4.1 存储接口

| 接口 | 方法 | 内存实现 | 可替换 |
|------|------|----------|--------|
| `SessionStore` | Create, Get, Delete, ListByUser | ✅ InMemorySessionStore | ✅ 可替换为 Redis/DB |
| `CheckpointStore` | Save, Load, Delete, ListByThread | ✅ InMemoryCheckpointStore | ✅ 可替换为 Redis/DB |
| `MemoryStore` | Put, Get, List, Delete | ✅ InMemoryMemoryStore | ✅ 可替换为 Redis/DB |

### 4.2 业务接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `TokenCounter` | CountMessage, CountMessages | Token 计数（当前：SimpleTokenCounter） |
| `model.BaseChatModel` | Generate, Stream | Eino 模型接口（当前：MockChatModel / OpenAI） |

---

## 5. 多用户隔离策略

账户密码以 PHC 格式保存为随机盐 Argon2id 哈希，参数为 19 MiB 内存、2 次迭代和单线程。解析器在计算前限制内存、迭代次数、并行度、盐和输出长度，防止损坏的数据库值触发异常资源消耗。此前版本的 64 位十六进制 SHA-256 哈希仍可验证，但只在密码正确时生成 Argon2id 哈希并通过 `UserStore.UpdatePasswordHash` 原地升级。

系统不编译任何默认凭证。空用户库启动时，`BOOTSTRAP_ADMIN_USERNAME` 和 `BOOTSTRAP_ADMIN_PASSWORD` 必须同时提供，且密码至少 12 个字符。引导账户 ID 由用户名稳定生成，确保文件会话重启和多实例使用相同身份；若账户已存在则不覆盖密码或角色。持久化用户库已经包含 admin 角色账户后，可以移除引导变量。

角色更新成功后，`SessionStore.DeleteByUser` 撤销该用户的全部会话；内存、JSON 文件和 PostgreSQL 后端均实现批量撤销。每次会话校验还会重新读取当前用户并比较用户名与角色，因此另一个实例在角色更新并发窗口内创建的旧角色会话也会在使用时被拒绝和删除。

登录失败限流同时维护规范化用户名和直接连接 IP 两个桶。密钥以 SHA-256 摘要保存，避免将用户名和 IP 明文写入限流表。默认固定窗口 15 分钟内失败 5 次后锁定 15 分钟；锁定时 HTTP 返回 429 和 `Retry-After`。PostgreSQL 后端使用 `agent_login_rate_limits` 共享状态，失败次数在事务内通过排序后的 advisory lock 串行更新。成功登录清除相应计数；存储失败时不放行登录。应用不使用未经可信代理认证的 `X-Forwarded-For`，因此在反向代理后按代理直接连接 IP 计数，应在代理侧另设客户端限流。

### 5.1 隔离键设计

| 数据类型 | 隔离键 | 说明 |
|----------|--------|------|
| Session | sessionID | 每次登录生成唯一 session |
| Thread 消息 | userID + threadID | 不同用户同 threadID 不共享 |
| Checkpoint | userID + threadID + runID | 三级隔离 |
| 长期记忆 | userID | 同用户跨 thread 共享 |
| 订单数据 | userID | 用户只能查自己的订单 |

### 5.2 工具参数不可信原则

工具身份由框架从 Go context 中间件注入的 `AuthContext` 派生为**类型化的 `auth.ToolIdentity`**（UserID/Roles/ThreadID/RunID），经 `ToolFunc` 签名强制传入工具，**不从工具参数中读取**，也没有可伪造的非类型化 map 通道。这防止用户伪造 `user_id` 访问他人数据。

```go
// 工具函数内部（identity 由框架构造，无法从 LLM 参数伪造）
if identity == nil || identity.UserID == "" { ... }  // ← 无身份即拒绝
orders := store.QueryByUser(identity.UserID)          // ← 只能查自己的数据
```

### 5.3 框架强制机制（非调用方自觉）

隔离不是靠各处代码"记得用 AuthContext.UserID"，而是由框架在四个层面强制：

| 层 | 机制 | 效果 |
|----|------|------|
| 工具边界 | `ToolFunc func(*auth.ToolIdentity, string) ToolResult` — 身份是类型化参数，只能由框架从 `AuthContext` 构造（`auth.ToolIdentityFromContext`），无身份（nil）即拒绝 | 工具拿不到伪造的身份 |
| 运行时资源 | 线程按 `(userID, threadID)` 复合键存储；运行事件在内存模式记录属主，在 PostgreSQL 模式按 `(user_id, run_id)` 读取；审批/恢复校验 `req.UserID == 当前用户` | 任何用户无法读/删他人线程、事件、审批 |
| 存储层 | `auth.CheckUserScope(ctx, userID)`：当 ctx 携带身份时，所有 Memory/Checkpoint/Vector 访问的 userID 参数必须与之一致，否则报"cross-user access denied" | 即使上游某处传错 userID，框架调用链内也无法跨用户读写 |
| ACL 预检 | RBAC 预检无条件执行（空角色/无身份一律拒绝，无旁路） | 无角色的调用不可能触达工具 |

> 存储层校验对"无身份 ctx"（内部后台任务，如快照落盘 goroutine）放行，避免误伤合法的框架内部调用。

### 5.4 存储后端可替换（持久化实现）

会话、检查点、长期记忆和线程均有 `memory`（默认）、`file`（单进程 JSON）与 `postgres`（多实例）实现；用户账户和示例业务数据提供 `memory` / `postgres`，审批提供 `memory` / `file` / `postgres`。对应环境变量为 `USER_STORE`、`SESSION_STORE`、`CHECKPOINT_STORE`、`MEMORY_STORE`、`THREAD_STORE`、`APPROVAL_STORE` 和 `BUSINESS_STORE`。

file 版采用**装饰器模式**：内嵌对应的 InMemory 实现并拦截全部写操作（Create/Update/Delete/Save），每次写盘后原子落盘（临时文件 + rename，崩溃不损坏文件），启动时从文件恢复状态。由于隔离校验在内存实现内部，file 版自动继承 `CheckUserScope` 强制，隔离语义不因后端切换而减弱。

PostgreSQL 版在启动时执行编译进程序的版本化 SQL 迁移。`agent_schema_migrations` 记录版本、名称、SHA-256 校验和与执行时间；全局 advisory lock 串行化多个实例的迁移过程，每个版本在独立事务中执行。迁移历史被改写、数据库版本高于当前程序或任一版本执行失败时，应用停止启动。初始迁移幂等创建用户、会话、线程、检查点、记忆、审批、订单、邮件记录和工具幂等凭据表，可兼容升级此前由启动代码自动建表的数据库。

只要启用了 PostgreSQL 后端，运行结果及事件也存入 `agent_runs`。读取以用户 ID 与运行 ID 联合定位，审批恢复会追加之前的事件。`RUN_EVENT_RETENTION` 设置保留期限（默认 168 小时）；查询过滤过期记录，后续写入清理过期行。写入失败会返回运行错误，避免把未持久化的结果报告为成功。

当 Checkpoint、线程和审批三个存储均选 PostgreSQL 时，创建中断的三项记录由 `PublishInterrupt` 在一个数据库事务内提交。若最后的审批插入失败，前面的 Checkpoint 和线程写入也回滚；新审批 ID 冲突会拒绝此次提交，不覆盖旧审批。混合后端无法共享数据库事务，继续使用逐项写入路径。

恢复时先原子认领审批，再执行模型和工具。恢复产生的数据库状态由 `PublishResume` 一次提交：完成时写线程历史、旧审批的完整响应及运行事件；再次中断时写新 Checkpoint、新审批、线程历史、旧审批响应及运行事件。任何一步失败都回滚这些收尾写入，并保留旧审批的 `running` 认领以禁止可能重复的外部副作用。外部工具调用不在数据库事务中，需依赖幂等凭据或业务对账。

线程执行先取得基于 `(userID, threadID)` 的 PostgreSQL advisory lock，使不同应用实例对同一线程串行更新；锁使用独立连接池，避免长时间持锁耗尽业务 SQL 连接。审批通过带状态条件的 `UPDATE ... RETURNING` 原子认领，只有一个实例能从 `pending` 转为 `running`。订单使用软删除，`(runID, toolCallID)` 派生的幂等键保存原始删除结果；邮件示例记录使用唯一幂等键，重复恢复不会生成第二条记录。连接池由 `DATABASE_MAX_OPEN_CONNS`、`DATABASE_MAX_IDLE_CONNS` 和 `DATABASE_CONN_MAX_LIFETIME` 控制。

部署探针分为存活与就绪两类。`/healthz` 只确认 HTTP 进程可响应；`/readyz` 使用 2 秒独立 context 检查服务依赖。PostgreSQL 模式会读取完整迁移历史并验证版本、名称和校验和，因此数据库断连、迁移缺失、历史被改写或数据库版本高于程序时均返回 503。响应不包含底层错误，避免泄露连接与结构信息。

---

## 6. 恢复语义

### 6.1 工具级中断恢复

**语义**：恢复时从 Checkpoint 加载完整 `SteppedRunState`，执行被中断的工具，然后继续 ReAct 循环。

**恢复流程**：
1. 校验审批属主，取得会话执行锁，重新读取审批记录。
2. 已保存响应时直接返回；不同的审批决定被拒绝；`running` 且无响应时报告结果不确定，禁止重放。
3. 从该审批自己的 `State` 恢复完整 `SteppedRunState`，包括 `Children` 中的子 Agent 状态。缺失或不匹配时直接报错，不使用线程历史猜测执行步骤。
4. 保存决定和 `running` 凭据，再通过 `HandleApproval()` 精确匹配 `toolCallID`、工具名称、参数，执行或拒绝该调用。
5. 共用 `advance` 循环继续执行。再次中断时保存新审批和状态并返回新的 `interrupt`；完成时保存对话。
6. 保存本次审批的完整响应和 `finished` 标记，再返回客户端。

`CHECKPOINT_STORE=file` 同时在 `<CHECKPOINT_STORE_PATH>.approvals.json` 保存审批、嵌套状态和执行响应。JSON 后端仅支持单进程独占写入；外部副作用与本地凭据并非同一事务，崩溃窗口需要业务对账。订单、邮件示例数据仍在内存中。

### 6.2 节点级中断恢复

**语义**：恢复该审批记录的 `SteppedRunState`，根据决定处理 PendingToolCalls。

**恢复流程**：
1. `Runner.Resume()` 检测 `req.NodeName != ""`，识别为节点级中断
2. 从该审批记录加载完整 `SteppedRunState`
3. 调用 `SteppedRunner.HandleApproval()`：
   - **批准**：PendingToolCalls 保留，下一轮 `executePendingTools()` 执行所有待定工具
   - **拒绝**：为每个待执行调用补充配对的拒绝结果，再清空 PendingToolCalls，让 LLM 重新规划
4. 继续执行 ReAct 循环

**节点级中断触发条件**：
- 请求显式设置 `confirmBeforeExecute=true`，不检测消息关键词
- SteppedRunner 在 LLM 返回工具调用后、执行前触发中断
- 中断前先保存完整待执行批次；批准计划后高风险工具仍需要单独审批
- 同批调用逐个出队，子 Agent 完成仅移除对应调用；子 Agent 中断时保留其完整状态

### 6.3 重试、取消与存储失败

模型限流最多重试 3 次，退避 250/500/1000 ms，工具副作用不自动重试。每次聊天和恢复限时 2 分钟，HTTP context 贯穿执行和退避；网络工具通过 `ToolIdentity.Context` 接收取消。

线程与检查点写入先构造新快照，写入并同步临时文件，重命名成功后再发布内存状态。失败返回错误，不把仅存于内存的数据报告成已保存。审批凭据写入失败时不启动工具，结果写入失败时阻止重放。检查点、审批或线程存储初始化失败时停止启动，不静默降级。

---

## 7. 配置项

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `MODEL_PROVIDER` | `mock` | 模型提供方：mock / openai / ark |
| `OPENAI_BASE_URL` | - | OpenAI 兼容 API 地址 |
| `OPENAI_API_KEY` | - | API Key |
| `OPENAI_MODEL` | - | 模型名称 |
| `ADDR` | `:8080` | HTTP 监听地址 |
| `SESSION_TTL` | `30m` | 会话滑动过期时间（如 30m/2h），每次校验成功自动续期 |
| `BOOTSTRAP_ADMIN_USERNAME` | - | 空用户库首次启动时创建的管理员用户名 |
| `BOOTSTRAP_ADMIN_PASSWORD` | - | 初始管理员密码，至少 12 个字符，必须与用户名同时设置 |
| `LOGIN_MAX_FAILURES` | `5` | 失败窗口内允许的次数 |
| `LOGIN_FAILURE_WINDOW` | `15m` | 失败计数窗口 |
| `LOGIN_LOCKOUT` | `15m` | 达到阈值后的锁定时长 |
| `USER_STORE` | `memory` | 用户账户存储后端：`memory` / `postgres` |
| `SESSION_STORE` | `memory` | 会话存储后端：`memory` / `file` / `postgres` |
| `SESSION_STORE_PATH` | `data/sessions.json` | file 后端数据文件路径 |
| `CHECKPOINT_STORE` | `memory` | 检查点存储后端：`memory` / `file` / `postgres` |
| `CHECKPOINT_STORE_PATH` | `data/checkpoints.json` | file 后端数据文件路径 |
| `MEMORY_STORE` | `memory` | 长期记忆存储后端：`memory` / `file` / `postgres` |
| `MEMORY_STORE_PATH` | `data/memory.json` | file 后端数据文件路径 |
| `THREAD_STORE` | `memory` | 对话线程存储后端：`memory` / `file` / `postgres` |
| `THREAD_STORE_PATH` | `data/threads.json` | file 后端数据文件路径 |
| `APPROVAL_STORE` | 同 `CHECKPOINT_STORE` | 审批存储后端：`memory` / `file` / `postgres` |
| `BUSINESS_STORE` | `memory` | 示例订单、邮件记录和工具幂等凭据：`memory` / `postgres` |
| `DATABASE_URL` | - | PostgreSQL DSN；任一存储使用 `postgres` 时必填 |
| `DATABASE_MAX_OPEN_CONNS` | `25` | PostgreSQL 最大打开连接数 |
| `DATABASE_MAX_IDLE_CONNS` | `5` | PostgreSQL 最大空闲连接数 |
| `DATABASE_CONN_MAX_LIFETIME` | `30m` | PostgreSQL 连接最长复用时间 |
| `MAX_TOKENS` | `8000` | 上下文 token 上限 |
| `MAX_MESSAGES` | `30` | 最大消息数 |
| `SUMMARIZE_THRESHOLD_RATIO` | `0.8` | 摘要触发阈值比例 |
| `SUMMARY_TARGET_TOKENS` | `800` | 摘要目标 token 数 |

---

## 8. 项目结构

```
agent-eino-demo/
  cmd/server/main.go          # 入口
  internal/
    app/                       # 应用组装与配置
    auth/                      # 身份与权限
    hitl/                      # 人机协同
    agent/                     # Agent 编排
    tools/                     # 工具系统
    contextmgr/                # 上下文管理
    memory/                    # 记忆管理
    httpapi/                   # REST API
  integration_test/            # 集成测试
  web/                         # 前端页面
  docs/                        # 文档
```

---

## 9. 记忆系统 v2 设计

在模块 05 的 KV 记忆之上，v2 补齐冲突消解、相关性检索与生命周期管理三块能力。

### 9.1 数据模型

`MemoryEntry` 新增字段（全部 omitempty，旧数据文件向后兼容）：

| 字段 | 说明 |
|------|------|
| `type` | preference / identity / fact / episode / rule，空视为 preference |
| `importance` | 1-5，零值视为 3 |
| `source_thread_id` / `source_excerpt` | 记忆来源的对话线程与原句摘录 |
| `access_count` / `last_accessed_at` | 检索命中统计 |
| `archived` | 归档标记（不删除，仅退出检索与默认列表） |
| `history` | 被覆盖的旧值修订链（保留最近 5 版） |

### 9.2 写入与冲突消解

`UpsertPreference` 为唯一写入口：key 已存在且值不同 → 旧值进 `history`（新值生效）；值相同 → no-op。

**提取策略为"LLM 主导 + 规则兜底"**：配置了模型时，LLM 提取拥有本轮决定权（结果经轻量防幻觉校验——枚举类 key 如语言/框架/编辑器/城市，其 value 必须在消息原文中出现才落库，防止模型编造）；规则提取仅在无模型或模型调用/解析失败时兜底（如 mock 模式），保证无真实 LLM 时提取依然确定可用。带偏好信号的消息额外写入 `type=episode` 情景条目并进入向量索引；普通闲聊不产生任何写入。

### 9.3 统一检索

`RetrieveRelevant(ctx, userID, query, budget)` 是记忆注入的唯一入口：

```
score = 关键词重叠×2 (query-aware) + importance/5 + exp(-小时/168) (一周半衰) + min(access×0.05, 0.5)
```

- 按分数排序，在 `MEMORY_BUDGET_TOKENS` 预算内装配"确定性记忆"与"相关历史记忆"两段
- 命中条目 `access_count++` 并刷新时间戳（使用即强化，越常用越靠前）
- 归档条目永远不参与检索

### 9.4 生命周期（遗忘 / 整合 / 沉淀）

`Consolidate` 执行一轮整理：

1. **遗忘**：有效分低于 0.5 的条目 `archived=true`（重要度低且长期未被访问的自然衰减退出）
2. **整合**：活跃条目达到 `MEMORY_CONSOLIDATE_THRESHOLD` 且有 LLM 时，由模型生成 `user_profile` 用户画像、判定过时条目归档、并从情景沉淀新事实（new_facts）
3. **降级**：无 LLM（mock 模式）或材料不足时，用规则拼接简版画像，遗忘仍然执行

### 9.5 API 与界面

- `POST /api/memory/consolidate`：手动触发整理，返回归档数/画像/新事实
- 前端记忆面板：按类型分组、重要度星标、来源 tooltip、归档折叠区、"🧹 整合记忆"按钮
