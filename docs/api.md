# API 文档

> 本文档描述 Agent Eino Demo 项目的所有 REST API 端点，包括请求/响应格式和示例。

---

## 通用说明

### 认证方式

除登录接口与静态页面外，所有接口都需要认证。支持两种方式，**请求头优先**：

| 方式 | 适用场景 | 说明 |
|------|----------|------|
| `Authorization: Bearer <sessionId>` | 脚本、CLI、第三方客户端 | 登录响应体中的 `sessionId` |
| 会话 Cookie `agent_session` | 浏览器 | 登录时由服务端下发，`HttpOnly` + `SameSite=Strict`，页面脚本读不到，刷新页面仍保持登录 |

URL 查询参数**不接受**作为凭证：URL 会进入浏览器历史、访问日志与 Referer 头。

除 `/healthz`、`/readyz` 和 `/api/auth/login` 外，所有端点均需在请求头中携带 Session ID：

```
Authorization: Bearer <sessionId>
```

> ⚠️ 仅支持请求头传递。**查询参数（`?sessionId=`）不被接受**——URL 中的凭证会泄漏到浏览器历史、访问日志与 Referer 头，后端对查询参数一律返回 401。

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
| 503 | 服务依赖尚未就绪 |
| 429 | 登录失败次数超过限制（带 `Retry-After` 秒数） |

---

## 健康检查（公开）

### GET /healthz

进程存活探针，不检查外部依赖。成功时返回：

```json
{"status": "ok"}
```

### GET /readyz

流量就绪探针。内存/文件模式在应用初始化完成后直接就绪；PostgreSQL 模式会在 2 秒超时内查询迁移表，检查数据库连接、全部预期版本及迁移校验和。

成功响应（200）：

```json
{"status": "ready"}
```

依赖不可用、迁移缺失或迁移历史不匹配时返回通用响应，不暴露数据库错误（503）：

```json
{"status": "unavailable"}
```

两个端点仅接受 `GET`，并返回 `Cache-Control: no-store`。

---

## 1. 认证

### POST /api/auth/login

用户登录，创建会话。

**请求体**：

```json
{
  "username": "admin",
  "password": "<BOOTSTRAP_ADMIN_PASSWORD>"
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

同时下发会话 Cookie（浏览器无需处理响应体中的 `sessionId`）：

```
Set-Cookie: agent_session=s_550e8400-...; Path=/; Max-Age=1800; HttpOnly; Secure; SameSite=Strict
```

`Max-Age` 跟随 `SESSION_TTL`；`Secure` 由 `SESSION_COOKIE_SECURE` 控制（普通 HTTP 的局域网地址下需关闭，否则浏览器会丢弃该 Cookie）。

**失败响应** (401)：

```json
{"error": "invalid credentials"}
```

默认按用户名和直接连接 IP 各自统计：15 分钟内失败 5 次后锁定 15 分钟。锁定期间返回 429，响应包含 `Retry-After` 秒数；限流存储不可用时返回通用 503。成功登录清零对应计数。

项目没有默认账户。空用户库启动时，通过 `BOOTSTRAP_ADMIN_USERNAME` 和 `BOOTSTRAP_ADMIN_PASSWORD` 创建初始管理员。

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

登出当前会话：服务端删除会话记录，并让会话 Cookie 立即过期（`Max-Age=-1`）。

**认证**：`Authorization: Bearer <sessionId>` 或会话 Cookie

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

更新用户角色。更新成功后，该用户的所有现有会话立即撤销，需要使用新角色重新登录；其他用户的会话不受影响。

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
  "stream": false,
  "confirmBeforeExecute": false
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| message | string | 是 | 用户消息 |
| threadId | string | 否 | 会话线程 ID，默认 `t_default` |
| stream | bool | 否 | 是否使用 SSE 流式响应，默认 `false` |
| confirmBeforeExecute | bool | 否 | 开启节点级中断：LLM 生成执行计划后先暂停等待审批，再执行工具。按请求传递，不影响并发请求 |

#### 同步模式 (stream=false)

**成功响应** (200)：

```json
{
  "run_id": "r_ca24550c-3e1c-417d-bcb0-10c3fbb2e337",
  "status": "completed",
  "answer": "1+1=2",
  "routed_agent": "assistant",
  "events": [
    {"id": "e_1", "run_id": "r_...", "type": "model_call_start", "timestamp": "...", "detail": "Model call (attempt 1)", "metadata": {"attempt": 1}},
    {"id": "e_2", "run_id": "r_...", "type": "model_call_end", "timestamp": "...", "detail": "Model call finished", "metadata": {"attempt": 1, "failed": false, "prompt_tokens": 192, "completion_tokens": 68}}
  ],
  "context_tokens": {"current": 1430, "threshold": 1733, "max": 2000},
  "actual_tokens": {"last_prompt_tokens": 192, "completion_tokens": 68, "total_tokens": 260, "calls": 1}
}
```

`context_tokens` 是本地估算（含工具定义开销与为回答预留的空间）；`actual_tokens` 是模型服务商实际返回的用量，仅在服务商提供时出现。`status` 取值为 `completed` / `interrupted` / `error` / `cancelled`，失败与取消时 `answer` 承载错误说明。

**中断响应** (200, status=interrupted)：

```json
{
  "run_id": "r_e5f6g7h8",
  "status": "interrupted",
  "answer": "",
  "interrupt": {
    "interrupt_id": "i_9a0b1c2d",
    "type": "tool",
    "tool_name": "delete_order",
    "arguments": "{\"order_id\":\"A-1001\"}",
    "message": "高危工具 delete_order 需要审批",
    "plan": [{"name": "delete_order", "arguments": "{\"order_id\":\"A-1001\"}"}]
  },
  "events": [
    {"id": "e_1", "type": "hitl_interrupt", "tool_name": "delete_order", "detail": "Tool delete_order requires approval", "metadata": {"reason": "high_risk_tool"}}
  ]
}
```

`interrupt.type` 为 `tool`（高危工具审批）或 `node`（执行计划审批）；`plan` 仅在节点级中断时出现。中断后本次 run 结束，需通过审批接口恢复。

#### SSE 流式模式 (stream=true)

**响应头**：

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

**事件类型**：

| 事件 | 数据字段 | 说明 |
|------|----------|------|
| `chunk` | `content` | 模型输出的增量片段，需按到达顺序拼接 |
| `tool_call` | `phase`、`tool`、`detail`，`end` 阶段另有 `result` | 执行进度，见下表 |
| `done` | 见下 | 结束事件，**必须**以它为准判定结果 |

`tool_call` 的 `phase` 取值：

| phase | 含义 |
|-------|------|
| `route` | Supervisor 路由到某个子 Agent（`tool` 为子 Agent 名） |
| `start` | 开始调用工具 |
| `end` | 工具执行完成，`result` 为截断后的结果摘要 |
| `denied` | 被 ACL 拦截 |
| `approval` | 命中高危工具，等待人工审批 |

实际帧示例：

```
event: chunk
data: {"content":"我可以帮您完"}

