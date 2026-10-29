# 采集 Agent 验收记录

日期：2026-10-29
环境：Windows / Go 1.25.0 / Python 3.12 / 内存存储（E2E）

## 一、自动化测试

### 服务端

```powershell
go test ./internal/study ./internal/httpapi ./internal/config -count=1
```

结果：`ok`（study 12s、httpapi 16s、config 0.4s）。

覆盖：

| 用例 | 断言 |
|---|---|
| `TestAgentIdentityAndKeyAccess` | 密钥前缀 `wzag_`、权限仅 `task.claim`+`ingest`、停用即拒绝、slug 冲突 |
| `TestCollectionTaskRequiresIngestSource` | 来源必须存在、默认配额/预算、超出策略配额被拒 |
| `TestClaimIsExclusiveAcrossAgents` | 8 个并发领取仅 1 个成功 |
| `TestExpiredLeaseIsReclaimedWithAttemptIncrement` | 租约过期回收、attempts 递增、超过上限转 `expired` |
| `TestHeartbeatAllowsGraceWindow` | 租约到期后的宽限窗口内心跳与入库仍可继续，超出后拒绝 |
| `TestAgentTaskItemsQuotaDomainsAndIdempotency` | 域名白名单、配额、`external_id` 幂等更新、`integration=agent` 与 `task_id` 回执 |
| `TestCompleteIsIdempotentAndLeaseBlocksForeignWrites` | 跨采集端写入被拒、终态幂等且不覆盖首次结果 |
| `TestCancelAndRetryLifecycle` | pending 直接取消、运行中取消经心跳下发、release、retry 重置 |
| `TestAgentIdentityIsWorkspaceScoped` | 任务/采集端不跨工作区泄漏；密钥查询全局有效 |
| `TestAgentProtocolRoundTrip`（httpapi） | 鉴权、claim、heartbeat、events、items、complete、责任链隔离 |
| `TestAgentEndpointsAreHiddenWhenDisabled` | 功能关闭时 404 |
| `TestAgentEnabledByDefault`（config） | 未设置 `AGENT_ENABLED` 时默认开启；显式 `false` 才关闭 |
| `TestAgentCancelSignalReachesAgent` | 取消信号与取消后拒绝入库 |
| `TestOpenAPICoversAllBusinessRoutes` | 19 个新操作全部有 OpenAPI 文档 |

### 管理后台任务板块

```powershell
go test ./internal/httpapi -run 'TestWorkspaceTaskBoard' -count=1
```

结果：`ok`。

| 用例 | 断言 |
|---|---|
| `TestWorkspaceTaskBoardAssetsAreEmbedded` | 页面包含任务导航、`tasks.css` 与 `tasks.js`；脚本先于 `app.js` 加载；`app.js` 注册 `tasks:tasksView.render`；`tasks.js` 覆盖派发、取消、重试及四个数据接口 |
| `TestWorkspaceTaskBoardIsAdministratorOnly` | 任务导航位于管理员导航组，普通用户不可见 |
| `TestSourceDialogUsesAgentIntegration` | 录入端来源类型显示「Agent 录入」，接入方式默认 agent |
| `TestManualSourceAcceptsAgentIntegration`（study） | 服务端接受 `integration=agent`，未知值仍拒绝 |

### 采集端

```powershell
cd agent
go test ./... -count=1
```

结果：`ok`（collect / loop / provider / serverapi）。

| 用例 | 断言 |
|---|---|
| `TestClaimParsesTaskAndAuthenticates` | Bearer 头、路径、任务字段解析 |
| `TestClaimReturnsNilOnNoContent` | 204 表示无任务 |
| `TestErrorContractIsParsed` | `{"error":{code,message}}` → 409 `lease_expired` |
| `TestCompleteSendsStatsAndAcceptsNoContent` | 终态与统计序列化、204 |
| `TestChatSendsToolsAndParsesToolCalls` | tools/tool_choice、tool_calls 与 usage 解析 |
| `TestToolMessageReplayShape` | assistant `tool_calls` 与 `tool_call_id` 回放格式 |
| `TestAllowedIPRejectsNonPublicTargets` | 回环/私网/链路本地/多播/IPv4 映射 IPv6/ULA 全部拒绝 |
| `TestValidateTargetRejectsCredentialsAndSchemes` | 非 http(s)、含凭据的 URL 拒绝 |
| `TestFetcherExtractsReadablePage` | 标题、描述、正文提取，脚本内容剔除 |
| `TestFetcherBlocksLoopbackByDefault` | 默认禁止访问回环 |
| `TestFetcherTruncatesLargeResponses` | `max_bytes` 截断标记 |
| `TestLoopSucceedsAfterToolRound` | 两轮完成、统计、事件齐全 |
| `TestLoopStopsAtRoundLimit` / `TestLoopStopsAtToolCallLimit` | 预算终止 |
| `TestLoopStopsRepeatingIdenticalCalls` | 连续重复调用终止 |
| `TestLoopHonorsExplicitFinish` | `finish_task` 立即结束 |
| `TestLoopStopsOnCancellation` | 取消返回 cancelled |

