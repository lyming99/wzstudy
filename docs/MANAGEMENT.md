# 数据录入与数据查询：管理 API

基础路径 `/api/v1`，管理页面 `/admin/`，机器契约 `/openapi.json`。录入来源提交资料，查询客户端获取已发布资料；服务端提供规则推荐，并在显式授权后按客户端身份保存和查询学习反馈、复习、记录及进度。

## 认证与安全边界

- 管理员使用邮箱/密码建立 HttpOnly 会话，或使用 `ADMIN_API_KEY` 自动化管理。初始化、Argon2id 密码哈希及启动规则见 `ADMIN_AUTH.md`。
- Cookie 为 `wzstudy_admin`、SameSite=Strict，最长 8 小时；生产要求 HTTPS，写请求必须来自受信任 Origin。重启后需重新登录。
- **录入端 Source** 使用专用 `wzsk_` Key，仅配置 `ingest/upload`。
- **查询客户端 Client** 独立存储、创建、启停及轮换，新 Key 前缀 `wzck_`，配置 `read/sync` 及显式授予的 `feedback`。迁入的旧查询 Key 可保留原前缀；鉴权依据实际独立记录而不是前缀。
- `READ_API_KEY` 仅作旧版全局只读兼容；新接入不需要分发它。管理员、全局只读、查询客户端均不能代替录入端入库。
- 服务端仅持久保存 Key 哈希。完整 Key 只在创建、轮换或显式迁移生成时返回一次；列表、工作空间与后续查询不返回 Key 或哈希。
- 客户端不能访问 `/admin/*`、管理其他身份或调用审核接口。筛选条件不是租户隔离权限；`read/sync` 覆盖工作空间的已发布内容。

成功响应为 `{ "data": ... }`，失败为 `{ "error": { "code": "...", "message": "..." } }`。JSON 请求拒绝未知字段及无效 UTF-8，不支持 URL 中携带 Key。

## 录入端管理

```text
GET  /admin/sources
POST /admin/sources
GET  /admin/sources/{id}
PUT  /admin/sources/{id}
POST /admin/sources/{id}/key
POST /admin/sources/{id}/refresh
```

创建 API/Skill/MCP 录入端示例：

```json
{
  "slug": "course-collector",
  "name": "课程采集程序",
  "kind": "manual",
  "integration": "collector",
  "enabled": true,
  "auto_publish": false,
  "permissions": ["ingest", "upload"],
  "category": "人工智能",
  "language": "zh-CN"
}
```

- `kind=manual` 表示外部统一 API 接入，`integration=collector/skill/mcp` 只是来源标记，不赋予额外权限。新建省略时默认 collector；更新时省略保留原值。
- `kind=rss` 需要公开 HTTP(S) `feed_url`，integration 固定为 rss；不能配置权限数组。RSS 归入数据录入，不是客户端推送任务。
- manual 的权限只接受 `ingest/upload`。创建时省略/null 沿用旧入库/上传默认；更新时省略/null 保留原策略；显式 `[]` 撤销全部。不能授予 `read/sync/inbox`。
- 上传必须同时有 `ingest` 和 `upload`。来源启用状态和权限在写事务内复核。
- 默认 `auto_publish=false`；已有来源保持其发布策略。停用来源不会自动下架存量资料。
- `PUT` 是完整可编辑配置替换，不是 PATCH；需保留其他字段并提交最新 revision。slug/kind 不可改。
- 轮换正文为 `{ "revision": N }`，返回 `{ "source": ..., "access_key": ... }`，旧 Key 立即失效。
- 待拆分的旧查询/混合来源不能普通编辑或轮换，应先完成下文的身份迁移，防止丢弃旧查询归属。
- RSS 抓取继续拒绝内网/回环/保留地址，校验 DNS 和跳转；轮询取决于 POLL_ENABLED，立即采集只对启用 RSS 有效。

## 独立查询客户端管理