event: tool_call
data: {"detail":"Routing to sub-agent math_agent","phase":"route","tool":"math_agent"}

event: tool_call
data: {"detail":"Calling tool calculator","phase":"start","tool":"calculator"}

event: tool_call
data: {"detail":"Tool calculator executed","phase":"end","result":"1 + 1 = 2","tool":"calculator"}
```

`done` 帧字段：

```json
{
  "status": "completed",
  "answer": "1+1=2",
  "threadId": "t_default",
  "runId": "r_...",
  "interrupt": null,
  "memory": [],
  "events": [],
  "contextTokens": {"current": 1430, "threshold": 1733, "max": 2000},
  "actualTokens": {"last_prompt_tokens": 192, "completion_tokens": 68, "total_tokens": 260, "calls": 1},
  "streamed": true
}
```

- 服务端不发送 `error` 事件：失败与取消统一通过 `done` 的 `status` 表达（`error` / `cancelled`），`answer` 承载原因。
- `streamed=true` 表示答案已经通过 `chunk` 逐段下发，客户端不应再把 `answer` 追加到已拼接的内容之后（会重复）。
- 失败或取消时不会下发 `chunk`，避免把错误文本当作正文渲染。
- 客户端断开连接即取消本次 run（服务端通过请求 context 感知），无需单独的取消接口。

---

### GET /api/agent/runs/{runId}/events

获取指定运行的执行事件列表。仅运行所属用户可读取；其他用户的运行返回 404。启用 PostgreSQL 后可跨实例读取，事件默认保留 168 小时（可用 `RUN_EVENT_RETENTION` 配置），过期后返回 404。审批恢复后的事件追加在同一运行记录中。

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
| stream | bool | 否 | 是否用 SSE 返回恢复进度，默认 `false`（保持一次性 JSON 响应） |

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

#### 流式模式 (stream=true)

恢复会执行被批准的工具并继续 ReAct 循环，耗时可能与一次聊天相当，且**可能再次中断**，因此可以改为流式返回。帧格式与聊天接口一致（`chunk` / `tool_call`），终止帧为 `done`：

```json
{
  "status": "completed",
  "answer": "操作已批准并执行。",
  "runId": "r_e5f6g7h8",
  "approved": true,
  "interrupt": null,
  "events": [],
  "streamed": true
}
```

- 再次中断时 `status` 为 `interrupted`，`interrupt` 携带新的审批请求（与聊天接口的处理方式相同）。
- 失败与取消同样通过 `status` 表达（`error` / `cancelled`），`answer` 承载原因。注意：**一旦开始流式输出，HTTP 状态码固定为 200**，错误只能从 `done` 帧读取；需要 HTTP 状态码的调用方应使用默认的非流式模式。
- 被批准的工具执行会产生 `tool_call` 进度帧（`phase` 为 `start` / `end`）。

#### 关于客户端断连

审批被 claim 之后，恢复执行会与本次连接**解耦**：断连不会取消已获授权的操作，服务端仍会执行到完成（上限仍为 2 分钟）。原因是 claim 是不可回退点——决定已持久化、工具可能已经执行，此时取消会消耗掉这次审批（无法重试）并把已发生的副作用记录为取消。

claim 之前断连仍会中止本次恢复，因为那时还没有任何不可逆操作。

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

配置了 MCP 服务器时，远端工具也出现在这个列表里，字段含义完全相同——`risk_level` 由本地策略统一给（默认 `medium`），`requires_approval` 只看 `MCP_REQUIRE_APPROVAL`，不看远端服务器自报的 annotations。`param_schema` 是远端声明的原始 JSON Schema，可能含嵌套对象与数组（`ParamsOneOf` 保真，不经过扁平格式）。

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

保留键（以 `__` 开头）属于框架内部状态，删除请求返回 400，不允许通过记忆接口改写。

---

### GET /api/memory/settings

读取当前用户的记忆开关。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{"enabled": true}
```

