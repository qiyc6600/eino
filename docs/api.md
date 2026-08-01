# API 文档

> 本文档描述 Agent Eino Demo 项目的所有 REST API 端点，包括请求/响应格式和示例。

---

## 通用说明

### 认证方式

除 `/api/auth/login` 外，所有端点均需在请求头中携带 Session ID：

```
Authorization: Bearer <sessionId>
```

或通过查询参数传递：

```
?sessionId=<sessionId>
```

### 响应格式

所有响应均为 JSON 格式。错误响应格式：

```json
{"error": "错误描述"}
```

### 状态码

| 状态码 | 含义 |
|--------|------|
| 200 | 成功 |
| 201 | 创建成功 |
| 400 | 请求参数错误 |
| 401 | 未认证（缺少或无效的 session） |
| 404 | 资源不存在 |
| 409 | 冲突（如用户名重复） |
| 500 | 服务器内部错误 |

---

## 1. 认证

### POST /api/auth/login

用户登录，创建会话。

**请求体**：

```json
{
  "username": "admin",
  "password": "admin123"
}
```

**成功响应** (200)：

```json
{
  "sessionId": "s_550e8400-e29b-41d4-a716-446655440000",
  "user": {
    "id": "u_admin",
    "username": "admin",
    "roles": ["admin"]
  }
}
```

**失败响应** (401)：

```json
{"error": "invalid credentials"}
```

**默认账户**：

| 用户名 | 密码 | 角色 |
|--------|------|------|
| admin | admin123 | admin |
| visitor | visitor123 | visitor |

---

### GET /api/auth/me

获取当前登录用户信息及可用工具列表。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{
  "sessionId": "s_550e8400...",
  "user": {
    "id": "u_admin",
    "username": "admin",
    "roles": ["admin"]
  },
  "tools": ["calculator", "weather", "grep", "query_order", "delete_order", "send_email"]
}
```

---

### POST /api/auth/logout

登出当前会话。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{"status": "ok"}
```

---

## 2. 用户管理

### GET /api/users

列出所有用户（不含密码哈希）。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {"id": "u_admin", "username": "admin", "roles": ["admin"]},
  {"id": "u_visitor", "username": "visitor", "roles": ["visitor"]}
]
```

---

### POST /api/users

创建新用户。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "username": "newuser",
  "password": "password123",
  "roles": ["visitor"]
}
```

**成功响应** (201)：

```json
{
  "id": "u_a1b2c3d4",
  "username": "newuser",
  "roles": ["visitor"]
}
```

**冲突响应** (409)：

```json
{"error": "user already exists: newuser"}
```

---

### GET /api/roles

列出所有角色及其权限。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {
    "name": "admin",
    "permissions": [
      {"resource": "tool", "action": "invoke", "name": "calculator"},
      {"resource": "tool", "action": "invoke", "name": "weather"},
      {"resource": "tool", "action": "invoke", "name": "grep"},
      {"resource": "tool", "action": "invoke", "name": "query_order"},
      {"resource": "tool", "action": "invoke", "name": "delete_order"},
      {"resource": "tool", "action": "invoke", "name": "send_email"}
    ]
  },
  {
    "name": "visitor",
    "permissions": [
      {"resource": "tool", "action": "invoke", "name": "calculator"},
      {"resource": "tool", "action": "invoke", "name": "weather"},
      {"resource": "tool", "action": "invoke", "name": "query_order"}
    ]
  }
]
```

---

### PUT /api/users/{userId}/roles

更新用户角色。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "roles": ["admin", "visitor"]
}
```

**成功响应** (200)：

```json
{"status": "ok"}
```

---

## 3. Agent 聊天

### POST /api/agent/chat

发送消息给 Agent，获取回复。支持同步和 SSE 流式两种模式。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "message": "1+1等于多少",
  "threadId": "t_default",
  "stream": false
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| message | string | 是 | 用户消息 |
| threadId | string | 否 | 会话线程 ID，默认 `t_default` |
| stream | bool | 否 | 是否使用 SSE 流式响应，默认 `false` |

#### 同步模式 (stream=false)

**成功响应** (200)：

```json
{
  "run_id": "r_a1b2c3d4",
  "status": "completed",
  "answer": "1+1=2",
  "thread_id": "t_default",
  "events": [
    {"id": "e_1", "run_id": "r_a1b2c3d4", "type": "model_call_start", "detail": "...", "timestamp": "..."},
    {"id": "e_2", "run_id": "r_a1b2c3d4", "type": "tool_call_start", "detail": "calculator", "timestamp": "..."},
    {"id": "e_3", "run_id": "r_a1b2c3d4", "type": "tool_call_end", "detail": "1+1 = 2", "timestamp": "..."},
    {"id": "e_4", "run_id": "r_a1b2c3d4", "type": "model_call_end", "detail": "1+1=2", "timestamp": "..."}
  ]
}
```

**中断响应** (200, status=interrupted)：

```json
{
  "run_id": "r_e5f6g7h8",
  "status": "interrupted",
  "answer": "该操作需要审批才能执行，已创建审批请求，请在审批中心处理。",
  "thread_id": "t_default",
  "interrupt_id": "i_9a0b1c2d",
  "events": [
    {"id": "e_1", "type": "hitl_interrupt", "detail": "delete_order requires approval", ...}
  ]
}
```

#### SSE 流式模式 (stream=true)

**响应头**：

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

**事件类型**：

