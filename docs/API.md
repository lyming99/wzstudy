# WzStudy HTTP API v1

当前权限模型：所有账号共用 `/workspace/` 登录入口并拥有自己的资料和学习工作区；管理员使用本人工作区，同时额外拥有用户管理权限。资料与学习数据按账号隔离，管理员不能直接访问其他用户的私人资料。`/admin/` 保留为账号管理兼容入口，学习页为 `/learn/`。用户工作区说明见 `USER_WORKSPACES.md`（服务端 `/docs/user-workspaces`），完整路由见 `/openapi.json`。

基地址：`http://127.0.0.1:8091/api/v1`（生产必须通过 HTTPS 代理）。

成功：`{"data": ...}`。错误：`{"error":{"code":"conflict","message":"..."}}`。请求 ID 在 `X-Request-ID`；JSON 请求必须带 `Content-Type: application/json`。未知字段、拼接 JSON、无效 UTF-8 内容、超限和空正文均会拒绝，不接受批次内重复 external_id。

## 1. 凭据、所有者与权限

- 浏览器账号统一使用 `POST /auth/login` 提交邮箱和密码；响应 `data.role` 为 `admin` 或 `user`，均设置 `wzstudy_user` Cookie，并返回本人用户身份。服务端核验管理员邮箱和对应密码后授予额外管理能力，不接受客户端指定角色；`ADMIN_API_KEY` 本身不具有资料读写、附件或学习数据权限。
- `/user/auth/login` 兼容管理员和普通用户；原生应用可指定 `session_mode=token` 获得 `wzus_...` 会话令牌，管理员的用户令牌也可访问管理接口。工作台与学习端使用同一个个人账号。
- `/workspace/*` 资料管理只接受用户会话；`/client/learning*`、`/client/feedback*` 和个性化推荐同样必须用户登录。
- `wzsk_...` 为用户创建的独立录入来源 Key，只能在所属工作区提交本来源资料与授权文件。
- `wzck_...` 为用户创建的查询客户端 Key，只读所属用户工作区的已发布资料并按授权同步；不能代替用户进行学习。旧 `feedback` 权限记录为历史兼容保留，不再使应用 Key 成为学习身份。
- 旧 `READ_API_KEY` 不再隐式读取全局资料。没有所有者的旧来源/客户端 Key 也不会获得任何用户的工作区。

资料“发布”表示可以被本人及本人查询客户端读取，不是公开给其他用户。所有者由服务端认证决定，筛选参数不授予跨用户权限。请勿将密码、会话令牌或 Key 放进 URL、日志或浏览器持久存储。密码重置、停用及用户改密撤销所有旧用户会话；退出只撤销当前会话。Cookie 写请求必须来自本站 Origin。

## 公开合集与章节

合集是用户本人编排的内容目录，不是个人收藏。查询接口：

```text
GET /catalog/collections?limit=20&offset=0
GET /catalog/collections/{id}
GET /catalog/collections/changes?after=0&limit=20
```

列表、详情需要 read；独立变更流需要 sync。列表返回摘要，详情按章节顺序返回可见内容 ID；章节关联的内容下架后不再返回该章节。变更流只含已公开合集，使用自己的 cursor/high_watermark，不改变 `/changes` 的资料游标。旧版或下架合集的历史快照作为删除变更返回，不泄露旧快照。每页最多 20 个变更。

用户使用 `/workspace/catalog/collections` 创建、编辑、发布、下架和删除合集。编辑草稿不会更改公开快照；发布时所有章节必须引用当前已发布内容。详细字段见 `/openapi.json` 和管理文档。新增功能需要数据库迁移 `000005_catalog_collections.sql`，不会覆盖历史 `collection` 记录。

## 用户学习路线图

管理端新增独立“学习路线图”入口（`/workspace/#roadmaps`），以思维导图编辑节点、层级、顺序、说明、折叠状态和资料引用。API 为 `/workspace/roadmaps`（GET/POST）、`/workspace/roadmaps/{id}`（GET/PUT）与 `/workspace/roadmaps/{id}/delete`（POST）。仅资料所有者本人可读写；创建 revision=0，更新和删除检查最新 revision；最多 300 节点、12 层。该模块不发布到客户端，不写个人学习进度，不改变公开资料/合集游标。完整使用与请求示例见 `LEARNING_ROADMAPS.md`、管理文档及 `/openapi.json`。

