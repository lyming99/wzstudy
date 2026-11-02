# 采集计划：定时、未读存量与外部触发

## 范围与使用入口

工作台「收集任务」包含「执行任务」和「采集计划」两个视图。原有一次性派发、任务模板、采集端协议及入库去重继续保留。新建计划可复用当前任务模板，但保存的是配置快照，后续修改或删除模板不会改变计划。

支持手动、单次定时、固定间隔、每天、每周、未读存量条件、API/Webhook。未读存量模式定期检查当前账号的公开未读资料数量，条件满足时生成采集任务；不是任意脚本条件。任意 Cron、脚本执行和前置任务完成后编排暂不支持。

工作台中的排队有效期、补跑窗口、启用开关和时间预览默认收进「高级设置」；未读模式的检查间隔也在其中。不展开仍会保留并提交现有配置，数值校验失败时自动展开定位。

管理员和普通用户均管理自己的工作区。不能用录入来源 Key、查询 Key、采集端 Key 管理计划。

## 调度与运行语义

- 后台调度器随服务启动，在 `AGENT_ENABLED=true` 时运行，每 15 秒扫描一次；它只生成任务，不执行采集、下载或模型调用。
- 实际采集仍由在线采集端主动领取。等待领取不等于已经开始执行；执行超时预算从领取时开始。
- 调度时间以 UTC 保存；每天/每周规则按计划的 IANA 时区计算，不依赖服务器时区。程序内嵌时区数据，支持 Windows 和精简镜像。
- API 单次时间使用带偏移的 ISO 时间，如 `2026-11-02T09:00:00+08:00`。工作台改用日期时间 Picker，按明确的计划时区换算并按该时区回填；精度为秒。不存在的夏令时时刻会拒绝，重复时刻须选择明确的 UTC 偏移。周期规则使用 `time_of_day` 和 `timezone`。
- 固定间隔最短 1 分钟、最长 10080 分钟。每周使用 `weekdays`，星期日为 0、星期六为 6。夏令时不存在的本地时间跳过当天，重复的本地时间只取一个执行时间，不执行两次。
- 创建、编辑、预览和启用计划都不会立即执行；需要立即执行时使用「运行一次」。手动运行不移动已有定时安排。
- 同一计划默认不重叠执行：`pending/claimed/running` 都算占用。重叠触发保存 `skipped/overlap` 记录，而不是排出无限长队列。
- `queue_ttl_seconds` 是等待领取有效期，默认 3600 秒，可设 60～604800 秒。到期等待任务变为 `expired`，错误码为 `queue_expired`，不再被领取。租约恢复后再次等待的任务也受原有效期约束；运行中的任务仍按执行预算、心跳和取消规则处理。
- 服务恢复后，周期计划只合并处理最近一个已到达的时间点，不遍历补齐全部历史周期。`catch_up_seconds` 默认 3600，可设 30～604800；超过窗口保存 `skipped/misfire_window_exceeded`，继续推进下次时间。未填写或为 0 时使用默认值。
- 暂停停止所有新的手动/定时/Webhook 触发，但不会取消已生成任务；重新启用从当前时间重新计算周期。已过期的单次计划需改为未来时间后才能重新启用。
- 归档停止触发、撤销触发凭据，保留配置及运行历史。列表可勾选「显示已归档计划」。现有任务不被取消或删除。
- 运行记录保存计划版本和本次配置快照，实际任务状态、统计和结果从任务读取。删除已结束任务前保存摘要，历史返回 `task_deleted=true`，仍能查看配置与结果摘要。
- 计划任务的租约重领沿用原 `attempts/max_attempts`。失败后再次执行应通过计划生成新任务，不能调用旧任务 `/retry` 重置历史；该请求返回 409。自动失败重跑未启用，不叠加无限重试。
- 沿用默认 `duplicate_policy=skip` 和已删除资料保护。任务失败、取消、过期都不会回滚已入库资料。

