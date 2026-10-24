# 管理工作台与客户端推送 API · v0.3.0

基础路径：`/api/v1`。页面入口：`/admin/`。完整机器可读契约：`openapi.json`。

本文补充已有 `API.md` 的管理能力；原有来源接入、RSS、公开目录、changes 和 SSE 语义继续保留。

## 系统管理员初始化

参照 wenzflow 的启动引导规则：首次启动配置 `SYSTEM_ADMIN_EMAIL`、`SYSTEM_ADMIN_PASSWORD`，可选 `SYSTEM_ADMIN_DISPLAY_NAME`；兼容 `BOOTSTRAP_ADMIN_EMAIL/PASSWORD`。密码为 8–128 UTF-8 字节，不去除首尾空白。只持久保存带随机盐的 Argon2id 哈希（64 MiB、3 次、并行度 1），不保存明文密码。

管理员在监听 HTTP 前创建。已有管理员时，同邮箱启动不重置密码；未配置初始化变量也可正常重启；不同邮箱被拒绝。没有匿名注册、提权或初始化 HTTP 接口。修改环境变量不是重置已存在密码的方法。

本地可运行根 `start-local.ps1 -InitializeAdmin`，在本机终端输入邮箱并隐藏输入密码；该命令初始化并启动。真实密码无需发给助手。初始化完成后正常运行 `start-local.ps1` 即可。环境配置、数据保护和回退规则见同目录 `ADMIN_AUTH.md`。

## 认证与会话

- `POST /auth/login`：JSON `{ "email": "<管理员邮箱>", "password": "<管理员密码>" }`。
- `GET /auth/session`：读取会话、版本、存储模式与 `max_upload_bytes`。
- `POST /auth/logout`：撤销当前会话。
- 管理 API 可使用 `Authorization: Bearer <ADMIN_API_KEY>`，或浏览器 HttpOnly 会话。
- 会话 Cookie 为 `wzstudy_admin`，`SameSite=Strict`，有效期 8 小时；生产环境强制 `Secure`。会话不持久保存，服务重启后失效。
- Cookie 认证的写请求必须带受信任 Origin；同源自动允许，跨源按 `CORS_ORIGINS` 精确匹配。
- **管理密钥不能代替来源密钥入库；来源密钥不能管理工作空间；只读密钥不能访问草稿。**
- 邮箱不存在和密码错误统一返回 401；登录请求限制 4 KiB，按真实连接 IP 每分钟最多 10 次，最多同时执行 2 次密码哈希。超过限制返回 429 与 Retry-After，不信任未经配置的转发 IP 头。
- 成功为 `{ "data": ... }`，失败为 `{ "error": { "code": "...", "message": "..." } }`。任何业务密钥都不支持 URL 查询参数传递。

## 工作空间与资料列表

`GET /admin/workspace` 返回：

- `sources`：真实来源，内部工作台来源标记为 `system=true`。
- `summary`：`total/pending/draft/published/week_new`，按类型、分类、来源的数量，近 7 天 UTC 入库趋势，已登记附件大小与数量。
- `collections`、`plans`、`settings`、`recent`。

`GET /admin/items` 增加参数：

| 参数 | 说明 |
| --- | --- |
| `kind` | `article/audio/video/carousel`；未传则全部 |
| `status` | `draft/pending/published`；未传则全部 |
| `sort` | `asc/desc`，默认最近更新优先 |
| `q` | 标题、摘要、作者、分类、标签关键词 |
| `category` / `tag` / `source_id` | 继续沿用现有过滤参数 |
| `limit` / `offset` | 每页 1–100，offset 为 0–10000 |

返回 `{ "items": [...], "total": N, "limit": N, "offset": N }`。列表中的正文被省略为空字符串；使用 `GET /admin/items/{id}` 取得完整正文和操作记录。统计只包含未删除资料。

## 多格式资料模型

`Content.format` 仍是 `text/markdown`，不被媒体类型替代，以兼容原有客户端。

新增字段：

```json
{
  "kind": "audio",
  "assets": [
    {
      "id": "<上传后返回的 UUID>",
      "name": "lesson.wav",
      "mime": "audio/wav",
      "size": 32044,
      "source_id": "<来源 UUID>",
      "url": "/api/v1/media/<asset UUID>",
      "created_at": "2026-10-23T12:00:00Z"
    }
  ]
}
```