## 私人学习数据（用户身份 v2）

`GET /client/learning/capabilities` 需要用户会话，返回 `type=wzstudy.learning`、`version=2`、`identity=user`、`user_id` 和能力列表。管理员可使用本人用户会话学习；管理员 API Key、查询客户端、录入端和旧全局 Key 均不能代替学习者。学习与录入都按稳定用户 ID 隔离；同账号跨设备共享本人数据，不同用户既不共享资料也不共享学习空间。历史响应字段 `client_id` 在用户认证下等于当前用户 ID，只作兼容，不接受它指定归属。

```text
POST /client/learning/events
GET  /client/learning?item_id=<UUID>[,<UUID>]&limit=20&offset=0
GET  /client/learning/events?after=0&limit=100
GET  /client/learning/items/{item_id}/records?limit=20&offset=0
GET  /client/learning/items/{item_id}/records/{record_id}
GET  /items/{id}/learning-stats
GET  /client/learning/items/{item_id}/stats
```

上报格式 `{"events":[...]}`，每批 1–100 条，在同一事务中复查权限并原子提交。每条必须有非零 UUID `event_id`、原始 WzStudy `item_id` 和 `action`；重试必须保持事件 ID 和全部字段不变。同用户相同 ID/内容返回 duplicate，不同内容返回冲突。下架内容不接受新学习事件，已提交事件可以幂等重试；本人删除自己的旧记录仍可执行。事件和游标与原 `/client/feedback`、公开 `/changes` 相互独立。

| action | 字段与语义 |
|---|---|
| `view` | 一次客户端确认的有效查看；查询和预加载不会自动生成 |
| `review_plan` | `value=true/false` 设置或取消复习计划 |
| `review` | 完成一次复习，增加次数并清除待复习状态 |
| `progress` | `position`、`mode` 和 `expected_revision`；服务器使用版本检查拒绝旧进度覆盖，允许主动回看 |
| `record_create` | `record_id`、`text`，可选 `position`、`parent_id`、`as_danmaku`；私人记录最多 300 字符。可选 `draft_revision` 将创建记录与清空匹配的当前草稿原子提交；版本、文字或位置变化时整体冲突，重试不会清除后来创建的草稿 |
| `record_delete` | `record_id`，只能删除本身份且所属资料一致的记录 |
| `duration` | `session_id`、从 1 开始递增的 `session_sequence`、`start_at/end_at`；单段最多 2 分钟，重复/重叠区间按并集计算 |

私人草稿可经 `GET /client/learning/items/{item_id}/draft[?parent_id=...]` 查询，经 `POST /client/learning/drafts/commands` 以事件 ID 和期望修订保存或清空。清空保留版本墓碑，旧设备不能以旧版本复活已删除草稿；回复草稿的父记录必须属于当前登录用户。历史草稿只能在用户确认来源及身份后，使用 `POST /client/learning/drafts/import-legacy` 在目标不存在任何草稿或墓碑、资料修订一致时导入，保留原编辑时间；冲突时不覆盖或删除本机草稿。草稿与已提交记录独立，不计为学习记录。每次接受的草稿写入在数据源保留 用户私有的不可变事件快照；清空当前草稿并不清除快照。可通过 `GET /client/learning/items/{item_id}/draft/history` 分页查看、通过 `GET /client/learning/drafts/events/{event_id}` 精确读取；历史留存遵循用户工作区的数据保留规则。

历史未完成的复习待办可在用户明确核验归属后调用 `POST /client/learning/import-legacy-reviews`，以 `{"reviews":[...]}` 传入事件 ID、资料 ID、当前公开修订和原待办关联时间。服务器在事务中检查该身份此资料没有任何既有复习计划或复习事件；已有撤销、完成或待复习记录都拒绝覆盖。该接口只创建待复习状态，不会伪造完成复习次数，也不删除本地待办。重复请求必须使用相同事件 ID 和载荷。