```text
GET  /admin/clients
POST /admin/clients
GET  /admin/clients/{id}
PUT  /admin/clients/{id}
POST /admin/clients/{id}/key
```

创建示例：

```json
{"slug":"desktop-reader","name":"桌面学习客户端","enabled":true,"permissions":["read","sync"]}
```

创建返回 201：`{ "data": { "client": { ... }, "access_key": "<仅本次显示>" } }`。

- 权限接受 read/sync/feedback，其中反馈仅写自己的学习事件，不允许资料入库或管理。创建省略/null 默认 read+sync；更新省略/null 保留，`[]` 撤销全部。
- PUT 包含 slug/name/description/enabled/permissions 和最新 revision；slug 不可改。
- 轮换使用最新 revision，返回新的 client 与一次性 access_key。停用、轮换只影响该查询身份，不影响录入端。
- 启用不代表设备在线；系统不维护学习进度或在线状态。

## 数据源合集与章节

管理员 API（与历史已退役的 `/admin/collections` 分开）：

```text
GET  /admin/catalog/collections?limit=20&offset=0
POST /admin/catalog/collections
GET  /admin/catalog/collections/{id}
PUT  /admin/catalog/collections/{id}
POST /admin/catalog/collections/{id}/publish
POST /admin/catalog/collections/{id}/unpublish
POST /admin/catalog/collections/{id}/delete
```

创建时 revision 为 0；更新、发布、下架、删除必须带最新 revision。保存请求示例：

```json
{"revision":0,"title":"入门合集","summary":"课程简介","chapters":[{"id":"","title":"第一章","item_id":"<已存在的内容 UUID>"}]}
```

章节按数组顺序排列，每章关联一个内容；新增章节可传空 id，由服务端生成稳定 UUID。一个合集最多 100 章，同一内容和章节 id 在同一合集中不可重复。草稿可以引用已存在但尚未发布的内容；发布时所有章节必须已公开。编辑只修改草稿，已发布快照保持不变，直到再次发布。删除合集不删除内容；内容下架时自动生成独立的合集变更，公开查询不再暴露该章节。历史 `collection` 记录不会被此接口改写。

## 学习路线图（思维导图）

管理端左侧“学习路线图”（`/admin/#roadmaps`）是独立编排模块。支持新建、名称/简介搜索、分页、编辑、删除；画布支持添加子节点/同级节点、拖动分支或选择上级、同级排序、折叠、缩放平移、撤销重做、节点说明和关联资料。显式保存到服务端，未保存离开会提示，版本冲突不会静默覆盖。

```text
GET  /admin/roadmaps?q=<关键词>&limit=20&offset=0
POST /admin/roadmaps
GET  /admin/roadmaps/{id}
PUT  /admin/roadmaps/{id}
POST /admin/roadmaps/{id}/delete
```

仅管理员可读写，不开放给查询客户端，不修改个人学习进度或现有公开资料游标。节点使用唯一 UUID 和 parent_id 表示树，数组顺序决定同级顺序，唯一根节点标题与路线图名称一致。最多 300 节点、12 层、每节点 20 份资料引用；保存/删除携带当前 revision，409 时保留本地草稿。删除只移除路线图或关联，不删除资料。字段、使用步骤、键盘操作与部署注意事项见 `LEARNING_ROADMAPS.md`，完整契约见 `/openapi.json`。

## 工作空间与资料维护

`GET /admin/workspace` 返回 sources、clients、identity_migration、summary、settings、recent，不再包含 plans/collections。summary 只作为链路辅助状态，不是独立数据洞察业务。

```text
GET  /admin/items
GET  /admin/items/{id}
POST /admin/items
PUT  /admin/items/{id}
POST /admin/items/{id}/submit
POST /admin/items/{id}/publish
POST /admin/items/{id}/unpublish
POST /admin/items/{id}/delete
POST /admin/uploads
PUT  /admin/settings
```

管理员列表支持 kind、status、source_id、category、tag、q、sort、limit、offset，返回 items/total/limit/offset，列表正文省略；详情返回完整资料。来源归属及 external_id 在编辑中保持不变。

