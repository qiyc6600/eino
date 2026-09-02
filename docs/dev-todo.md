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
| 13 | users map 并发保护 | 模块 01 | ✅ | `auth.Service` 增加 `usersMu sync.RWMutex`，并发登录/用户管理/角色更新经 `-race` 验证 |
| 14 | 节点级中断显式触发 | 模块 02 | ✅ | Chat 请求 `confirmBeforeExecute` 标志（前端开关）替代关键词检测，RunStep 按参数传递无共享状态污染 |

---

## 已知限制（非阻塞）

| # | 限制 | 涉及模块 | 说明 |
|---|------|----------|------|
| 1 | HITL 为异步审批模式 | 模块 02 | 中断后 run 结束，通过独立 API 调用恢复，非"挂起等待"语义 |
| 2 | grep 使用硬编码 mock 数据 | 模块 03 | 搜索日志是示例场景，数据完全合成 |
| 3 | 密码使用无盐 SHA-256 | 模块 01 | 演示项目可接受的安全简化 |

> 已修复：多用户隔离非自动强制（原限制 #1）——已升级为框架强制（类型化 ToolIdentity + 线程/run/审批属主校验 + 存储层 CheckUserScope），详见 docs/design.md 5.3 节。
> 已修复：节点级中断需意图触发（原限制 #2）——改为请求显式 `confirmBeforeExecute` 标志（前端开关/API 字段），消息文本不再参与触发判断。

---

## 历史记录

- 本文档原为 1960 行的详细开发计划文档，已在所有缺失项修复后精简为当前状态追踪格式。
- 原始开发计划的实现方案已全部落地，详见上方"已完成项"列表。
