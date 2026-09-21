# Agent 框架对比分析：LangGraph vs LangGraph4j vs Eino

> 本文档基于实训任务要求，对三种主流 Agent 框架进行系统性对比分析，明确各框架的核心能力、优势与风险，并阐述本项目选择 Eino 的技术决策理由。

---

## 1. 框架概览

### 1.1 LangGraph

| 属性 | 说明 |
|------|------|
| **语言** | Python |
| **维护方** | LangChain（OSS），核心团队约 10 人 |
| **最新版本** | 1.2.9（截至 2026-07，生产成熟） |
| **一句话定位** | 基于 Pregel/Beam 启发的有状态 Agent 编排运行时 |
| **核心抽象** | `StateGraph`（状态图）、`Node`（节点函数）、`Edge`（条件/固定边） |
| **GitHub Stars** | ~12k+ |
| **生产案例** | Uber、JP Morgan、Klarna 等 |
| **PyPI 周下载** | 200k+ |

**核心设计理念**：LangGraph 将 Agent 编排建模为**有状态图**。开发者定义节点（Python 函数）和边（条件跳转），框架按照 Pregel 计算模型调度执行。每一步执行后，状态自动通过 `Checkpoint` 持久化，支持中断恢复。

```python
from langgraph.graph import StateGraph, START, END

graph = StateGraph(AgentState)
graph.add_node("reason", reason_node)
graph.add_node("act", act_node)
graph.add_edge(START, "reason")
graph.add_conditional_edges("reason", should_continue, {"continue": "act", "end": END})
graph.add_edge("act", "reason")
app = graph.compile(checkpointer=MemorySaver(), interrupt_before=["act"])
```

### 1.2 LangGraph4j

| 属性 | 说明 |
|------|------|
| **语言** | Java |
| **维护方** | bsorrentino（社区单人维护） |
| **最新版本** | 1.8.20（截至 2026-07，未到稳定语义） |
| **一句话定位** | LangGraph 的 Java 移植，补 LangChain4j 编排运行时短板 |
| **核心抽象** | `StateGraph`、`Node`、`Edge`（与 LangGraph Python 对齐） |
| **GitHub Stars** | ~800+ |
| **生产案例** | 较少，主要为个人项目和实验性应用 |
| **Maven 依赖** | `org.bsc.langgraph4j:langgraph4j-core-jdk8` |

**核心设计理念**：LangGraph4j 是 LangGraph Python 版本的 Java 移植，力图在 Java 生态中复现 LangGraph 的状态图模型。它提供了 `interrupt`/`resume` 原语级支持，但与 LangChain4j 的集成仍处于早期阶段。

```java
var graph = new StateGraph<>(AgentState::new)
    .addNode("reason", reasonNode)
    .addNode("act", actNode)
    .addEdge(START, "reason")
    .addConditionalEdges("reason", shouldContinue,
        Map.of("continue", "act", "end", END))
    .addEdge("act", "reason");
var app = graph.compile(CompileConfig.builder()
    .checkpointSaver(new MemorySaver())
    .build());
```

### 1.3 Eino

| 属性 | 说明 |
|------|------|
| **语言** | Go |
| **维护方** | 字节跳动 / CloudWeGo 团队 |
| **最新版本** | v0.9.12（截至 2026-07，未到 1.0） |
| **一句话定位** | 强类型组件抽象 + Graph/Chain/Workflow 编排 + ADK 智能体 |
| **核心抽象** | `compose.Graph`、`compose.Chain`、`compose.Workflow`、`react.Agent`、`adk` |
| **GitHub Stars** | ~2k+ |
| **生产案例** | 字节跳动内部大规模使用 |
| **Go Module** | `github.com/cloudwego/eino` |

**核心设计理念**：Eino 采用 Go 的接口组合风格，将 Agent 编排拆解为**强类型组件**（ChatModel、Tool、Retriever 等）和**编排模式**（Graph、Chain、Workflow）。ADK（Agent Development Kit）层提供 ReAct、AgentAsTool、interrupt/resume 等高层能力，框架层保持可替换性。

```go
agent, _ := react.NewAgent(ctx, &react.AgentConfig{
    ToolCallingModel: chatModel,
    ToolsConfig: compose.ToolsNodeConfig{
        Tools: []tool.BaseTool{calculatorTool, weatherTool},
    },
    MaxStep: 20,
})
result, _ := agent.Generate(ctx, messages)
```

