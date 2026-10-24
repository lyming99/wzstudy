# WzStudy HTTP API v1

实现版本 **v0.3.0**。真实管理页面为 `/admin/`，完整管理/上传/推送补充文档为 `/docs/management`（对应 `MANAGEMENT.md`）。所有新旧业务路由均列入 `/openapi.json`。

基地址：`http://127.0.0.1:8091/api/v1`（生产必须通过 HTTPS 代理）。

成功：`{"data": ...}`。错误：`{"error":{"code":"conflict","message":"..."}}`。请求 ID 在 `X-Request-ID`；JSON 请求必须带 `Content-Type: application/json`。未知字段、拼接 JSON、无效 UTF-8 内容、超限和空正文均会拒绝，不接受批次内重复 external_id。

## 1. 凭据与权限

`Authorization: Bearer <key>`：

- `ADMIN_API_KEY`：管理/审核/读取的自动化 API 凭据，不是浏览器登录密码，也不能代替数据源密钥接入。
- 管理页面使用系统管理员邮箱与密码，见 `ADMIN_AUTH.md`；登录提交 `email/password`，建立 HttpOnly 会话。
- `READ_API_KEY`：读取已发布内容、changes 和 stream，不可读取草稿或管理。
- `wzsk_...`：创建数据源/重置密钥时返回一次，可调用该源 `/ingest/{id}`、`/ingest/{id}/files` 与 `/clients/{id}/inbox`。

源密钥在同一接入事务内校验；轮换后旧 Key 无法晚提交。管理员/读取密钥由环境变量管理，修改后需重启服务。不要放入 URL 或日志。

## 2. 数据源

### `POST /admin/sources`

```json
{
  "slug": "go-lessons",
  "name": "Go 学习资料",
  "description": "课程、文章及实践记录",
  "kind": "manual",
  "feed_url": "",
  "category": "编程 / Go",
  "language": "zh-CN",
  "enabled": true,
  "auto_publish": false,
  "poll_interval_seconds": 900
}
```

- slug：1–64 字符，小写字母/数字/连字符，首字符为字母或数字，唯一。
- name：非空，最多 200 UTF-8 字节；description 最多 4000 字节。
- kind：`manual`（默认）或 `rss`；`rss` 可读取 RSS 2.0 / Atom，必须给出公开 HTTP(S) feed_url；manual 必须为空。
- language 默认 `zh-CN`；enabled 默认 true；auto_publish 默认 false；采集间隔默认 900，范围 60–86400 秒。
- auto_publish 为 true 时，接入新内容/修改内容直接更新公开快照并产生事件；应仅给可信来源开启。

返回 201：`{"data":{"source":{"id":"...","revision":1,...},"access_key":"wzsk_..."}}`。

### 查询与修改

- `GET /admin/sources`：所有源配置及采集状态，不返回 Key/hash、ETag 或采集租约。
- `GET /admin/sources/{id}`：单源详情。
- `PUT /admin/sources/{id}`：完整替换可编辑字段，正文为创建字段加 `revision`。slug/kind 必须与原值相同，其他遗漏字段会恢复默认/清空（enabled 未给出则保留），不要作为 PATCH 使用。
- 修改配置会撤销正在进行的采集结果；修改 URL 同时清除旧 ETag/Last-Modified。
- `POST /admin/sources/{id}/key`：正文 `{"revision":1}`；返回新 source 和一次性的 access_key，旧 Key 立即失效。
- `POST /admin/sources/{id}/refresh`：无请求体，立即采集，成功返回接入计数。最长约 25 秒；正在采集返回 409；不安全目标/网络/格式错误返回 502，并记录通用 last_error。
- enabled=false 停止接入、采集与该来源收件箱读取，不影响已有线上快照；没有隐式级联删除接口。

读取端 `GET /sources` 仅返回 id、slug、name、description、category、language，不暴露上游 URL、状态错误或密钥设置。

## 3. 手动批量接入

### `POST /ingest/{source_id}`

使用对应源 Key，不使用管理 Key。

```json
{
  "items": [
    {
      "external_id": "lesson-001",
      "title": "理解 context",
      "summary": "学习超时与取消传播",
      "content": "# Context\n正文...",
      "format": "markdown",
      "url": "https://example.com/lessons/context",
      "author": "课程团队",
      "category": "编程 / Go",
      "language": "zh-CN",
      "tags": ["Go", "并发"],
      "published_at": "2026-10-23T08:00:00Z",
      "source_updated_at": "2026-10-23T09:00:00Z"
    }
  ]
}
```

限制：

| 字段 | 约束 |
| --- | --- |
| 请求 | 最大 4 MiB，1–100 条，整批提交或整批失败 |
| external_id | 稳定且非空，最多 512 字节，唯一性范围为当前源 |
| title | 非空，最多 500 字节 |
| summary/content | 最多 8000 / 262144 字节 |
| format | `text`（默认）或 `markdown`，不接受原始 HTML 格式 |
| author/category/language | 最多 200 / 200 / 35 字节；分类和语言未给出时使用源默认值 |
| tags | 最多 20 个，每个最多 80 字节，去重并排序后比较内容哈希 |
| URL | 可空，HTTP(S)，不得包含用户名密码或 fragment，最多 2048 字节 |
| 时间 | RFC3339，1970–9999 年，可省略，不作为游标 |

成功返回：