历史私人记录可经用户明确确认后调用 `POST /client/learning/import-legacy-records`，每批 `{"records":[...]}` 最多 100 条。每条需显式给出事件、资料、记录 UUID、正文、原创建／更新时间和资料公开修订，可带位置、已有父记录及音频弹幕标记。要求资料仍公开、当前公开修订一致、记录 ID 未占用；检查与写入原子执行，父记录必须已存在或在批中先于回复。学习身份由用户会话决定；同事件及全部字段完全相同可幂等重试，字段变化冲突。记录时间按原值保留，该接口不会证明旧记录归属、上传其他历史或清理本机数据；共享记录和无法保真映射的记录应保留待处理。

`position` 的 `version` 是公开资料 revision；`kind=media` 使用 `time_ms`，`kind=article` 使用稳定的 `block_id=body/source` 和 UTF-16 `offset`，`kind=image` 使用原始资源 UUID `image_id`。文章进度的 mode 为 `preview/fulltext`，其他类型使用空 mode；本机 feed ID 前缀不能传给服务端。

学习数据列表的 `duration_ms` 可使用同时提供的 RFC3339 `from` 和 `until` 限定半开时间窗口（最多 32 天），由客户端按本地日期计算 UTC 边界；不提供时返回全量并集。`/items/{id}/learning-stats` 只返回公开资料的聚合数量，喜欢和收藏按当前有效查询身份计数，不返回其他人的记录；独立查询客户端也可用 `read` 或 `sync` 权限访问 `/client/learning/items/{item_id}/stats`，该接口不接受管理员 API Key、录入端或旧全局 Key；管理员用户会话只可查询本人工作区。用户可在 `/workspace/clients/{id}/learning` 等路径检查本人查询客户端的历史兼容数据；系统管理员不能借此查看用户学习数据。

## 学习推荐查询

`GET /client/recommendations?limit=20&cursor=...` 只接受有 read 权限的独立查询客户端 Key，不接受管理员、录入端或旧全局 Key 冒充学习者。首屏可省略 cursor；后续请求使用返回的 `next_cursor`，直至 `has_more=false`。

服务端在最多 1000 条已发布候选上执行可解释规则排序：待复习优先，结合未查看状态、喜欢、收藏和标签/分类偏好，再按公开时间及 ID 稳定排序。无 feedback 权限时使用不含个人历史的基础排序。读取推荐不会自动上报推荐、曝光、查看或复习。客户端实际选择或展示后应单独上报事件。

游标绑定查询客户端，快照最多保留 10 分钟并随服务进程重启失效；权限变化时需重新查询。每页重新检查内容公开状态，已下架内容不会继续返回；快照仅固定候选顺序，不冻结资料正文。候选耗尽可开始新一轮，不等于自动完成复习。

## 2. 数据源

### `POST /workspace/sources`

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
- integration：manual 录入来源可标记 `collector/skill/mcp/agent`，创建缺省为 collector、更新省略保留；不影响权限。
- kind：`manual`（默认）或 `rss`；`rss` 可读取 RSS 2.0 / Atom，必须给出公开 HTTP(S) feed_url；manual 必须为空。
- language 默认 `zh-CN`；enabled 默认 true；auto_publish 默认 false；采集间隔默认 900，范围 60–86400 秒。
- auto_publish 为 true 时，接入新内容/修改内容直接更新公开快照并产生事件；应仅给可信来源开启。
- manual 录入端的 `permissions` 只接受 `ingest/upload` 且不允许重复；`[]` 撤销全部。创建时省略/null 保留旧默认 `ingest + upload`，不会自动授予阅读/同步；查询客户端单独通过 `/workspace/clients` 创建，不复用 Source。RSS 不能设置客户端权限。

返回 201：`{"data":{"source":{"id":"...","revision":1,...},"access_key":"wzsk_..."}}`。

### 查询与修改