手动保存结构：

```json
{"revision":0,"status":"pending","content":{"kind":"article","title":"学习资料","format":"markdown","content":"# 正文","category":"学习方法","tags":["学习方法"]}}
```

- 保存状态为 draft 或 pending；草稿先 submit，再 publish。发布/下架/提交/删除均携带最新 revision。
- 编辑已公开资料时，旧 published 快照继续供客户端查询，审核通过后才更新；重复发布相同快照不重复生成变更。
- 下架或软删除写入 delete 墓碑。删除不清理附件；普通重复入库不会悄悄复活已删除的 external_id。
- 文件上传使用 multipart：metadata 为上述管理输入，files 为一个音视频/文章或最多 20 张轮播。默认总量 200 MiB，支持配置上限；文本须 UTF-8，正文最多 262144 字节。
- 原文件不转码；拒绝 HTML/SVG、类型不匹配、空文件和超限。校验失败清理未提交文件，提交结果不确定时保留不可变文件，不盲目重传。
- 浏览器管理员可查看私有附件；公开媒体只能读取仍被当前公开快照引用的资产，支持 Range。历史变更中的旧地址不代表下架后仍可访问。
- `/admin/settings` 保存管理员显示名称、compact 与 revision，不增加新的业务模块。

## 统一录入与回执

```text
GET  /ingest/{source_id}/status
POST /ingest/{source_id}
POST /ingest/{source_id}/files
GET  /admin/intakes?limit=20&offset=0&source_id=<可选UUID>
```

采集程序、Skill、MCP 复用同一认证、校验、来源绑定、幂等更新和附件登记流程。请求/字段约束见 `API.md`；MCP 说明见项目 `mcp/wzstudy_ingest/README.md`。

- status 只检查该来源自己的入库能力，不写资料/来源/游标，不要求查询或上传权限。
- JSON 单批 1–100 条、最多 4 MiB，整批提交或失败；多批请求不是一个大事务。
- `source_id + external_id` 幂等更新，内容相同不增加资料 revision。每次成功提交可以有独立回执，包括未变更批次。
- 成功新增 `receipt_id/source_id/received_at`，保留 created/updated/unchanged/published/items。每项资料含真实 ID、revision 和状态。
- 回执与资料在同一事务保存，只记录已提交批次，不伪造失败或历史记录；不保存资料标题、正文或 Key。
- `/admin/intakes` 支持来源筛选、limit 1–100、offset 0–10000，返回 items/total/limit/offset。公开状态是提交时快照，之后可发生审核变化。
- published 是本批发布计数，不是整个库的公开数量。录入成功不等于已公开，也不向学习客户端主动分发。

## 客户端主动查询

```text
GET /sources
GET /items?kind=video&category=<分类>&tag=<标签>&q=<关键词>&limit=20&offset=0
GET /items/{id}
GET /media/{id}
GET /changes?after=0&limit=100
```

read 读取公开目录、详情及媒体；sync 读取公开变更（包含公开正文），两者不隐式互授。列表分页不是冻结快照；可靠缓存从 changes 的游标 0 回放，按 item_id 应用 upsert/delete，与 next_cursor 原子提交，空过滤页也要保存游标，has_more=true 继续补拉。

管理端查询预览使用公开接口和管理员会话，只展示公开结果；它不等于替某个客户端验证实际 Key 的权限。客户端鉴权由独立集成测试与客户端自身请求验证。

## 旧身份显式迁移

```text
GET  /admin/identity-migration
POST /admin/identity-migration
```

预检只读返回 entries/pending/mixed，不返回哈希/Key，不写文件或改内容游标。旧 nil 来源仍是入库/上传，`[]` 与仅 inbox 不自动获得查询权。

执行前备份工作区/数据库及附件。对每一个混合身份提交：

```json
{"choices":[{"source_id":"<旧来源 UUID>","revision":1,"keep_key":"ingest"}]}
```