任务目标附加本次触发时间，明确相对日期的参照，不以延迟领取时间为准。附加时间和允许的事件参数后，目标仍受原 4000 UTF-8 字节上限限制；超长基础目标在保存时拒绝，超长事件参数组合在触发时拒绝。服务器策略变化导致原配置不再合法时，记录 `blocked/invalid_task_configuration`。来源或指定采集端被停用/删除时记录 `blocked/source_or_agent_unavailable`。

## 工作区 API

管理端前缀：`/api/v1/admin/agent/collection-plans`（当前管理员的工作区）。

普通用户前缀：`/api/v1/workspace/agent/collection-plans`。

使用已登录的工作区会话；Cookie 写请求应带同源 Origin。Token 模式使用用户登录所得的工作区 Bearer 会话。以下路径均相对上述前缀：

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | 空路径 | 列表；`include_deleted=true` 包含归档 |
| POST | 空路径 | 新建，不立即执行 |
| GET | `/{id}` | 读取计划，包括已归档快照 |
| PUT | `/{id}` | 更新完整配置，必须带当前 `revision` |
| POST | `/preview` | 预览接下来最多 5 个执行或检查时间；未读模式附当前存量判断，手动/Webhook 返回空数组 |
| POST | `/{id}/state` | `{revision,enabled}` 启停；暂停时不要求来源仍可用 |
| POST | `/{id}/delete` | `{revision}` 归档 |
| POST | `/{id}/run` | `{event_id}` 手动运行；重复事件返回原运行记录 |
| GET | `/{id}/runs` | 历史；`limit=1..100`、`offset=0..10000`，返回 `runs/total/limit/offset` |
| POST | `/{id}/trigger-key` | `{revision,revoke?}` 生成/轮换/撤销外部凭据；明文只返回一次 |

单个工作区最多 100 个未归档计划。版本不符返回 409；修改、轮换和归档均使用版本校验。自动调度不改变配置版本。

### 每天采集示例

```json
{
  "name": "每天收集 Go 官方博客",
  "enabled": true,
  "task": {
    "goal": "采集最近一天的 Go 官方博客文章，整理成中文学习资料并附出处",
    "constraints": {
      "max_items": 3,
      "content_type": "article",
      "duplicate_policy": "skip",
      "allow_domains": ["go.dev"]
    },
    "budget": {"max_rounds": 30, "max_tool_calls": 40, "timeout_seconds": 300}
  },
  "schedule": {"kind": "daily", "timezone": "Asia/Shanghai", "time_of_day": "09:00"},
  "queue_ttl_seconds": 3600,
  "catch_up_seconds": 3600
}
```

未指定 `task.source_id` 时，在计划创建时解析并绑定当前工作区的「Agent 收集」来源。后续不能通过外部触发改变来源。模板不保存来源，计划则保存明确的来源绑定。

其他规则示例：

```json
{"kind":"once","timezone":"Asia/Shanghai","at":"2026-11-02T09:00:00+08:00"}
{"kind":"interval","timezone":"UTC","interval_minutes":60}
{"kind":"weekly","timezone":"Asia/Shanghai","time_of_day":"09:00","weekdays":[1,3,5]}
{"kind":"manual","timezone":"UTC"}
{"kind":"webhook","timezone":"UTC"}
```

## 未读存量条件

选择「未读存量条件」，设置比较符和数量阈值。例如「未读存量低于 20 条时补采」，默认每 10 分钟检查一次，可在高级设置调整。

```json
{
  "name": "未读资料不足时补采",
  "enabled": true,
  "task": {
    "goal": "采集 Go 官方博客文章，整理成中文学习资料并保留出处",
    "constraints": {"max_items": 3, "duplicate_policy": "skip", "allow_domains": ["go.dev"]},
    "budget": {"max_rounds": 30, "max_tool_calls": 40, "timeout_seconds": 300}
  },
  "schedule": {
    "kind": "unread_inventory",
    "timezone": "Asia/Shanghai",
    "interval_minutes": 10,
    "unread_condition": {"operator": "lt", "threshold": 20}
  },
  "queue_ttl_seconds": 3600,
  "catch_up_seconds": 3600
}
```