- `GET /workspace/sources`：所有源配置及采集状态，不返回 Key/hash、ETag 或采集租约。
- `GET /workspace/sources/{id}`：单源详情。
- `PUT /workspace/sources/{id}`：完整替换可编辑字段，正文为创建字段加 `revision`。slug/kind 必须与原值相同，其他遗漏字段会恢复默认/清空（enabled、permissions 未给出或为 null 时保留原值；permissions=[] 明确撤销全部），不要作为 PATCH 使用。
- 修改配置会撤销正在进行的采集结果；修改 URL 同时清除旧 ETag/Last-Modified。
- `POST /workspace/sources/{id}/key`：正文 `{"revision":1}`；返回新 source 和一次性的 access_key，旧 Key 立即失效。
- `POST /workspace/sources/{id}/refresh`：无请求体，立即采集，成功返回接入计数。最长约 25 秒；正在采集返回 409；不安全目标/网络/格式错误返回 502，并记录通用 last_error。
- 录入端 enabled=false 停止该 Key 的入库、上传及来源采集，不影响其他客户端读取已有线上快照；没有隐式级联删除接口。

读取端 `GET /sources` 仅返回 id、slug、name、description、category、language，不暴露上游 URL、状态错误或密钥设置。

### 查询客户端与身份迁移

查询客户端是独立 Client，不再是 manual Source 的权限组合：

```text
GET/POST /workspace/clients
GET/PUT  /workspace/clients/{id}
POST     /workspace/clients/{id}/key
GET/POST /workspace/identity-migration
```

创建请求为 `{"slug":"desktop-query","name":"桌面学习端","permissions":["read","sync"]}`，可带 description/enabled；返回 `{"data":{"client":{...},"access_key":"<仅本次返回>"}}`。权限可配置 read/sync/feedback，反馈必须显式授予，创建省略/null 默认两项，更新省略/null 保留、[] 撤销全部。PUT 和轮换需要最新 revision，客户端不能管理其他身份。

来源增加 integration（collector/skill/mcp/agent；RSS 固定 rss），仅作接入标记。来源不接受 read/sync，客户端不接受 ingest/upload/inbox。待拆分旧来源的普通编辑/轮换返回 409，应先预检并显式迁移，避免丢失旧 Key 归属。

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

`GET /workspace/intakes?limit=20&offset=0&source_id=<可选UUID>` 仅管理员可读，返回 items/total/limit/offset。limit 1–100、offset 0–10000，按接收时间倒序；不包含资料标题、正文或 Key，不伪造失败/历史批次。回执公开状态是提交时快照。

采集程序、Skill 与 MCP 均调用同一录入服务；MCP 的具体协议、文件授权和运行方式见 `mcp/wzstudy_ingest/README.md`。录入成功不等于已公开，也不会向学习客户端分发。

## 4. 审核、发布和下架

- `GET /workspace/items`：管理列表，内容包括 draft 和可选 published 快照。
- `GET /workspace/items/{id}`：完整管理记录。
- `POST /workspace/items/{id}/publish`，正文 `{"revision":1}`。
- `POST /workspace/items/{id}/unpublish`，正文 `{"revision":2}`。

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

服务端不主动向客户端推送资料；客户端可主动查询已发布资料、内容变更及基于当前身份的规则推荐。推荐查询本身不写入查看或推荐事件。

- 浏览候选：`GET /items?kind=video&category=人工智能&tag=AI&limit=20&offset=0`；各过滤条件取交集，可按实际需要省略。
- 阅读详情：`GET /items/{id}`，媒体地址由 `assets` 返回，媒体读取仍需 `read` 权限。
- 更新本地缓存：用 `sync` 权限定期调用 `/changes?after=<已提交游标>`，按 `has_more` 补齐后再按客户端策略等待下一次拉取。
- 学习端默认权限为 `read + sync`，学习反馈权限须显式授予。`GET /client/recommendations` 提供规则推荐；经 feedback 授权后，学习记录、复习、查看、进度及时长按客户端身份隔离。
- 示例 `examples/subscribe.py` 已改为 HTTP 主动轮询，SQLite 在同一事务中保存资料变更和游标；保留旧文件名以兼容已有运行方式。

