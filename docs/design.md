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

每个审批通过的工具执行都通过 `runID + toolCallID` 生成 `idempotencyKey`，同一 key 的重复执行只返回第一次的结果。

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
| 运行时资源 | 线程按 `(userID, threadID)` 复合键存储（`threadStore`）；run 事件记录属主（`runOwners`）；审批/恢复校验 `req.UserID == 当前用户` | 任何用户无法读/删他人线程、事件、审批 |
| 存储层 | `auth.CheckUserScope(ctx, userID)`：当 ctx 携带身份时，所有 Memory/Checkpoint/Vector 访问的 userID 参数必须与之一致，否则报"cross-user access denied" | 即使上游某处传错 userID，框架调用链内也无法跨用户读写 |
| ACL 预检 | RBAC 预检无条件执行（空角色/无身份一律拒绝，无旁路） | 无角色的调用不可能触达工具 |

> 存储层校验对"无身份 ctx"（内部后台任务，如快照落盘 goroutine）放行，避免误伤合法的框架内部调用。

---

## 6. 恢复语义

### 6.1 工具级中断恢复

**语义**：恢复时从 Checkpoint 加载完整 `SteppedRunState`，执行被中断的工具，然后继续 ReAct 循环。

**恢复流程**：
1. `Runner.Resume()` 从 `ApprovalRequest` 获取 `interruptID` 和 `RunID`
2. 优先从 Checkpoint 加载 `SteppedRunState`（含完整对话历史 + PendingToolCalls）
3. 调用 `SteppedRunner.HandleApproval()` 执行被中断的工具或注入拒绝消息
4. 继续执行 ReAct 循环直到完成或再次中断
5. 若 Checkpoint 不可用，降级为线程重建模式（从线程历史重建对话）

**降级路径**（线程重建模式）：
- 从线程历史中恢复 assistant 的 tool_call 消息
- 追加工具结果消息（执行/拒绝）
- 重建对话后重新运行 `SteppedRunner.RunAll()`

**幂等性保证**：
- 每个 HITL 审批通过的工具执行都通过 `runID + toolCallID` 生成 `idempotencyKey`
- 同一 `idempotencyKey` 的重复执行只返回第一次的结果
- 危险工具（delete_order、send_email）必须保证幂等性

### 6.2 节点级中断恢复

**语义**：恢复时从 Checkpoint 加载 `SteppedRunState`，根据审批结果决定是否执行 PendingToolCalls。

**恢复流程**：
1. `Runner.Resume()` 检测 `req.NodeName != ""`，识别为节点级中断
2. 从 Checkpoint 加载完整 `SteppedRunState`
3. 调用 `SteppedRunner.HandleApproval()`：
   - **批准**：PendingToolCalls 保留，下一轮 `executePendingTools()` 执行所有待定工具
   - **拒绝**：清空 PendingToolCalls，注入拒绝消息，LLM 重新规划
4. 继续执行 ReAct 循环

**节点级中断触发条件**：
- 用户消息包含"确认后再执行"、"先审批再执行"等关键词
- `wantsNodeInterrupt()` 检测意图 → `SetNodeInterruptConfig(Enabled=true)`
- SteppedRunner 在 LLM 返回工具调用后、执行前触发中断
- 中断时保存完整 `SteppedRunState`（含对话历史、步骤数、PendingToolCalls）

---

## 7. 配置项

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `MODEL_PROVIDER` | `mock` | 模型提供方：mock / openai / ark |
| `OPENAI_BASE_URL` | - | OpenAI 兼容 API 地址 |
| `OPENAI_API_KEY` | - | API Key |
| `OPENAI_MODEL` | - | 模型名称 |
| `ADDR` | `:8080` | HTTP 监听地址 |
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