---

## 2. 核心能力对比

### 2.1 编排抽象

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **核心抽象** | StateGraph + Node + Edge | StateGraph + Node + Edge | Graph + Chain + Workflow |
| **条件路由** | ✅ `add_conditional_edges` | ✅ `addConditionalEdges` | ✅ `compose.Graph.Branch` |
| **循环支持** | ✅ 原生（边回指节点） | ✅ 原生 | ✅ 原生（ReAct 内置循环） |
| **编译优化** | ✅ 编译后不可变 | ✅ 编译后不可变 | ✅ 编译后不可变 |
| **流式支持** | ✅ `stream()` / `astream()` | ✅ 流式 | ✅ `StreamReader[T]` |
| **子图组合** | ✅ 嵌套 StateGraph | ⚠️ 部分支持 | ✅ 子 Graph / Chain 嵌套 |
| **类型安全** | ❌ Python 动态类型 | ✅ Java 泛型 | ✅ Go 接口 + 泛型 |

**分析**：三者均支持图编排和条件路由。LangGraph 的状态图模型最成熟，但 Python 的动态类型在大型项目中容易引入运行时错误。Eino 的 Go 接口系统天然适合构建可替换后端（如 `SessionStore`、`CheckpointStore`），编译期即可捕获类型不匹配。LangGraph4j 虽然有 Java 泛型，但社区规模和 API 稳定性是短板。

### 2.2 ReAct 与 Agent

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **ReAct 原生** | ✅ `create_react_agent` | ❌ 需自建 | ✅ `react.NewAgent` |
| **MaxIterations** | ✅ `recursion_limit` | ⚠️ 需手动实现 | ✅ `MaxStep` |
| **Tool Calling** | ✅ `ToolNode` + `tool` 装饰器 | ❌ 需适配 LangChain4j | ✅ `ToolsNode` + `ToolCallMiddleware` |
| **AgentAsTool** | ✅ 将 Agent 包装为工具 | ❌ 无原生支持 | ✅ ADK `AgentTool` |
| **自定义 Agent** | ✅ 自由组合 StateGraph | ✅ 自由组合 StateGraph | ✅ Graph/Chain/Workflow 自由组合 |
| **流式 Agent** | ✅ `astream_events` | ⚠️ 有限 | ✅ `agent.Stream()` |

**分析**：LangGraph 和 Eino 都提供了 ReAct Agent 的原生实现，开发者可以直接使用。LangGraph4j 缺少 ReAct 原生支持，需要开发者手动构建 Reason→Act→Observe 循环。Eino 的 `react.NewAgent` 封装了完整的 ReAct 循环，并通过 `MaxStep` 提供兜底机制，与本项目需求高度匹配。

### 2.3 HITL 与 Checkpoint（人机协同）

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **interrupt/resume** | ✅ 原生 `interrupt()` | ✅ 原生 `interrupt()` | ✅ 原生 interrupt/resume |
| **interrupt_before/after** | ✅ 编译时指定 | ✅ 编译时指定 | ✅ 节点级中断配置 |
| **CheckpointStore** | ✅ `BaseCheckpointSaver` 接口 | ✅ `CheckpointSaver` 接口 | ✅ `CheckpointStore` 接口 |
| **内存版实现** | ✅ `MemorySaver` | ✅ `MemorySaver` | ✅ 需自建（本项目已实现） |
| **持久化后端** | ✅ SQLite / Postgres / Redis | ⚠️ 仅内存版 | ✅ 接口可替换 |
| **节点级中断** | ✅ `interrupt_before/after` | ✅ `interrupt_before/after` | ✅ 节点中断配置 |
| **工具级中断** | ✅ 在工具前 interrupt | ✅ 在工具前 interrupt | ✅ ToolCallMiddleware 中触发 |
| **恢复语义** | 从中断点继续 | 从中断点继续 | 从中断点继续 |
| **状态序列化** | ✅ 自动 JSON 序列化 | ✅ 自动序列化 | ✅ 需手动序列化 State |

**分析**：三者均支持 interrupt/resume 原语。LangGraph 的 Checkpoint 系统最成熟，内置多种持久化后端。Eino 的 interrupt/resume 通过 ADK 层提供，与 Checkpoint 紧密集成。本项目在 Eino 基础上自建了 `InMemoryCheckpointStore`，并实现了工具级中断（通过 `ToolCallMiddleware`）和节点级中断（通过 `NodeInterruptGate`），满足任务要求。