旧的推送计划、收件箱、SSE 及专题管理路由已移除并返回 404。历史资料、附件和变更游标保留，不清空客户端缓存；旧工作空间的推送/专题记录不再读取或执行；遗留 `inbox` 授权被忽略且不会转换为阅读权限。旧查询或混合 Source 须显式迁入 Client 后才能用原 Key 查询。

## 8. 错误码

400 invalid_request/invalid_json；401 unauthorized；403 permission_denied/source_disabled/origin_denied；404 not_found；405 method_not_allowed；409 conflict/source_busy；413 payload_too_large；415 unsupported_media_type；500 internal_error；502 feed_fetch_failed；503 not_ready/polling_unavailable；504 timeout。

超时/断线不能证明服务端没有提交。接入依赖 external_id+内容哈希重试；管理更新先刷新 revision，不要盲目重放旧 revision。


## 查询客户端扫码身份校验

`GET /client/connection` 只接受独立查询客户端 Bearer Key，需要 read 或 sync 中至少一项。它不读取 Cookie、不接受管理员/全局只读/录入 Key，也不会轮换或创建身份。成功返回 `{data:{type:"wzstudy.query_client",version:1,client_id,name,permissions}}`，失败 401/403，不包含任何 Key 或哈希，禁止缓存。

管理端二维码直接携带 UTF-8 JSON 的 type/version/base_url/client_id/name/access_key（v1 严格六字段、最大 1200 字节）。扫码端确认目标并调用上述接口，核对身份后才保存；不将 Key 放在 URL，不转发到重定向地址。完整协议和标准库解析/注册示例见 `CLIENT_QR.md` 与 `examples/client_connection.py`。


## 数据源内的个人合集

个人合集与管理员发布的目录合集是不同数据；按独立查询 Client 身份隔离，读写都必须明确授予 `feedback`。多个设备共享一个查询 Key 时也共享此身份的个人合集。接口不会接受客户端指定其他 `client_id`：

- `GET /client/learning/collections?limit=20&offset=0` 分页查询自己的合集；
- `GET /client/learning/collections/{collection_id}` 查询详情，成员返回 `item_id` 和 `available`，已下架资料只保留不可用占位，不暴露草稿；
- `POST /client/learning/collections/import-legacy` 仅在用户明确核验历史归属后导入一个数据源内的成员子集；传入事件 ID、目标合集 ID、标题、有序成员及原创建／更新时间。服务器在同一事务确认全部成员已公开且目标 ID 未占用，不覆盖既有或已删除合集；相同事件及载荷可幂等重试。跨源成员须由客户端拆分，原合集、封面和其他来源成员不会自动删除。
- `POST /client/learning/collections/commands` 提交 `event_id`、`collection_id`、`action`、`expected_revision` 和动作相应字段。`action` 可为 `create/rename/add/remove/reorder/delete`；`create` 的版本为 0，其他命令必须携带当前合集版本。重排的 `order` 必须包含所有成员，包括不可用占位，避免误删隐藏成员。

客户端必须为事件生成稳定 UUID，在超时或结果未知时以完全相同的事件 ID 和正文重试。相同事件返回原回执并标记 duplicate；同一 ID 携带不同正文或版本冲突返回 409。添加成员只接受当前已发布资料；之后下架的成员仍可移除。删除合集不会删除原资料。需要同时授予 feedback 权限，断网时客户端不得转存本地冒充成功。

## 客户端反馈与去重

客户端可显式申请 feedback 权限，上报推荐/曝光、已读、喜欢、学习记录和收藏，按事件 ID 幂等保存。`POST /api/v1/client/feedback` 仅写本身份；`GET /api/v1/items?exclude=recommended,read` 或 `unseen=true` 可过滤旧候选，支持时间窗口和本页状态。管理员只读查看反馈或按身份模拟查询，不替用户上报。默认 read/sync 及所有旧 Key 不自动扩权。详见 `docs/CLIENT_FEEDBACK.md`（文档目录内为 `CLIENT_FEEDBACK.md`），示例为 `examples/feedback_client.py`。