- 老资料不带 `kind` 时按 `article` 解释，不改变老请求的默认正文格式。
- 资产按注册表重新解析；伪造的 URL、文件名、MIME、大小不会被当作可信元数据。
- 客户端 JSON 入库只能引用同一来源已登记的资产；管理员可管理整个空间的资产。
- 音视频最多一个文件；轮播最多 20 个图片。外部链接资料可以只提供 `url`，不要求上传文件。
- 文件名仅作显示与下载建议，存储路径由服务端生成的 UUID 决定。

## 创建、编辑、审核和删除

### 管理员创建 / 编辑

```text
POST /admin/items
PUT  /admin/items/{id}
```

请求：

```json
{
  "revision": 0,
  "status": "pending",
  "content": {
    "title": "如何建立有效的学习系统",
    "kind": "article",
    "format": "markdown",
    "content": "# 从问题开始\n\n让知识走向行动。",
    "summary": "一套可持续的学习路径",
    "category": "学习方法",
    "author": "管理员",
    "tags": ["学习方法"]
  }
}
```

创建时 `revision=0`。`source_id` 为空时，服务自动使用受保护的内部工作台来源；`external_id` 自动生成。编辑必须传资料最新的 `revision`，来源和外部标识保持不变。

保存仅允许 `draft` 或 `pending`；不能通过保存接口直接声明已经发布。

### 状态流转

```text
draft --POST /items/{id}/submit--> pending
pending --POST /items/{id}/publish--> published
published --编辑--> pending（旧公开快照保留）
published --POST /items/{id}/unpublish--> draft
```

以上操作的完整路径均带 `/admin`，请求体为 `{ "revision": N }`。

- 草稿必须先提交审核，再发布。
- 编辑已发布内容时，客户端仍读取独立的旧 `published` 快照，直至再次审核通过。
- 重复发布相同公开内容不重复生成内容变更事件。
- `POST /admin/items/{id}/delete` 使用修订号软删除；曾发布的资料会写入 `delete` 墓碑事件。
- 删除后的外部标识不允许被普通重复入库悄悄复活；客户端需使用新的 `external_id`。
- 旧修订号返回 `409 conflict`，不会静默覆盖别人修改。请刷新并人工确认，而不是自动重试发布。

## 文件上传

```text
POST /admin/uploads
POST /ingest/{source_id}/files
```

两个接口均使用 `multipart/form-data`：

- `metadata`：JSON 字符串。管理上传对应上面的管理请求；客户端上传对应单个 `Content`。
- `files`：一个或多个文件字段。

客户端示例（替换来源 ID 和本机环境变量）：

```sh
curl -X POST "http://127.0.0.1:8091/api/v1/ingest/$SOURCE_ID/files" \
  -H "Authorization: Bearer $SOURCE_KEY" \
  -F 'metadata={"external_id":"lesson-audio-001","title":"每日听力","kind":"audio","category":"语言学习"}' \
  -F 'files=@./lesson.wav'
```

- 来源专属 Bearer 密钥只用于该来源；管理员不能以管理密钥调用客户端入库。
- 建议提供稳定 `external_id`，以便查询/重复入库时识别资料，避免网络超时后盲目创建重复内容。
- 合计文件大小受 `UPLOAD_MAX_MB` 控制，默认 200 MiB；轮播最多 20 张。
- 文本文件必须为 UTF-8，正文不超过 262144 字节。文件导入的 Markdown 会设置 `format=markdown`。
- 拒绝 HTML、SVG、类型不匹配、空文件和超限内容。验证失败会回滚元数据并清理已生成文件。
- 若数据库提交结果不确定，会保留不可变文件，以免删除实际上已提交的资产；遇到超时/存储错误，请先查询当前状态再决定重试。
- 原文件不转码，支持的容器不等于所有编码均可在所有浏览器播放。

### 媒体读取

`GET /media/{asset_id}` 支持 Range。浏览器管理会话可以预览私有资产；公开客户端应使用只读 Bearer 密钥，且资产必须仍被某份当前公开快照引用。

下架/删除后，旧收件箱或历史 changes 中可能仍有此前公开的引用，但媒体访问会返回 404。客户端应同时处理下架事件与不可用资源。

## 来源与 RSS

来源创建、编辑、轮换密钥和采集继续使用已有接口：

```text
POST /admin/sources
PUT  /admin/sources/{id}
POST /admin/sources/{id}/key
POST /admin/sources/{id}/refresh
```