- keep_key 必须明确为 ingest 或 query，所有混合身份都要选择；缺失、过期 revision 或已失效选择返回 409，整批回滚。
- 纯查询身份将旧 Key 哈希迁入 Client，清空 Source 认证能力；保留旧 source_id 作为资料出处。
- 混合身份的旧 Key 只保留给选定一侧，另一侧生成新 Key，仅本次返回。未将一个 Key 复制给两个角色。
- 保留停用状态、原查询/写入权限子集；不调用 SaveItem 或 Append，资料修订号、附件和内容游标不改变。
- 返回 converted、clients、new_keys；重复执行无待迁移身份时 converted=0。失败不返回部分新 Key；提交结果不确定时先重新预检，不盲目重放。
- 启动、GET 和前端页面加载不会自动执行迁移；生产前应规划维护窗口与客户端切换。

## 存储与退役模块

- 文件存储继续使用版本 1 的通用 records；新增 client/intake 独立种类，不重写旧资料。
- PostgreSQL 必须追加执行 `000003_input_query_identities.sql`，扩展记录种类并新增客户端哈希唯一索引及回执索引；不改历史迁移。缺少新结构时 readiness 不通过。
- 历史 collection/plan/delivery 记录保留但不再提供业务入口；原推送、收件箱、SSE 及专题管理路由返回 404。
- 迁移身份后的回退需要成套恢复迁移前备份，不能只换回旧可执行文件而假定旧 Key 归属已还原。
- 附件持久化与数据库备份须保持同一时间点；生产只用 PostgreSQL、HTTPS 和共享附件卷。管理员会话仍需单实例或粘性路由。


## 客户端二维码接入

“查询客户端”列表提供扫码入口；新建或显式轮换成功的一次性 Key 弹窗也可生成。二维码在浏览器本地编码，不调用外部服务。历史 Key 不能回读：已有客户端可手动粘贴已保存的 Key，或明确确认轮换。打开扫码弹窗、生成图片或校验都不修改凭据。

载荷为最大 1200 UTF-8 字节的 JSON，严格六字段：`type="wzstudy.query_client"`、整数 `version=1`、`base_url`（HTTP(S) 服务 origin）、UUID `client_id`、`name`、`access_key`。不要把 Key 放进 URL，二维码持有者等同于持有 Key。

新增 `GET /api/v1/client/connection`，仅使用 `Authorization: Bearer <查询 Key>` 验证真实身份；需要 read 或 sync，拒绝管理员 Cookie/Key、旧全局 Key、来源 Key，不回退其他角色。返回 `{data:{type,version,client_id,name,permissions}}`，不含 Key/哈希/备注，禁止缓存。

扫码客户端应先确认目标地址，再调用该接口核对 client_id、类型、版本及实际权限，然后安全保存连接。普通相机只显示 JSON 不等于已经添加；实际客户端需接入协议。标准库导入模块为 `examples/client_connection.py`，完整说明见 `docs/CLIENT_QR.md`。

手机不可直接使用管理员电脑的 127.0.0.1 地址；填写可达地址不会修改服务监听。局域网/防火墙/HTTPS 需按部署需求显式配置。二维码无独立失效期，停用、撤权或轮换后失效；下载/截图副本需用户自行保护。


## 客户端反馈与去重

客户端可显式申请 feedback 权限，上报推荐/曝光、已读、喜欢、学习记录和收藏，按事件 ID 幂等保存。`POST /api/v1/client/feedback` 仅写本身份；`GET /api/v1/items?exclude=recommended,read` 或 `unseen=true` 可过滤旧候选，支持时间窗口和本页状态。管理员只读查看反馈或按身份模拟查询，不替用户上报。默认 read/sync 及所有旧 Key 不自动扩权。详见 `docs/CLIENT_FEEDBACK.md`（文档目录内为 `CLIENT_FEEDBACK.md`），示例为 `examples/feedback_client.py`。