### 2.4 多 Agent 协作

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **Supervisor 模式** | ✅ 原生 `create_react_agent` + 路由 | ❌ 需自建 | ✅ ADK `AgentCollaboration` |
| **Swarm 模式** | ✅ 原生 `create_swarm` | ❌ 无 | ❌ 无 |
| **AgentAsTool** | ✅ 将子 Agent 包装为工具 | ❌ 无原生支持 | ✅ `adk.NewAgentTool` |
| **Fresh Context** | ✅ 子 Agent 独立消息 | ⚠️ 需手动管理 | ✅ 子 Agent 独立消息 |
| **并行子 Agent** | ✅ `Send` API | ❌ 无 | ⚠️ 需手动 goroutine |
| **子 Agent 结果汇总** | ✅ supervisor 自动汇总 | ❌ 需自建 | ✅ supervisor 自动汇总 |

**分析**：LangGraph 的多 Agent 能力最丰富，支持 Supervisor 和 Swarm 两种模式。Eino 通过 ADK 的 `AgentCollaboration` 和 `AgentTool` 提供了 Supervisor 模式的完整支持。本项目使用 `AgentAsTool` 模式将 3 个子 Agent（math_agent、search_agent、general_agent）包装为工具，由 supervisor 中心路由，满足任务"supervisor 中心路由分派"的要求；配置了 MCP 服务器时再追加一个 `mcp_agent` 承载外部工具（未发现工具则不创建）。

### 2.5 工具系统

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **工具注册** | ✅ `@tool` 装饰器 / `StructuredTool` | ⚠️ 需实现 LangChain4j `Tool` 接口 | ✅ `tool.InvokableTool` + `schema.ToolInfo` |
| **工具中间件** | ❌ 需自建拦截层 | ❌ 需自建拦截层 | ✅ `ToolCallMiddleware` 原生支持 |
| **JSON Schema** | ✅ 自动从函数签名推断 | ⚠️ 需手动定义 | ✅ `ParamsOneOf` + `ParameterInfo` |
| **工具元数据** | 名称 + 描述 + 参数 | 名称 + 描述 + 参数 | 名称 + 描述 + 参数 + 风险等级（可扩展） |
| **批量工具调用** | ✅ `ToolNode` 批量执行 | ⚠️ 有限 | ✅ `ToolsNode` 批量执行 |
| **工具错误处理** | ✅ 结构化错误返回 | ⚠️ 需手动处理 | ✅ 结构化 `ToolResult` |

**分析**：Eino 的 `ToolCallMiddleware` 是三框架中**唯一原生支持工具调用中间件**的框架。这正是本项目实现"工具级 ACL 框架统一拦截"的核心依赖——在工具调用前通过中间件统一检查权限，而非在每个工具内部手写校验。LangGraph 和 LangGraph4j 均缺少此能力，需要开发者自行实现拦截层。

### 2.6 上下文管理

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **消息裁剪** | ❌ 需自建 | ❌ 需自建 | ❌ 需自建 |
| **Token 计数** | ✅ `tiktoken` / `langchain.callbacks` | ❌ 需自建 | ❌ 需自建 |
| **摘要压缩** | ✅ `ConversationSummaryMemory` | ❌ 需自建 | ❌ 需自建（本项目已实现 LLM 摘要） |
| **MessageModifier** | ✅ 运行时修改消息 | ❌ 无 | ✅ 运行时修改消息 |
| **MessageRewriter** | ✅ 持久化修改历史 | ❌ 无 | ✅ 持久化修改历史 |
| **tool 配对保护** | ❌ 需自建 | ❌ 需自建 | ❌ 需自建（本项目已实现 `GuardToolPairs`） |

**分析**：三个框架均未内置完整的上下文管理能力。LangGraph 借助 LangChain 的 Memory 类提供了部分支持（如 `ConversationSummaryMemory`），但与 StateGraph 的集成需要手动处理。本项目在 Eino 基础上自建了完整的上下文管理系统：按消息数裁剪（`TrimByCount`）、按 token 裁剪（`TrimByToken`）、tool 配对保护（`GuardToolPairs`）、LLM 摘要压缩（`Summarizer`），满足任务全部要求。

### 2.7 记忆系统