```json
{"data":{"created":1,"updated":0,"unchanged":0,"published":0,"items":[{"id":"...","source_id":"...","external_id":"lesson-001","revision":1,"draft":{},"created_at":"...","updated_at":"..."}]}}
```

同一 source_id + external_id 更新同一 ID；标题改名不会创建另一记录。相同规范化内容不增加 revision，也不产生新的事件。重试不会自动撤回已下架的相同内容；需要管理员再次发布，或提交实际变更且该源启用 auto_publish。

草稿更新保留旧 published 快照。批次遗漏的内容不删除。源 Key 不能通过传入其他 source_id 越权。

## 4. 审核、发布和下架

- `GET /admin/items`：管理列表，内容包括 draft 和可选 published 快照。
- `GET /admin/items/{id}`：完整管理记录。
- `POST /admin/items/{id}/publish`，正文 `{"revision":1}`。
- `POST /admin/items/{id}/unpublish`，正文 `{"revision":2}`。

修改需要最新管理记录的 revision；过期返回 409。实际发布/下架增加 revision。重复批准同一快照、重复下架不会增加版本或事件。发布时没有 published_at 的内容使用首次创建时间，避免每次轮询产生时间变化。

下架不删除来源记录/草稿，但读者详情立即变为 404，增量日志产生 `operation=delete` 墓碑，item 不再出现在最新列表。

## 5. 阅读接口

`GET /items` 仅返回公开快照。查询参数：

- `source_id`：可选，最多 20 个 UUID，逗号分隔。
- `category`、`tag`：精确匹配。
- `q`：标题/摘要子串，大小写不敏感，最多 200 字节。
- `limit`：默认 20，范围 1–100。
- `offset`：默认 0，范围 0–10000。

按 published_at 降序、ID 降序稳定打破同时间排序。管理列表采用相同筛选参数，但匹配草稿，按记录 updated_at 排序。

`GET /items/{id}`：只返回公开快照，未发布/已下架返回 404。

PublicItem 字段为 `id`、`source_id`、`revision`、全部 Content 字段、`updated_at`。读取端永远没有 draft、内容哈希、Key 或采集租约。

**offset 列表用于浏览，不保证多次请求间的冻结快照；不要拿列表末页时间戳或列表分页来替代可靠同步。**

## 6. 增量拉取

### `GET /changes?after=0&limit=100&source_id=<可选UUID列表>`

```json
{
  "data": {
    "changes": [
      {"cursor":1,"operation":"upsert","item_id":"...","source_id":"...","revision":2,"item":{"id":"...","title":"..."},"created_at":"..."},
      {"cursor":2,"operation":"delete","item_id":"...","source_id":"...","revision":3,"created_at":"..."}
    ],
    "next_cursor":2,
    "high_watermark":2,
    "has_more":false
  }
}
```

- after 默认 0，必须是非负 int64，超前于服务端时钟返回 400（可能意味着连错环境、旧备份恢复或客户端状态损坏）。
- limit 范围 1–100，控制的是**扫描的全局事件窗口**，不是保证返回的匹配条数。
- 所有返回事件严格按 cursor 升序；内容变化和事件一起提交，未提交事务不会被高水位越过。
- 带 source_id 的过滤页可以 changes 为空但 next_cursor 前进。**始终保存 next_cursor**，has_more=true 时继续请求，不能因空页停止。
- 不支持分类/标签级变化筛选，避免文章改分类时客户端残留旧缓存；按来源订阅后在本地计算分类/标签视图。
- 初次完整同步从 0 开始回放，按 item_id 进行 upsert/delete，并把缓存修改与 next_cursor 原子提交；失败时重放上一页即可。
- 修改 source_id 筛选集合必须重建缓存并从 0 重放，不要沿用旧筛选的游标。
- 日志当前没有自动过期，删除墓碑永久保留。
- 日志只同步内容的公开快照；来源名称等元信息变更请重新读取 `GET /sources`，不属于内容变更事件。

## 7. SSE 实时流

### `GET /stream?after=0&source_id=<可选UUID列表>`

需要读取/管理 Bearer Key。返回 `text/event-stream`，禁止反向代理缓冲。`Last-Event-ID` 请求头存在时优先于 after（用于重连）。

```text
retry: 3000

id: 1
event: change
data: {"cursor":1,"operation":"upsert","item_id":"...","source_id":"...","revision":2,"item":{...}}

id: 2
event: checkpoint
data: {"cursor":2}

: heartbeat

```

- change：完整的持久化事件；处理成功后再保存 id。
- checkpoint：过滤扫描进度，没有匹配事件也需保存 id。
- error：存储读取失败，关闭并重连；不是需要写入本地业务数据的事件。
- 心跳注释不修改游标；每约 10 秒发送，空闲连接仍保活。
- 断线后带最后**已成功落盘**的 id 重连；可能重复投递，不保证恰好一次。
- 单连接约 30 分钟后关闭，客户端应重连；单实例连接上限默认 128，满时 503。

可运行客户端见 `examples/subscribe.py`，其 SQLite 事务同时提交数据和游标。

## 8. 错误码

400 invalid_request/invalid_json；401 unauthorized；403 source_disabled/origin_denied；404 not_found；405 method_not_allowed；409 conflict/source_busy；413 payload_too_large；415 unsupported_media_type；500 internal_error；502 feed_fetch_failed；503 not_ready/stream_limit/polling_unavailable；504 timeout。

超时/断线不能证明服务端没有提交。接入依赖 external_id+内容哈希重试；管理更新先刷新 revision，不要盲目重放旧 revision。
