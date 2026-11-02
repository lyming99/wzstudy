# 采集端协议（Agent Protocol）

本协议连接两个独立项目：

- **wzstudy 服务端**：保存采集端身份、指派任务、接收进度与数据、复用现有来源入库。
- **wzstudy-agent 采集端**：独立进程，通过出站 HTTP 拉取任务并执行 agent loop。

设计约束：

1. 采集端只写不读：不能查询资料库、不能发布、不能修改来源配置。
2. 任务是唯一的写入凭据：采集端只能向任务绑定的来源推送数据。
3. 拉模式：采集端主动访问服务端，服务端不反向连接采集端。
4. 所有写事务在服务端串行化，任务不会被两个采集端同时领取。

## 认证

| 项 | 值 |
|---|---|
| 请求头 | `Authorization: Bearer wzag_...` |
| 密钥存储 | 服务端用哈希认证，并在所属工作区保存可查看副本；创建、轮换或查看时返回。旧记录在采集端下次领取任务时自动补存副本 |
| 权限 | `task.claim`（领取/心跳/上报）、`ingest`（任务作用域入库）；不含 `read`/`sync`/`upload` |
| 停用 | 采集端停用后所有请求返回 401/403 |

采集端密钥不能用于 `/api/v1/ingest/{source_id}`（那是来源密钥），来源密钥也不能用于 `/api/v1/agent/*`。

## 端点

所有路径前缀为 `/api/v1`。