| 能力 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **短期记忆（Checkpoint）** | ✅ `BaseCheckpointSaver` | ✅ `CheckpointSaver` | ✅ 通过 ADK Checkpoint |
| **长期记忆（Store）** | ✅ `BaseStore` + 多后端 | ❌ 需自建 | ✅ ADK Session/Memory |
| **跨会话记忆** | ✅ `InMemoryStore` / 持久化 | ❌ 需自建 | ✅ 需自建（本项目已实现 `MemoryStore`） |
| **命名空间隔离** | ✅ 按 tuple 隔离 | ❌ 需自建 | ✅ 按 userId 隔离 |
| **记忆搜索** | ✅ 支持语义搜索 | ❌ 无 | ❌ 需自建 |

**分析**：LangGraph 的记忆系统最完善，内置了短期/长期记忆的分层抽象和多种持久化后端。Eino 通过 ADK 提供了基础的记忆能力，但长期记忆的跨会话存取需要开发者自建。本项目实现了 `MemoryStore`（长期记忆）和 `CheckpointStore`（短期记忆）两套独立接口，支持按 userId 隔离和跨会话偏好读取。

---

## 3. 成熟度与风险评估

### 3.1 版本与稳定性

| 维度 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **版本** | 1.2.9 | 1.8.20 | 0.9.12 |
| **API 稳定性** | ✅ 生产级，语义版本控制 | ⚠️ 未稳定，API 可能变动 | ⚠️ 未到 1.0，API 可能变动 |
| **破环性变更频率** | 低（主版本 1.x） | 中（API 仍在调整） | 中（v0.x 阶段） |
| **向后兼容承诺** | ✅ 语义版本保证 | ❌ 无明确承诺 | ❌ 无明确承诺 |
| **发布节奏** | 每 1-2 周 | 不定期 | 每 1-2 周 |

### 3.2 社区与生态

| 维度 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **GitHub Stars** | ~12k+ | ~800+ | ~2k+ |
| **核心贡献者** | ~10 人（LangChain 团队） | 1 人（bsorrentino） | ~5 人（CloudWeGo 团队） |
| **文档质量** | ✅ 完善，有教程和示例 | ⚠️ 基础，示例较少 | ⚠️ 持续完善中 |
| **第三方集成** | ✅ 丰富（LangChain 生态） | ⚠️ 有限（LangChain4j/Spring AI） | ⚠️ 有限（CloudWeGo 生态） |
| **问题响应** | ✅ 活跃（1-3 天） | ⚠️ 不确定（单人维护） | ✅ 活跃（CloudWeGo 团队） |
| **生产案例** | Uber / JP Morgan / Klarna | 较少 | 字节跳动内部大规模 |

### 3.3 风险矩阵

| 风险 | LangGraph | LangGraph4j | Eino |
|------|-----------|-------------|------|
| **维护者流失** | 低（LangChain 团队） | 🔴 高（单人维护） | 中（字节团队） |
| **API 破坏性变更** | 低（1.x 稳定） | 🔴 高（未稳定） | 🟡 中（0.x 阶段） |
| **生态孤立** | 低（LangChain 生态） | 🔴 高（Java 生态边缘） | 🟡 中（Go 生态新兴） |
| **安全漏洞响应** | ✅ 快速 | 🔴 不确定 | ✅ 快速 |
| **文档不足** | 低 | 🔴 高 | 🟡 中 |

---

## 4. 本项目关键需求与框架匹配度

### 4.1 逐项匹配分析