- `kind=manual` 代表客户端/API 接入，`kind=rss` 代表公开 RSS/Atom。
- 新来源默认人工审核；显式设置 `auto_publish=true` 的来源保留已有自动发布政策。
- 密钥只在创建/轮换返回一次；后续查询不返回原文或哈希。轮换立即使旧密钥失效。
- 内部工作台来源不能停用、轮换密钥或作为客户端接收端。
- 停用来源禁止入库、采集与该来源收件箱访问，但不会自动下架它已经公开的内容。
- RSS 复用安全抓取器：拒绝内网/环回/保留地址，校验 DNS 并将连接固定到已校验 IP，禁用环境代理，限制跳转、端口、超时与响应大小。
- 普通互联网链接登记只保存出处，不等于 RSS 采集，也不承诺绕过网站认证提取正文。

## 专题与空间设置

```text
POST /admin/collections
PUT  /admin/collections/{id}
POST /admin/collections/{id}/delete
PUT  /admin/settings
```

专题字段为 `name/category/description/color/revision`，色系为 `green/purple/blue`。按分类自动归集资料，不复制媒体。删除合集不删除原资料。

空间设置为 `{ "name": "管理员", "compact": false, "revision": N }`。所有修改均使用修订号；首版为共享单管理员工作空间，不提供多用户权限模型。

## 定时推送与接收端收件箱

### 创建

`POST /admin/plans`：

```json
{
  "title": "下班后的二十分钟",
  "item_ids": ["<已发布资料 UUID>"],
  "target_ids": ["<已启用客户端来源 UUID>"],
  "scheduled_at": "2026-10-24T10:00:00Z"
}
```

示例时间仅说明格式，实际必须传未来两年以内的时间。`item_ids` 最多 50 个且不能重复；空 `target_ids` 表示执行时所有已启用的非内部客户端来源。

### 操作

`POST /admin/plans/{id}/action`：

```json
{ "revision": 1, "action": "pause" }
```

支持 `pause/resume/run/delete`。已完成计划不可重复执行；立即执行支持已暂停/失败计划，但仍重新检查已发布资料与接收端。

服务每 2 秒检查到期计划。计划、增量事件和投递记录在同一存储事务提交；PostgreSQL 通过原有 feed clock 串行化并发写入，多个调度器不会重复执行同一个已完成计划。文件存储只适用于单进程。

执行使用 **当前已经批准的 `published` 快照**，从不使用未审核草稿。接收端停用、资料下架/删除等业务失败会记为 `failed`，不会悄悄部分投递或无限自动重试。

### 读取

```text
GET /clients/{source_id}/inbox?after=0&limit=100
Authorization: Bearer <此来源的专属密钥>
```

返回：

```json
{
  "data": {
    "deliveries": [],
    "next_cursor": 12,
    "high_watermark": 12,
    "has_more": false
  }
}
```

每份 delivery 包括 `cursor/plan_id/plan_title/target_ids/item/created_at`。`item` 是已审核的公开快照。

- 接收范围由服务端校验，不能拿其他来源的密钥读取某个接入端的收件箱。
- 游标窗口与 changes 共用全局提交顺序。没有匹配投递时也可能推进游标；始终保存 `next_cursor`，在 `has_more=true` 时继续补拉。
- 推送会产生 upsert 同步事件，即使资料公开修订号未变；客户端应按**游标**发现新投递，而不是只用 item revision 判断是否拉取收件箱。
- `done` / `deliveries` 表示持久投递记录已写入，数量为资料数乘以接收端数，不代表通知已经显示、设备在线或用户已读。
- 本版本不集成 APNs/FCM、短信、邮件或终端应用。可通过 SSE 的新游标触发收件箱拉取，或定期轮询收件箱。

## 存储、备份与部署边界

- `STORE=file`：开发环境单进程，快照以同目录临时文件 + Sync + Rename 保存；元数据事务成功后才对其他请求可见。
- `STORE=postgres`：新增 `000002_management_workspace.sql`，保存资产、专题、计划、投递和设置。旧资料结构继续通过 JSONB 扩展，不改已应用迁移。
- `DATA_DIR/uploads` 必须持久化并与数据库一同备份；多实例需要共享附件卷，浏览器管理会话还需要粘性路由。首版建议单实例运行，Bearer API 不依赖进程内会话。
- 消费多媒体和收件箱的客户端需适配新增字段；老文章请求的路径、正文格式和入库方式保持可用。
- 上传附件暂不自动回收；删除保留备份数据。生产环境需自行管理磁盘与备份策略。
- 生产禁止 file/memory，需 PostgreSQL 和 HTTPS。启动及迁移说明见 同目录 `RUNNING.md`。