| 事件 | 格式 | 说明 |
|------|------|------|
| `chunk` | `event: chunk\ndata: {"content":"..."}\n\n` | 模型输出片段 |
| `tool_call` | `event: tool_call\ndata: {"name":"calculator","arguments":"..."}\n\n` | 工具调用通知 |
| `done` | `event: done\ndata: {"status":"completed","answer":"...","threadId":"..."}\n\n` | 完成事件 |
| `error` | `event: error\ndata: {"error":"..."}\n\n` | 错误事件 |

---

### GET /api/agent/runs/{runId}/events

获取指定运行的执行事件列表。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {"id": "e_1", "run_id": "r_a1b2c3d4", "type": "model_call_start", "detail": "...", "timestamp": "..."},
  {"id": "e_2", "run_id": "r_a1b2c3d4", "type": "tool_call_start", "detail": "calculator", "timestamp": "..."}
]
```

---

## 4. 线程管理

### GET /api/chat/threads

列出所有会话线程 ID。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
["t_default", "t_demo_1", "t_demo_2"]
```

---

### POST /api/chat/threads

创建新会话线程。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "threadId": "t_my_thread"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| threadId | string | 否 | 自定义线程 ID，不传则自动生成 |

**成功响应** (201)：

```json
{"threadId": "t_my_thread"}
```

---

### GET /api/chat/{threadId}/messages

获取指定线程的消息历史。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {"role": "user", "content": "1+1等于多少"},
  {"role": "assistant", "content": "1+1=2"}
]
```

---

## 5. 审批

### GET /api/approvals

列出当前用户的待审批请求。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {
    "interrupt_id": "i_9a0b1c2d",
    "type": "tool",
    "run_id": "r_e5f6g7h8",
    "user_id": "u_admin",
    "thread_id": "t_default",
    "tool_name": "delete_order",
    "arguments": "{\"order_id\":\"A-1001\"}",
    "risk_level": "high",
    "message": "删除订单 A-1001",
    "status": "pending",
    "created_at": "2026-07-30T12:00:00Z"
  }
]
```

---

### GET /api/approvals/{interruptId}

获取指定审批请求详情。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{
  "interrupt_id": "i_9a0b1c2d",
  "type": "tool",
  "run_id": "r_e5f6g7h8",
  "tool_name": "delete_order",
  "arguments": "{\"order_id\":\"A-1001\"}",
  "risk_level": "high",
  "status": "pending"
}
```

**未找到** (404)：

```json
{"error": "approval not found"}
```

---

### POST /api/approvals/{interruptId}/decision

对审批请求做出决定（批准或拒绝）。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "approved": true,
  "reason": "确认删除"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| approved | bool | 是 | 是否批准 |
| reason | string | 否 | 决定原因 |

**批准成功响应** (200)：

```json
{
  "runId": "r_e5f6g7h8",
  "status": "completed",
  "answer": "操作已批准并执行。",
  "approved": true
}
```

**拒绝成功响应** (200)：

```json
{
  "runId": "r_e5f6g7h8",
  "status": "completed",
  "answer": "操作已被拒绝：风险太高",
  "approved": false
}
```

---

## 6. 工具

### GET /api/tools

列出所有注册的工具及其元数据。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {
    "name": "calculator",
    "description": "Evaluate a mathematical expression",
    "risk_level": "low",
    "requires_approval": false,
    "param_schema": "{\"type\":\"object\",\"properties\":{\"expression\":{\"type\":\"string\"}}}"
  },
  {
    "name": "delete_order",
    "description": "Delete an order. Admin only.",
    "risk_level": "high",
    "requires_approval": true,
    "param_schema": "{\"type\":\"object\",\"properties\":{\"order_id\":{\"type\":\"string\"}},\"required\":[\"order_id\"]}"
  }
]
```

---

### GET /api/tools/{toolName}

获取指定工具的详细信息。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{
  "name": "weather",
  "description": "Query real-time weather for any city",
  "risk_level": "low",
  "requires_approval": false,
  "param_schema": "{\"type\":\"object\",\"properties\":{\"city\":{\"type\":\"string\"}}}"
}
```

**未找到** (404)：

```json
{"error": "tool not found: nonexistent"}
```

---

## 7. 记忆

### GET /api/memory

列出当前用户的所有长期记忆（偏好）。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {
    "user_id": "u_admin",
    "key": "preferred_language",
    "value": "Python",
    "source": "user_stated",
    "created_at": "2026-07-30T12:00:00Z",
    "updated_at": "2026-07-30T12:00:00Z"
  }
]
```

---

### POST /api/memory

写入一条长期记忆。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "key": "preferred_language",
  "value": "Go"
}
```

**成功响应** (200)：

```json
{"status": "ok"}
```

---

### DELETE /api/memory/{key}

删除指定键的长期记忆。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{"status": "ok"}
```

---

## 附录：事件类型

Agent 运行过程中记录的事件类型：

| 事件类型 | 说明 |
|----------|------|
| `model_call_start` | LLM 推理开始 |
| `model_call_end` | LLM 推理结束 |
| `tool_call_start` | 工具调用开始 |
| `tool_call_end` | 工具调用结束 |
| `acl_denied` | ACL 权限拒绝 |
| `hitl_interrupt` | HITL 中断 |
| `hitl_resume` | HITL 恢复 |
| `summary_compress` | 上下文摘要压缩 |
| `agent_start` | Agent 开始执行 |
| `agent_end` | Agent 执行结束 |
| `supervisor_route` | Supervisor 路由决策 |