| 任务要求 | LangGraph | LangGraph4j | Eino | 说明 |
|----------|-----------|-------------|------|------|
| **语言统一** | ❌ Python | ❌ Java | ✅ Go | 项目要求 Java/Python/Go 三选一，Eino 是唯一 Go 原生框架 |
| **SessionStore 接口** | ✅ 可实现 | ✅ 可实现 | ✅ 可实现 | 三者均可实现，但 Go 的接口系统更适合定义可替换后端 |
| **工具级 ACL 统一拦截** | ⚠️ 需自建拦截层 | ⚠️ 需自建拦截层 | ✅ `ToolCallMiddleware` 原生支持 | **Eino 是唯一原生支持工具中间件的框架** |
| **ReAct 循环** | ✅ `create_react_agent` | ❌ 需自建 | ✅ `react.NewAgent` | LangGraph4j 缺少 ReAct 原生支持 |
| **MaxIterations 兜底** | ✅ `recursion_limit` | ⚠️ 需手动实现 | ✅ `MaxStep` | LangGraph4j 需要手动实现循环计数 |
| **supervisor 多 Agent** | ✅ 原生 | ❌ 需自建 | ✅ ADK AgentAsTool | LangGraph4j 无原生多 Agent 支持 |
| **interrupt/resume** | ✅ 原生 | ✅ 原生 | ✅ 原生 | 三者均支持 |
| **CheckpointStore 接口** | ✅ 原生 | ✅ 原生 | ✅ 需自建 | Eino 需要（且已）自建 CheckpointStore |
| **上下文裁剪** | ❌ 需自建 | ❌ 需自建 | ❌ 需自建 | 三者均需自建 |
| **LLM 摘要压缩** | ✅ `ConversationSummaryMemory` | ❌ 需自建 | ❌ 需自建（已实现） | LangGraph 有 LangChain 生态支持 |
| **长期记忆** | ✅ `BaseStore` | ❌ 需自建 | ✅ 需自建（已实现） | Eino 和 LangGraph4j 需自建 |
| **多用户隔离** | ✅ 可实现 | ✅ 可实现 | ✅ 可实现 | 三者均需在业务层实现 |
| **Web 动态页面** | ✅ 可实现 | ✅ 可实现 | ✅ 可实现 | 与框架选择无关 |

### 4.2 匹配度评分

| 框架 | 匹配度 | 关键加分项 | 关键扣分项 |
|------|--------|-----------|-----------|
| **LangGraph** | ⭐⭐⭐⭐ | ReAct 原生、生态最成熟、Checkpoint 最完善 | 语言不统一（Python）、工具中间件需自建 |
| **LangGraph4j** | ⭐⭐ | 与 Java 生态友好 | 单人维护、无 ReAct 原生、无多 Agent、语言不统一 |
| **Eino** | ⭐⭐⭐⭐⭐ | 语言统一、工具中间件原生、ReAct 原生、AgentAsTool | 未到 1.0、部分能力需自建 |

---

## 5. 本项目技术决策

### 5.1 选择 Eino 的理由

#### 理由 1：语言统一（决策性因素）

项目要求"在 Java、Python、Go 中仅选一种语言，自研本子系统所有内容"。Eino 是唯一 Go 原生的 Agent 框架，选择 Eino 可以确保：

- Agent 框架、权限、记忆、上下文、HITL 等核心逻辑全部在 Go 后端实现
- 不引入 Python/Java 运行时依赖
- 前后端分离清晰，前端只负责展示

#### 理由 2：工具中间件原生支持（核心竞争力）

任务要求"工具调用前的权限校验必须由框架统一拦截，禁止在每个工具内部手写校验"。Eino 的 `ToolCallMiddleware` 是三框架中**唯一原生支持工具调用中间件**的框架：

```go
// Eino 的 ToolCallMiddleware — ACL 统一拦截的理想落点
type ToolsNodeConfig struct {
    Tools              []tool.BaseTool
    ToolCallMiddlewares []ToolCallMiddleware // ← 原生中间件链
}
```

LangGraph 和 LangGraph4j 均缺少此能力，需要开发者自行实现拦截层，且容易遗漏。

#### 理由 3：ReAct + AgentAsTool 原生支持

任务要求"实现统一的 ReAct 循环引擎"和"supervisor 中心路由模式"。Eino 的 `react.NewAgent` 和 `adk.NewAgentTool` 提供了完整的开箱即用支持：

```go
// ReAct Agent — 一行配置，含 MaxStep 兜底
agent, _ := react.NewAgent(ctx, &react.AgentConfig{
    ToolCallingModel: chatModel,
    ToolsConfig:      compose.ToolsNodeConfig{Tools: einoTools},
    MaxStep:          20, // ← 内置兜底，杜绝死循环
})

// AgentAsTool — 将子 Agent 包装为 supervisor 可调用的工具
agentTool := adk.NewAgentTool(subAgent)
```

#### 理由 4：Go 接口系统天然适合可替换后端

任务要求交付 `SessionStore`、`CheckpointStore`、`MemoryStore` 等接口。Go 的接口系统使得定义可替换后端极为自然：

```go
type SessionStore interface {
    Create(ctx context.Context, session Session) error
    Get(ctx context.Context, sessionID string) (Session, bool, error)
    Delete(ctx context.Context, sessionID string) error
    ListByUser(ctx context.Context, userID string) ([]Session, error)
}
```

内存版实现和持久化版实现可平滑切换，业务代码无需改动。

