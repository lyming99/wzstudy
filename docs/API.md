# WzStudy HTTP API v1

实现版本 **v0.3.0**。真实管理页面为 `/admin/`，完整管理/上传/客户端查询补充文档为 `/docs/management`（对应 `MANAGEMENT.md`）。所有新旧业务路由均列入 `/openapi.json`。

基地址：`http://127.0.0.1:8091/api/v1`（生产必须通过 HTTPS 代理）。

成功：`{"data": ...}`。错误：`{"error":{"code":"conflict","message":"..."}}`。请求 ID 在 `X-Request-ID`；JSON 请求必须带 `Content-Type: application/json`。未知字段、拼接 JSON、无效 UTF-8 内容、超限和空正文均会拒绝，不接受批次内重复 external_id。

## 1. 凭据与权限

`Authorization: Bearer <key>`：

- `ADMIN_API_KEY`：管理/审核/读取的自动化 API 凭据，不是浏览器登录密码，也不能代替数据源密钥接入。
- 管理页面使用系统管理员邮箱与密码，见 `ADMIN_AUTH.md`；登录提交 `email/password`，建立 HttpOnly 会话。
- `READ_API_KEY`：旧版全局只读兼容凭据，读取已发布内容、changes，不可读取草稿或管理；新客户端无需分发它。
- `wzsk_...`：录入端 Source 的独立 Key，只允许自己的 ingest/upload，不能查询资料。
- `wzck_...`：查询客户端 Client 的独立 Key，允许 read/sync，以及显式授予的 feedback；不能资料入库、上传或管理。旧 Key 显式迁移后可保留原前缀，授权依据独立身份记录。