### 采集端

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/agent/status` | 自查身份、权限、服务端时间、租约秒数 |
| POST | `/agent/tasks/claim` | 领取任务；`204` 表示暂无任务 |
| POST | `/agent/tasks/{id}/heartbeat` | 续租并读取取消信号 |
| POST | `/agent/tasks/{id}/events` | 批量上报进度事件（按 seq 幂等） |
| POST | `/agent/tasks/{id}/items` | 推送条目（服务端校验配额与域名） |
| POST | `/agent/tasks/{id}/complete` | 提交终态（幂等） |
| POST | `/agent/tasks/{id}/release` | 归还租约（优雅退出） |
| POST | `/agent/uploads` | 上传单个媒体/文本文件；资产先登记，首次入库时绑定任务来源 |

上传成功后，条目 `assets` 只需引用返回的 `id`；服务端会按登记信息校验 MIME、大小与任务数据类型，不会信任客户端提交的 MIME。

### 工作区管理端（用户会话）

普通账户使用 `/workspace/agent/*` 管理自己的采集端、任务和模板；管理员也可以使用这些接口管理管理员个人工作区。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/workspace/agent/status` | 模块开关、租约、任务计数、采集端列表 |
| GET/POST | `/workspace/agent/agents` | 列表/创建采集端身份（创建返回密钥一次） |
| GET/PUT | `/workspace/agent/agents/{id}` | 读取/更新（版本校验） |
| GET | `/workspace/agent/agents/{id}/key` | 查看当前身份的 Agent 密钥和已记住的非敏感打包配置 |
| POST | `/workspace/agent/agents/{id}/key` | 轮换密钥 |
| POST | `/workspace/agent/agents/{id}/package` | 用当前密钥和模型配置生成可运行 ZIP 包 |
| POST | `/workspace/agent/agents/{id}/delete` | 删除身份（版本校验；有未结束任务时 409 `agent_in_use`） |
| GET/POST | `/workspace/agent/tasks` | 分页列表/创建任务 |
| GET/POST | `/workspace/agent/task-templates` | 列出/保存当前配置为任务模板 |
| POST | `/workspace/agent/task-templates/{id}/delete` | 删除任务模板；表单配置不受影响 |
| GET | `/workspace/agent/tasks/{id}` | 任务详情（含统计与终态） |
| GET | `/workspace/agent/tasks/{id}/events` | 进度事件 |
| POST | `/workspace/agent/tasks/{id}/cancel` | 取消；运行中任务在下次心跳生效 |
| POST | `/workspace/agent/tasks/{id}/retry` | 失败/取消/过期任务重置为待领取 |
| POST | `/workspace/agent/tasks/{id}/delete` | 删除已结束任务及其事件 |
| GET | `/workspace/agent/binary` | 下载采集端程序，供用户自行配置 agent.env |

### 管理员端（管理员会话或管理员 API Key）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/admin/agent/status` | 管理员个人工作区的模块状态 |
| GET/POST | `/admin/agent/agents` | 管理员个人工作区的采集端身份 |
| GET/PUT | `/admin/agent/agents/{id}` | 读取/更新（版本校验） |
| GET | `/admin/agent/agents/{id}/key` | 查看当前身份的 Agent 密钥 |
| POST | `/admin/agent/agents/{id}/key` | 轮换密钥 |
| POST | `/admin/agent/agents/{id}/package` | 生成可运行 ZIP 包（管理员入口） |
| POST | `/admin/agent/agents/{id}/delete` | 删除身份（版本校验；有未结束任务时 409 `agent_in_use`） |
| GET/POST | `/admin/agent/tasks` | 管理员个人工作区的任务 |
| GET/POST | `/admin/agent/task-templates` | 管理员个人工作区的任务模板 |
| POST | `/admin/agent/task-templates/{id}/delete` | 删除任务模板 |
| GET | `/admin/agent/tasks/{id}` | 任务详情 |
| GET | `/admin/agent/tasks/{id}/events` | 进度事件 |
| POST | `/admin/agent/tasks/{id}/cancel` | 取消任务 |
| POST | `/admin/agent/tasks/{id}/retry` | 重试任务 |
| POST | `/admin/agent/tasks/{id}/delete` | 删除已结束任务 |
| GET | `/admin/agent/binary` | 下载内置采集端程序 |
| POST | `/admin/agent/deployments` | 生成带模型配置的一键部署包（仅管理员） |
| POST | `/admin/agent/models` | 查询模型列表（仅管理员） |
| GET/PUT | `/admin/agent/runtime` | 服务端托管采集端运行时（仅管理员） |
| POST | `/admin/agent/runtime/start`、`/stop` | 启动/停止内置采集端（仅管理员） |
| GET/POST | `/admin/agent/releases` | 发布/管理采集端程序包（仅管理员） |

工作区接口始终从用户会话确定所有者，不接受 `user_id/owner_id` 选择器。采集端、任务、事件、模板和入库资料都按工作区隔离；管理员内置运行时、模型密钥、一键部署与程序发布不能由普通账户调用。

## 数据模型

### 任务

```json
{
  "id": "uuid",
  "source_id": "uuid",
  "title": "采集 Go 官方博客近三篇文章",
  "goal": "自然语言采集目标",
  "constraints": {
    "max_items": 50,
    "language": "zh-CN",
    "category": "编程",
    "tags": ["Go"],
    "allow_domains": ["go.dev", "blog.golang.org"]
  },
  "budget": { "max_rounds": 100, "max_tool_calls": 200, "timeout_seconds": 300 },
  "status": "pending",
  "attempts": 0,
  "max_attempts": 3,
  "agent_id": "",
  "assigned_agent_id": "",
  "lease_expires_at": null,
  "deadline": null,
  "cancel_requested": false,
  "stats": { "items_created": 0, "items_updated": 0, "items_unchanged": 0, "rounds": 0, "tool_calls": 0, "duration_ms": 0 },
  "created_at": "...",
  "updated_at": "..."
}
```

`budget.max_tool_calls` 默认 200、最大 1000，仅限制单个 Agent 任务的一次执行；各任务独立计数，不共享采集端额度。租约重领后开始新的执行时重新计数。

`source_id` 在创建时固定，采集端不能修改。`allow_domains` 为空表示不限（仍禁止内网），非空时条目 `url` 的 host 必须是其中之一或子域。

### 条目（Content）

与现有入库契约一致：

| 字段 | 限制 |
|---|---|
| `external_id` | 1-512 字节，同一来源内幂等 |
| `title` | 1-500 字节 |
| `summary` | ≤8000 字节 |
| `content` | ≤262144 字节 |
| `format` | `text` 或 `markdown` |
| `kind` | `article` / `audio` / `video` / `carousel` |
| `tags` | ≤20 个，每个 ≤80 字节 |
| `url` | 合法 HTTP(S) 地址 |

### 可选字幕：服务端契约与 Agent 工具

`POST /agent/tasks/{id}/items` 已接受音视频条目的可选 `subtitles`，无需新增独立字幕上传端点。每轨结构为：

```json
{"id":"zh","language":"zh-CN","format":"srt","content":"1\n00:00:00,000 --> 00:00:02,000\n字幕正文"}
```

- 字幕仅适用于 `kind=video|audio`；每条资料最多 8 轨，轨道 ID 唯一，ID/语言上限分别为 200/35 UTF-8 字节，单轨正文最多 262144 字节，格式为 `srt` 或 `vtt`。
- AI 自主决定是否上传；省略 `subtitles` 不影响音视频入库。不要求自动生成字幕，也不会因“没有字幕”拒绝合法媒体。
- Agent 的 `fetch_subtitle` 工具仅获取可访问的公开 SRT/WebVTT 文件，并保留本次任务执行内的引用。工具返回 `uploaded:false`，获取不等于上传。
- 工具侧的 `ingest_items.subtitles` 可使用 `{id,language,ref}` 或 `{id,language,format,content}`。在发起 HTTP 请求前解析 ref，仅把实际字幕正文发送到上述服务端契约；**服务端不接收 ref，不把字幕当作媒体资产 ID**。
- 获取失败可跳过；引用失效、轨数/大小/字段无效时工具报错，AI 可移除或修正字幕后继续提交。服务端原有事务、租约、来源绑定、审核及去重规则不变。
- 字幕地址及重定向均受公网和任务允许域名限制，不读取任意本地文件、绕过访问控制或执行字幕中的指令。

## 状态机

```
pending ──claim──▶ claimed ──heartbeat/events──▶ running ──complete──▶ succeeded
   ▲                  │                             │
   │                  │ 租约到期                     ├──complete(failed)──▶ failed
   └──release/回收────┘                             └──complete(cancelled)▶ cancelled
                      └──attempts > max_attempts──▶ expired
```

- 领取：写事务内先回收过期租约，再按 `priority` 降序、`created_at` 升序选择任务。
- 租约：默认 300 秒；心跳间隔建议 30 秒；过期任务回到 `pending`，`attempts` 保留。
- 取消：`pending` 直接变 `cancelled`；`claimed/running` 置 `cancel_requested`，采集端在心跳响应中读取。
- 次数：超过 `max_attempts` 后由 `requeue` 置为 `expired`。

## 幂等与重试

| 场景 | 规则 |
|---|---|
| 事件重复上报 | 主键 `task_id + attempt + seq`，覆盖写入；返回 `accepted` / `highest_seq` |
| 条目重复推送 | `source_id + external_id` 更新现有条目，不新建 |
| 终态重复提交 | 已终态直接返回 204，不覆盖首次结果 |
| 领取重复 | 串行写事务 + 状态/租约 CAS，多采集端不会重复领取 |
| 网络重试 | 采集端对心跳/事件/条目/终态做指数退避（最多 3 次）；`claim` 只发一次，避免重复消耗任务 |
| 租约失效 | 服务端返回 409 `lease_expired` / `task_claimed`，采集端必须停止写入 |

## 错误码

| HTTP | code | 含义 |
|---|---|---|
| 401 | `unauthorized` | 缺少或无效的采集端密钥 |
| 403 | `permission_denied` | 采集端未获授该权限 |
| 403 | `agent_or_source_disabled` | 采集端或来源已停用 |
| 403 | `domain_not_allowed` | 条目链接不在允许域名内 |
| 404 | `not_found` | 任务/采集端不存在（或功能未启用） |
| 409 | `task_claimed` | 任务由其他采集端持有 |
| 409 | `lease_expired` | 租约过期或 attempt 不匹配 |
| 409 | `task_cancelled` | 任务已取消，不能再推送 |
| 409 | `conflict` | 状态不允许该操作 |
| 413 | `item_quota_exceeded` | 超过条目或事件配额 |
| 422 | `invalid_request` | 字段无效 |

错误体统一为：

```json
{ "error": { "code": "lease_expired", "message": "收集任务租约已过期" } }
```

## 兼容性

采集端在 `User-Agent` 中上报版本；`claim` 请求体也携带 `version`/`hostname` 供管理端展示。服务端不阻断旧版本，但新增字段遵循向后兼容：只增加可选字段，不复用既有字段语义。

完整 OpenAPI 见 `docs/openapi.json`（`/openapi.json` 也提供运行时版本）。


## 采集计划任务兼容性

定时、手动和 Webhook 计划最终都生成普通 `CollectionTask`，继续使用现有 claim/heartbeat/events/items/complete/release，无需新增采集端执行入口。任务可附带可选 `plan_id`、`plan_run_id`、`queue_expires_at`；旧采集端可忽略这些字段。排队有效期由服务端在领取前检查，不替代领取后的执行 deadline。目标中已附加本次触发时间和经过计划白名单校验的事件参数，不以领取时刻重新解释相对日期。

计划生成任务的终态不可通过原 `/retry` 重置（409），应从计划运行一次以生成新的运行记录和任务；租约重领仍沿用原 attempts 机制。删除终态任务前保留计划运行摘要。更多规则见 `COLLECTION_PLANS.md`。