- `operator` 支持 `lt / lte / eq / gte / gt`，对应 `< / ≤ / = / ≥ / >`。
- `threshold` 必须明确填写，范围为 0～1000000 的整数；0 是有效阈值，例如 `eq: 0` 或 `lte: 0` 表示库存为空。缺失、负值、小数和未知比较符拒绝。
- `interval_minutes` 为检查间隔，API 必须提供 1～10080 的整数；工作台默认 10。其他触发方式不能携带 `unread_condition`。
- 统计计划所属工作区中仍有公开投影、未删除的全部资料，不限内容类型、主题或学习模式。不计仅有草稿或尚未公开的待审核资料、已下架或其他工作区资料；有公开版本而另存了私有修订的资料仍按公开版本计入一次。
- 「未读」复用随机学习队列的规则：所属账号 `ViewCount == 0`、没有任何阅读进度、且没有有效的 `read` 标记。开始阅读但未读完也不再算未读；资料修订不会清空此前已读状态。历史客户端 Key 的独立阅读状态不与个人账号合并，管理员也只统计自己的工作区，不是全站总数。
- 创建、启用或改变规则后，第一次检查安排在一个检查间隔后，不立即采集。检查和任务派发在同一工作区写事务内完成，不使用随机队列快照，也不调用外部网络或模型。
- 自动检查不满足条件时只保存 `last_unread_check` 和推进 `next_run_at`，不创建任务或虚假的运行记录。条件满足时复用现有幂等、重叠、来源权限、预算、排队过期和补跑窗口规则；`pending/claimed/running` 仍会阻止同一计划重叠。
- 条件持续满足时，每个后续检查周期都可以再判断；上一任务结束后可生成新的独立任务。这不是失败后的立即重试，也不会每 15 秒扫描都派发。若采集资料还在待审核状态，公开前不会增加本模式的未读存量，请据此设置检查频率和单次采集预算。
- 停机恢复只合并最近一个检查槽，并读取恢复时的当前存量，不推算或补齐历史未读状态。现有 `catch_up_seconds` 仍约束迟到检查；目标中的相对日期参考实际检查时间。
- 工作台的「检查并运行」也会先判断条件，不会强行越过阈值；不改变下一次检查安排。手动检查不满足时保留 `skipped/unread_condition_not_met` 历史，便于审计；同一事件重试返回原结果。
- 自动条件触发的 `trigger_source` 为 `condition`。运行记录的 `unread_check` 保存当时的数量、规则、判断结果与检查时间；以后修改规则不会改写历史。未改变规则的编辑保留最近检查结果，改变规则时清除旧观察值，等待重新检查。

`POST /preview` 是只读查询。原有 `times` 字段保留；本模式表示后续**检查时间**，不是保证执行的时间，并附：

```json
{
  "times": ["2030-01-02T01:10:00Z"],
  "unread_check": {
    "checked_at": "2030-01-02T01:00:00Z",
    "unread_count": 8,
    "operator": "lt",
    "threshold": 20,
    "matched": true
  }
}
```

示例省略 HTTP 外层 `data`。预览不会创建来源、计划、任务或运行记录；未来存量变化或已有任务都可能导致后续检查不派发。

## 专用 API/Webhook 触发

创建 `schedule.kind=webhook` 的计划，按需开放 `allow_url`、`allow_keyword`，再生成触发凭据。默认不允许传入任何目标参数，只触发已有配置。

接口：`POST /api/v1/collection-triggers/{plan_id}`。

请求头：`Authorization: Bearer <wztg_...>`、`Content-Type: application/json`。生产环境必须使用 HTTPS，不把凭据放在 URL、日志或页面持久化存储中。

```json
{
  "event_id": "调用方的稳定事件编号",
  "sent_at": "2026-11-02T01:00:00Z",
  "url": "https://go.dev/blog/example"
}
```