客户端 Key 和权限由管理员配置并持久保存，无需为每个录入端或查询端修改环境变量。写入在同一事务内复查 Key、启用状态和权限；轮换后旧 Key 无法晚提交。后续查询和增量拉取立即按最新权限校验。管理员 API 凭据/旧版全局只读凭据仍由启动配置维护。不要将任何 Key 放入 URL 或日志。

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
  "permissions": ["ingest", "upload"],
  "poll_interval_seconds": 900
}
```

- slug：1–64 字符，小写字母/数字/连字符，首字符为字母或数字，唯一。
- name：非空，最多 200 UTF-8 字节；description 最多 4000 字节。
- integration：manual 录入来源可标记 `collector/skill/mcp`，创建缺省为 collector、更新省略保留；不影响权限。
- kind：`manual`（默认）或 `rss`；`rss` 可读取 RSS 2.0 / Atom，必须给出公开 HTTP(S) feed_url；manual 必须为空。
- language 默认 `zh-CN`；enabled 默认 true；auto_publish 默认 false；采集间隔默认 900，范围 60–86400 秒。
- auto_publish 为 true 时，接入新内容/修改内容直接更新公开快照并产生事件；应仅给可信来源开启。
- manual 录入端的 `permissions` 只接受 `ingest/upload` 且不允许重复；`[]` 撤销全部。创建时省略/null 保留旧默认 `ingest + upload`，不会自动授予阅读/同步；查询客户端单独通过 `/admin/clients` 创建，不复用 Source。RSS 不能设置客户端权限。

返回 201：`{"data":{"source":{"id":"...","revision":1,...},"access_key":"wzsk_..."}}`。

### 查询与修改

- `GET /admin/sources`：所有源配置及采集状态，不返回 Key/hash、ETag 或采集租约。
- `GET /admin/sources/{id}`：单源详情。
- `PUT /admin/sources/{id}`：完整替换可编辑字段，正文为创建字段加 `revision`。slug/kind 必须与原值相同，其他遗漏字段会恢复默认/清空（enabled、permissions 未给出或为 null 时保留原值；permissions=[] 明确撤销全部），不要作为 PATCH 使用。
- 修改配置会撤销正在进行的采集结果；修改 URL 同时清除旧 ETag/Last-Modified。
- `POST /admin/sources/{id}/key`：正文 `{"revision":1}`；返回新 source 和一次性的 access_key，旧 Key 立即失效。
- `POST /admin/sources/{id}/refresh`：无请求体，立即采集，成功返回接入计数。最长约 25 秒；正在采集返回 409；不安全目标/网络/格式错误返回 502，并记录通用 last_error。
- 录入端 enabled=false 停止该 Key 的入库、上传及来源采集，不影响其他客户端读取已有线上快照；没有隐式级联删除接口。

读取端 `GET /sources` 仅返回 id、slug、name、description、category、language，不暴露上游 URL、状态错误或密钥设置。

### 查询客户端与身份迁移

查询客户端是独立 Client，不再是 manual Source 的权限组合：

```text
GET/POST /admin/clients
GET/PUT  /admin/clients/{id}
POST     /admin/clients/{id}/key
GET/POST /admin/identity-migration
```

创建请求为 `{"slug":"desktop-query","name":"桌面学习端","permissions":["read","sync"]}`，可带 description/enabled；返回 `{"data":{"client":{...},"access_key":"<仅本次返回>"}}`。权限可配置 read/sync/feedback，反馈必须显式授予，创建省略/null 默认两项，更新省略/null 保留、[] 撤销全部。PUT 和轮换需要最新 revision，客户端不能管理其他身份。

来源增加 integration（collector/skill/mcp；RSS 固定 rss），仅作接入标记。来源不接受 read/sync，客户端不接受 ingest/upload/inbox。待拆分旧来源的普通编辑/轮换返回 409，应先预检并显式迁移，避免丢失旧 Key 归属。

迁移不会由启动或 GET 自动执行；纯查询 Key 仅迁入 Client，混合 Key 必须明确选择保留给 ingest 或 query，另一侧生成新 Key。全部选择与修订校验成功后原子保存，不修改资料、附件或内容游标。请求及回退说明见 `MANAGEMENT.md`。

## 3. 手动批量接入

### `GET /ingest/{id}/status`

来源专属 Bearer Key 的只读连通检查，要求来源已启用、类型为 `manual` 且具备 `ingest` 权限；不需要 `read`、`sync`、`upload`。仅返回自己的 `source_id`、`enabled`、`auto_publish` 与有效 `permissions`，不返回密钥、资料、其他来源或收件箱，不写入资料、游标或来源配置。Key / 来源不匹配为 401，停用或无入库权限为 403，RSS 来源不支持此客户端入库检查。供 AI Skill `doctor` 使用。

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

### 成功入库回执

响应新增 `receipt_id/source_id/received_at`，保留 created/updated/unchanged/published/items；每项资料返回真实 ID、revision 与状态。回执与资料同事务保存，相同内容重放不增加资料 revision，但可以产生新的成功请求回执。

`GET /admin/intakes?limit=20&offset=0&source_id=<可选UUID>` 仅管理员可读，返回 items/total/limit/offset。limit 1–100、offset 0–10000，按接收时间倒序；不包含资料标题、正文或 Key，不伪造失败/历史批次。回执公开状态是提交时快照。

采集程序、Skill 与 MCP 均调用同一录入服务；MCP 的具体协议、文件授权和运行方式见 `mcp/wzstudy_ingest/README.md`。录入成功不等于已公开，也不会向学习客户端分发。

## 4. 审核、发布和下架

- `GET /admin/items`：管理列表，内容包括 draft 和可选 published 快照。
- `GET /admin/items/{id}`：完整管理记录。
- `POST /admin/items/{id}/publish`，正文 `{"revision":1}`。
- `POST /admin/items/{id}/unpublish`，正文 `{"revision":2}`。

修改需要最新管理记录的 revision；过期返回 409。实际发布/下架增加 revision。重复批准同一快照、重复下架不会增加版本或事件。发布时没有 published_at 的内容使用首次创建时间，避免每次轮询产生时间变化。

下架不删除来源记录/草稿，但读者详情立即变为 404，增量日志产生 `operation=delete` 墓碑，item 不再出现在最新列表。

## 5. 阅读接口

`GET /items` 仅返回公开快照。查询参数：

- `kind`：`article/audio/video/carousel`，未传则全部。
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

## 7. 客户端查询与推荐

服务端只提供已发布资料及变更查询，不主动向客户端分发数据，也不计算个性化推荐。客户端结合学习记录、兴趣标签与自己的学习算法，主动选择候选资料或在本地缓存中排序。

- 浏览候选：`GET /items?kind=video&category=人工智能&tag=AI&limit=20&offset=0`；各过滤条件取交集，可按实际需要省略。
- 阅读详情：`GET /items/{id}`，媒体地址由 `assets` 返回，媒体读取仍需 `read` 权限。
- 更新本地缓存：用 `sync` 权限定期调用 `/changes?after=<已提交游标>`，按 `has_more` 补齐后再按客户端策略等待下一次拉取。
- 学习端默认权限为 `read + sync`；不需要接收推送权限。推荐算法与个性化排序属于客户端；学习反馈/进度可按显式授权上报，用于候选排除。
- 示例 `examples/subscribe.py` 已改为 HTTP 主动轮询，SQLite 在同一事务中保存资料变更和游标；保留旧文件名以兼容已有运行方式。

旧的推送计划、收件箱、SSE 及专题管理路由已移除并返回 404。历史资料、附件和变更游标保留，不清空客户端缓存；旧工作空间的推送/专题记录不再读取或执行；遗留 `inbox` 授权被忽略且不会转换为阅读权限。旧查询或混合 Source 须显式迁入 Client 后才能用原 Key 查询。

## 8. 错误码

400 invalid_request/invalid_json；401 unauthorized；403 permission_denied/source_disabled/origin_denied；404 not_found；405 method_not_allowed；409 conflict/source_busy；413 payload_too_large；415 unsupported_media_type；500 internal_error；502 feed_fetch_failed；503 not_ready/polling_unavailable；504 timeout。

超时/断线不能证明服务端没有提交。接入依赖 external_id+内容哈希重试；管理更新先刷新 revision，不要盲目重放旧 revision。


## 查询客户端扫码身份校验

`GET /client/connection` 只接受独立查询客户端 Bearer Key，需要 read 或 sync 中至少一项。它不读取 Cookie、不接受管理员/全局只读/录入 Key，也不会轮换或创建身份。成功返回 `{data:{type:"wzstudy.query_client",version:1,client_id,name,permissions}}`，失败 401/403，不包含任何 Key 或哈希，禁止缓存。

管理端二维码直接携带 UTF-8 JSON 的 type/version/base_url/client_id/name/access_key（v1 严格六字段、最大 1200 字节）。扫码端确认目标并调用上述接口，核对身份后才保存；不将 Key 放在 URL，不转发到重定向地址。完整协议和标准库解析/注册示例见 `CLIENT_QR.md` 与 `examples/client_connection.py`。


## 客户端反馈与去重

客户端可显式申请 feedback 权限，上报推荐/曝光、已读、喜欢、学习记录和收藏，按事件 ID 幂等保存。`POST /api/v1/client/feedback` 仅写本身份；`GET /api/v1/items?exclude=recommended,read` 或 `unseen=true` 可过滤旧候选，支持时间窗口和本页状态。管理员只读查看反馈或按身份模拟查询，不替用户上报。默认 read/sync 及所有旧 Key 不自动扩权。详见 `docs/CLIENT_FEEDBACK.md`（文档目录内为 `CLIENT_FEEDBACK.md`），示例为 `examples/feedback_client.py`。