---

### PUT /api/memory/settings

设置当前用户的记忆开关。关闭后**既不从新对话中提取记忆，也不把已有记忆注入提示词**；已有条目保留，重新开启即恢复。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{"enabled": false}
```

**成功响应** (200)：

```json
{"enabled": false}
```

`enabled` 为必填；缺失返回 400。开关在记忆服务内部强制执行（`ExtractAndSave` 与 `RetrieveRelevant` 各自先查开关），不依赖调用点自觉，因此任何调用路径都无法绕过。该设置以保留键 `__settings` 存放在同一存储后端中，自动继承用户隔离与后端可替换；它不会出现在 `GET /api/memory` 的列表里。

---

## 8. 文档（RAG）

每个用户维护自己的文档语料。上传时按段落切分为片段（目标约 400 字、约 80 字重叠）并建立向量索引；提问时相关片段带编号注入提示词，回答据此引用来源。

文档与记忆存放在同一存储后端中（保留键 `__doc_*` / `__chunk_*`），因此自动获得内存 / 文件 / PostgreSQL 三种持久化与用户隔离，且不出现在记忆列表、检索与整合中。

### GET /api/documents

列出当前用户的文档，按创建时间倒序。**不含分块原文**（列表仅用于管理）。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
[
  {
    "id": "d_7f3a...",
    "name": "运维手册",
    "chunks": 3,
    "chars": 1240,
    "created_at": "2026-09-21T19:45:00+08:00"
  }
]
```

---

### POST /api/documents

上传一份文档。重复上传同名文档会新建一份，不会覆盖。

**请求头**：`Authorization: Bearer <sessionId>`

**请求体**：

```json
{
  "name": "运维手册",
  "content": "发布流程：先在预发环境跑完整的冒烟测试……\n\n回滚规则：一旦线上错误率超过百分之一……"
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| name | string | 是 | 文档名称，用于引用时标注来源 |
| content | string | 是 | 文档全文；上限 200000 字符，超出返回 413 |

**成功响应** (200)：返回该文档的元数据（结构同 `GET` 的单项）。

名称或内容为空返回 400。

---

### DELETE /api/documents/{id}

删除文档及其全部分块与向量条目。

**请求头**：`Authorization: Bearer <sessionId>`

**成功响应** (200)：

```json
{"status": "ok"}
```

文档不存在、或属于其他用户时返回 404——后者不返回 403 是有意的：响应不确认另一个用户的文档是否存在。

---

## 附录：事件类型

Agent 运行过程中记录的事件类型：

| 事件类型 | 说明 | 触发点 |
|----------|------|--------|
| `model_call_start` | LLM 推理开始 | 每次模型调用前（含重试） |
| `model_call_end` | LLM 推理结束 | 调用返回或失败，`metadata` 带 `attempt`、`failed`，服务商提供用量时带 `prompt_tokens` / `completion_tokens` |
| `tool_call_start` | 工具调用开始 | ACL 通过、真正执行工具前 |
| `tool_call_end` | 工具调用结束 | 工具执行完成，`metadata.result` 为截断后的结果摘要 |
| `acl_denied` | ACL 权限拒绝 | 工具或子 Agent 被 RBAC 拦截 |
| `hitl_interrupt` | HITL 中断 | 高危工具拦截、或节点级计划审批 |
| `hitl_resume` | HITL 恢复 | 审批通过后继续执行 |
| `summary_compress` | 上下文摘要压缩 | 超过阈值触发压缩 |
| `agent_start` | Agent 开始执行 | — |
| `agent_end` | Agent 执行结束 | — |
| `supervisor_route` | Supervisor 路由决策 | 派发到子 Agent |

事件同时通过两条路径可见：随 `done` 帧的 `events` 字段一次性返回（用于事件面板），以及执行过程中以 `tool_call` 帧实时下发（用于聊天区的进度行）。`tool_call_start` / `tool_call_end` / `acl_denied` / `hitl_interrupt` 只记录一次，两条路径共享同一份事件。