## 二、本地默认启用验证

服务端不再要求设置 `AGENT_ENABLED`。重启本地服务后：

```powershell
Invoke-WebRequest http://127.0.0.1:8091/api/v1/health/live   # 200
Invoke-WebRequest http://127.0.0.1:8091/api/v1/agent/status  # 401（路由已注册，非 404）
Invoke-WebRequest http://127.0.0.1:8091/workspace/           # 200，包含 data-route="tasks"
```

结果：健康检查 200；`/agent/status` 返回 401 而非 404，证明采集模块默认生效；工作台页面包含任务导航。刷新后台页面后，任务板块显示“已启用”。

## 三、分发包验证

```powershell
dist\wzstudy-agent.exe version   # 0.1.0
dist\start-agent.cmd             # 首次自动生成 agent.env，然后执行 doctor
```

| 检查 | 结果 |
|---|---|
| `version` | `0.1.0` |
| 配置文件加载 | `agent.env` 被读取；环境变量优先，缺失的 `AGENT_CONFIG` 会明确报错 |
| 占位密钥 doctor | 成功连接 8091，提示“采集端密钥无效或已被轮换” |
| 真实密钥 doctor | e2e 中使用分发包连接 18091，返回采集端名称、权限 `["task.claim","ingest"]`、租约 120 秒 |
| zip 内容 | `wzstudy-agent.exe`、`agent.env.example`、`start-agent.ps1`、`start-agent.cmd`、`AGENT_README.md`、`skills/` |

## 四、端到端联调

脚本：`agent/scripts/e2e.py`

```powershell
python agent/scripts/e2e.py
```

脚本会：

1. 构建服务端与采集端二进制；
2. 启动服务端（内存存储、`AGENT_ENABLED=true`、独立管理员）；
3. 启动模拟模型服务（OpenAI 兼容，脚本化返回 `fetch_url` → `ingest_items` → `finish_task`）；
4. 启动本地测试网页服务；
5. 管理员登录 → 创建来源 → 创建采集端 → 创建任务；
6. 运行 `wzstudy-agent once`；
7. 校验任务终态、统计、事件、入库回执与公开条目；
8. 校验任务板块依赖的 `status` / `agents` / `sources` / `tasks` 列表接口。

实测输出（节选）：

```
task created: e08b60b7-2074-4c06-8c07-fed800a3e67e
task status: succeeded stats: {'items_created': 1, 'items_updated': 0, 'items_unchanged': 0, 'rounds': 3, 'tool_calls': 2, 'duration_ms': 4}
events: ['round_start', 'tool_call', 'tool_result', 'round_start', 'tool_call', 'tool_result', 'round_start', 'tool_call', 'tool_result', 'finish']
intake receipts for task: 1 agent
published items: ['E2E 示例文章']
E2E OK
```

验证结论：

- 任务通过 API 下发、采集端出站领取；
- 真实抓取本地页面并整理入库；
- 服务端二次校验来源、配额与域名；
- 入库回执 `integration=agent` 且关联 `task_id`；
- 管理端可查看任务详情、事件与资料条目；
- 采集端密钥不能当作来源密钥或管理端凭据使用（协议测试覆盖）。

## 三、已知限制

1. 任务板块覆盖派发、列表、详情、取消与重试；采集端身份的创建与轮换仍通过管理 API 完成。
2. 仅支持文本/Markdown 条目，不支持媒体上传与转码。
3. 不支持脚本型 skill、浏览器渲染、搜索工具。
4. 没有服务端定时任务；可用外部 cron 调 `wzstudy-agent once`。
5. `workspace_records` 采用按 kind 全量加载后过滤，任务量很大时需要改为独立表或分页索引（当前单工作区百级任务无压力）。
6. 本文件中的 E2E 使用内存存储；生产 PostgreSQL 需先执行 `go run ./cmd/migrate`。
