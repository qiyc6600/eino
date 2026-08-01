# Agent Eino Demo

基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 构建的全功能 Agent 框架演示项目，涵盖登录权限、人机协同、多 Agent 调度、上下文管理与记忆系统五大模块。

---

## ✨ 核心特性

| 模块 | 能力 |
|------|------|
| 🔐 登录与权限 | Session 认证、RBAC 多角色、工具级 ACL 统一拦截、多用户数据隔离 |
| 🤝 人机协同 | 工具级中断（高危操作审批）、节点级中断（执行计划审批）、Checkpoint 状态恢复 |
| 🧠 多 Agent | Supervisor 三子 Agent 路由、ReAct 循环（MaxStep=20）、6 个示例工具 |
| 📐 上下文管理 | 按消息数/Token 数裁剪、LLM 摘要压缩、Tool 配对保护 |
| 💾 记忆系统 | 短期记忆 CheckpointStore + 长期记忆 MemoryStore、跨会话偏好提取、向量检索 |

---

## 🚀 快速开始

```bash
# 克隆项目
cd agent-eino-demo

# 方式一：Mock 模式（无需 API Key，开箱即用）
go run ./cmd/server

# 方式二：接入真实 LLM
MODEL_PROVIDER=openai \
OPENAI_API_KEY=sk-xxx \
OPENAI_MODEL=gpt-4o \
go run ./cmd/server

# 浏览器访问
open http://localhost:8080
```

### 默认账号

| 用户名 | 密码 | 角色 | 可用工具 |
|--------|------|------|----------|
| `admin` | `admin123` | admin | calculator, weather, grep, query_order, delete_order, send_email |
| `visitor` | `visitor123` | visitor | calculator, weather, query_order |

> visitor 调用 admin 工具时被 ACL 拦截，拒绝结果回灌 LLM 使其重新规划。

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
│  │  节点级中断 → 执行计划审批 (意图触发)    │  │
│  │  工具级中断 → 高危工具审批 (自动触发)    │  │
│  └─────────────────────────────────────────┘  │
└──────────────────────────────────────────────┘
    │
    ▼
ChatModel (Mock / OpenAI / DeepSeek / Ark / ...)
    │
    ▼
┌──────────────────────────────────────────────┐
│  上下文管理                    记忆系统       │
│  TrimByCount / TrimByToken    CheckpointStore│
│  Summarizer (LLM 摘要)        MemoryStore    │
│  GuardToolPairs               VectorStore    │
└──────────────────────────────────────────────┘
```

**执行流程**：用户消息 → 上下文压缩 → LLM 推理 → 中断门检查 → 工具执行 → 结果回灌 → 继续推理 → 最终回复

---

## 🤝 人机协同（HITL）

本项目实现了两种中断类型，均采用**异步审批模式**（中断→返回→API 恢复）：

### 工具级中断

高危工具（`delete_order`、`send_email`）执行前自动触发中断，等待人工审批。

```
Agent 调用 delete_order → 中断 → 返回审批卡片 → 人工批准/拒绝 → Resume 继续执行
```

### 节点级中断

用户消息包含"确认后再执行"等意图时，LLM 生成执行计划后暂停，展示计划供人工审批。

```
用户: "查询北京天气，确认后再执行"
→ LLM 决定调用 weather 工具 → 中断 → 展示执行计划 → 人工批准 → 执行工具 → 返回结果
→ 人工拒绝 → LLM 收到拒绝反馈 → 重新规划
```

### 恢复语义

| 中断类型 | 恢复方式 | 说明 |
|----------|----------|------|
| 工具级 | 从 Checkpoint 加载完整状态 → 执行工具 → 继续 ReAct | 工具幂等性通过 `runID:toolCallID` 保证 |
| 节点级 | 从 Checkpoint 加载完整状态 → 保留/清空 PendingToolCalls → 继续 ReAct | 拒绝时 LLM 重新规划 |

Checkpoint 保存完整的 `SteppedRunState`（对话历史 + 步骤数 + 待执行工具），恢复时优先从 Checkpoint 还原，降级为线程重建。

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

## 📐 上下文管理

长对话自动裁剪，确保不超出 LLM 窗口：

| 策略 | 实现 | 特点 |
|------|------|------|
| 按消息数裁剪 | `TrimByCount` | system 消息永久保留 |
| 按 Token 数裁剪 | `TrimByToken` | 基于 `SimpleTokenCounter` 估算 |
| Tool 配对保护 | `GuardToolPairs` | tool_call 与 tool_result 不拆散 |
| LLM 摘要压缩 | `Summarizer.Compress` | 超阈值触发 LLM 摘要，规则提取降级兜底 |

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
| DELETE | `/api/memory/{key}` | 删除记忆 |
| GET | `/api/models` | 可用模型列表 |
| POST | `/api/models/switch` | 切换模型 |

---

## 🎯 演示场景

前端内置 **5 个一键演示按钮**，覆盖全部核心功能：

| 按钮 | 场景 | 自动操作 |
|------|------|----------|
| 🚫 Visitor 越权 | ACL 拦截 | visitor 登录 → 删除订单 → ACL 拒绝 |
| ✅ Admin 审批 | HITL 流程 | admin 登录 → 删除订单 → 触发审批 → 批准/拒绝 |
| 🔀 不同工具路径 | 多 Agent 路由 | 依次调用不同工具展示 Supervisor 分发 |
| ✂️ 长对话裁剪 | 上下文管理 | 连续发送 10 条消息触发摘要压缩 |
| 🧠 记忆管理 | 跨会话偏好 | 表达偏好 → 验证记忆保存 → 换线程读取 |

**节点级中断演示**：在聊天框输入"查询北京天气，确认后再执行"，触发执行计划审批流程。

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
| 多用户隔离非自动强制 | 隔离依赖下游 handler 主动使用 `AuthContext.UserID`，auth 层不自动拦截 |
| 节点级中断需意图触发 | 非默认启用，用户消息需包含"确认后再执行"等关键词 |
| HITL 为异步审批模式 | 中断后 run 结束，通过独立 API 恢复，非"挂起等待"语义 |
| grep 使用示例数据 | 搜索日志为硬编码 mock，无真实文件系统访问 |
| 密码无盐 SHA-256 | 演示项目简化，生产应使用 bcrypt/argon2 |

---

## 📄 License

本项目为演示项目，仅用于学习和参考。