- `event_id` 为 1～128 个 UTF-8 字节，同一逻辑事件重试时保持不变。相同编号不得修改 `url/keyword`，否则返回 409。
- `sent_at` 必填，需在服务端时间前后 5 分钟内。重试可以刷新发送时间，不改变逻辑事件编号和目标参数。
- 外部请求体最大 4 KiB。未知字段直接拒绝，不能传入 `owner_id/source_id/budget/constraints` 等覆盖项。
- 链接必须被计划显式开放，并满足计划允许域名及网络安全规则；关键词最多 500 UTF-8 字节。两者只作为采集参数，不能授予新权限。
- 每个计划每分钟最多接受一个新外部事件；同事件重试不受此计划级限制。认证前另有每个直连 IP 每分钟 60 次、每个服务进程每分钟 600 次请求上限，缓存有容量限制。返回 429 时附 `Retry-After: 60`。反向代理不应依赖任意 `X-Forwarded-For` 绕过此限制，可另外配置公网限流。
- 凭据只保留哈希。轮换、撤销、归档后旧凭据立即失效；停用用户或计划后触发被拒绝。
- 触发凭据不能读取计划、运行日志、资料库或领取任务。外部成功响应只含 `run_id/task_id/dispatch_status/reason`，不暴露配置和结果。
- `dispatch_status` 为 `queued/skipped/blocked`。`queued` 只表示已生成执行任务，不表示采集已经成功。由于已有任务而跳过的事件重试仍返回原跳过记录；要主动再运行，使用新的事件 ID。

数据库中按“工作区＋计划＋触发标识”建立唯一约束，和事务内幂等检查共同防重。定时按具体计划时间点防重，手动与 Webhook 按事件 ID 防重。配置、运行记录和执行任务的写入提交是原子的。

## 迁移与验证

新增迁移：`migrations/000013_collection_plans.sql`。仅新增记录种类和索引，不激活旧的 `kind=plan` 遗留记录，不改写已有任务、资料和模板。升级前备份数据库，先执行项目现有迁移流程再运行新版服务。启动器已有的迁移流程会包含新 SQL；直接启动二进制前应先运行 `go run ./cmd/migrate`。本功能不自动配置或启动采集端。

未读存量模式复用现有计划 JSON 记录、运行记录及索引，本次没有新增 SQL 迁移；尚未升级采集计划基础版本的部署仍需完成上述已有迁移。旧版服务不认识 `unread_inventory`，回滚到旧版前应先暂停或归档此类计划。本轮代码验证不等于已更新运行服务，也不会自动启动真实采集。

回归命令：

```powershell
go test ./...
go vet ./...
node --check internal/adminui/collection-plans.js
node scripts/test_collection_task_ui.cjs
node scripts/test_collection_plan_time.cjs
go test ./internal/study ./internal/httpapi -run 'TestCollectionUnread' -count=1
```

PostgreSQL 合约测试需要 `TEST_DATABASE_URL` 指向专用测试库；测试会创建并清理隔离 schema，禁止指向业务库。运行 `go test ./internal/study -run TestPostgresCollectionPlans -count=1 -v` 验证唯一约束、SQL 活跃任务查询、调度并发和事务回滚。

浏览器测试需要 Python Playwright 及 Chromium，使用内存 HTTP 测试服务，不连接业务服务、不执行真实采集：

```powershell
$env:WZ_COLLECTION_BROWSER_TEST='1'
go test ./internal/httpapi -run '^TestCollectionPlansBrowser$' -count=1 -v
```

覆盖创建/编辑、日期时间 Picker 与夏令时、高级设置折叠保存、未读条件预览/边界/检查/历史/重叠、暂停、归档、凭据生成撤销、移动端布局及普通用户隔离。页面截图保存为 `.artifacts/collection-plans-ui.png`，未读模式截图位于 `.artifacts/collection-unread/`。