### 5.2 不选 LangGraph 的理由

| 因素 | 说明 |
|------|------|
| **语言不统一** | LangGraph 是 Python 框架，与项目 Go 语言选择冲突。引入 Python 违反"仅选一门语言"的约束 |
| **工具中间件需自建** | LangGraph 缺少原生工具调用中间件，需要自行在 `ToolNode` 前后包装拦截逻辑 |
| **运行时依赖** | 引入 Python 运行时需要额外部署，增加运维复杂度 |

### 5.3 不选 LangGraph4j 的理由

| 因素 | 说明 |
|------|------|
| **语言不统一** | LangGraph4j 是 Java 框架，与项目 Go 语言选择冲突 |
| **社区规模过小** | 单人维护，生产风险极高，无法保证长期维护和问题响应 |
| **API 未稳定** | 版本 1.8.20 仍未达到稳定语义，API 可能存在破坏性变更 |
| **缺少 ReAct 原生支持** | 需要手动构建 Reason→Act→Observe 循环，开发成本高 |
| **无多 Agent 支持** | 缺少 AgentAsTool 和 Supervisor 模式，需要大量自建 |
| **与 LangChain4j 集成不成熟** | 虽然定位为"补 LangChain4j 编排运行时短板"，但实际集成仍不成熟 |

### 5.4 风险应对

| 风险 | 应对措施 |
|------|----------|
| **Eino API 变动** | ① 锁定 `v0.9.12` 版本；② 在 `internal/agent` 和 `internal/tools` 做适配层；③ 业务代码不直接依赖 Eino 细节 |
| **Eino 文档不足** | ① 参考 CloudWeGo 官方文档；② 阅读 Eino 源码理解内部机制；③ 在项目 `docs/` 中记录设计决策 |
| **Eino 功能缺失** | ① 上下文管理、Token 计数、记忆系统等自建实现；② 自建实现通过接口抽象，与 Eino 解耦 |
| **Eino 不可用** | ① MockChatModel 保证演示不依赖外部模型；② 所有验收场景都能在 mock 模式运行 |

---

## 6. 总结

### 6.1 框架选择决策树

```
项目语言选择？
├── Python → LangGraph（首选）
├── Java → LangGraph4j（唯一选择，但风险高）
└── Go → Eino（唯一选择，且匹配度高）
    ├── ReAct 原生 ✅
    ├── AgentAsTool 原生 ✅
    ├── ToolCallMiddleware 原生 ✅ ← 关键优势
    ├── interrupt/resume 原生 ✅
    └── 部分能力需自建 ⚠️（已实现）
```

### 6.2 最终结论

| 框架 | 推荐度 | 适用场景 |
|------|--------|----------|
| **LangGraph** | ⭐⭐⭐⭐⭐ | Python 项目首选，生态最成熟，生产案例最多 |
| **LangGraph4j** | ⭐⭐ | Java 生态的实验性选择，不建议用于生产 |
| **Eino** | ⭐⭐⭐⭐ | Go 项目首选，工具中间件是独特优势，适合需要 ACL 统一拦截的场景 |

**本项目选择 Eino**，因为：① 语言统一（Go）；② `ToolCallMiddleware` 原生支持 ACL 统一拦截；③ ReAct + AgentAsTool 原生支持；④ Go 接口系统适合构建可替换后端。风险通过版本锁定、适配层和 MockChatModel 予以控制。

---

## 附录：参考资料

- [Eino Overview](https://www.cloudwego.io/docs/eino/overview/)
- [Eino ReAct Agent Manual](https://www.cloudwego.io/docs/eino/core_modules/flow_integration_components/react_agent_manual/)
- [Eino ToolsNode & Tool Guide](https://www.cloudwego.io/docs/eino/core_modules/components/tools_node_guide/)
- [Eino Interrupt & CheckPoint](https://www.cloudwego.io/docs/eino/core_modules/chain_and_graph_orchestration/checkpoint_interrupt/)
- [Eino Agent Collaboration](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_collaboration/)
- [Eino Memory and Session](https://www.cloudwego.io/docs/eino/quick_start/chapter_03_memory_and_session/)
- [LangGraph Documentation](https://langchain-ai.github.io/langgraph/)
- [LangGraph4j GitHub](https://github.com/bsorrentino/langgraph4j)
- [CloudWeGo Eino GitHub](https://github.com/cloudwego/eino)
