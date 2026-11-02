# WzStudy API 参考

本文件配合 `../SKILL.md` 使用。所有示例都是**接口结构示例，不是执行请求**；尖括号值必须替换为已授权环境中的真实 ID/凭据，`attempt/revision` 必须从最新响应获取。不要直接向真实服务批量提交示例。

## 1. 来源、版本与可信顺序

核对日期：2026-11-01。依据当前工作区的 `internal/httpapi/server.go`、鉴权/处理器、`internal/study` 服务、`agent` 工具注册及 `docs/openapi.json`，不依赖旧对话中的部署状态。

- `openapi.json` 是本 Skill 附带的机器可读参考，保留仓库定义的完整路径和 schema，并记录少量已通过源码核对的修正；`x-wzstudy-skill-reference` 说明来源 SHA-256 和修正范围。它不是运行服务的版本证明，也不替代服务端校验。
- 当前实例的运行时定义可在服务根路径 `GET /openapi.json` 读取；它由二进制嵌入，可能早于工作区。核对版本不需要发送 Key，也不要把用户凭据发往陌生地址或跳转目标。
- 发生冲突时，以目标实例的真实行为及对应版本源码为准；本文中已明确核对的差异不要回退照抄旧文档。字段、状态码或权限不确定时停止相关写入并报告。
- 本 Skill 不安装 HTTP 工具、不自动登录、不调用 API、不注册技能、不改变 Agent 的权限或运行配置。

### 已核对的历史差异

1. 当前推送入库服务在新增/实际更新后直接发布；旧 `auto_publish=false` 和“默认待审核”说明不再代表推送链路。相同内容 unchanged、重复 skipped、手动草稿及下架资料须分别判断。
2. `TaskBudget.max_rounds` 服务端默认 100、最大 1000，不是旧 OpenAPI 中的最大 32。`max_tool_calls` 默认 200、最大 1000，按单个任务的一次执行计数；超时上限来自服务端策略。
3. 独立 Client 的 `feedback` 权限可维护其自己的学习状态；不与所有者的用户学习身份合并。不是所有 `learning` 路径都要求 feedback，模式普通查询/统计与能力探测还各有 read/sync 条件。
4. Agent 已有媒体上传、候选查重、可选字幕及采集计划；不能照抄旧文档中的“不支持媒体/没有定时任务”。但模型仍没有任意 HTTP、shell 或通用读文件工具。
5. `/workspace/*` 只接受本人用户会话；`/admin/agent/*` 是管理员管理入口，不是查询全站私人资料的通道。当前管理员鉴权还兼容管理 API Key 和旧管理员 Cookie；新登录签发 `wzstudy_user`，不要依赖旧 Cookie 申请新会话。
6. 目录合集保存即生效，旧合集 publish/unpublish 路径保留但返回 410，不能继续调用来“发布合集”。

## 2. URL、响应与认证

约定 `ORIGIN=http://127.0.0.1:8091`，`API_ROOT=ORIGIN+/api/v1`。生产非本机访问使用 HTTPS。除另行注明外，下文路径和完整索引均相对 `API_ROOT`。

```http
GET /api/v1/health/ready HTTP/1.1
Host: 127.0.0.1:8091
```

匿名健康：`GET /health/live` → `data.status=ok`；`GET /health/ready` → `data.status=ready`，存储未就绪为 503。`GET ORIGIN/openapi.json` 是根路径。

普通成功为 `{"data":...}`，普通失败为 `{"error":{"code":"...","message":"..."}}`；请求 ID 在 `X-Request-ID`。204 无正文，不要执行 JSON 解码；媒体/程序/部署 ZIP 成功时为二进制，不是 JSON。

JSON 写请求带 `Content-Type: application/json`；默认最大 4 MiB，但各接口可能另设更小/更大上限。拒绝未知字段、多段 JSON、无效 UTF-8、空正文或 null。要求空对象的接口应传 `{}`；不要因某些 POST 无业务字段就一律不传正文。multipart 不手工设置不匹配的 boundary。

### 认证速查

| 标签 | 认证方式 | 条件 |
|---|---|---|
| 无 | 不带凭据 | 健康、文档和登录入口 |
| Agent | `Authorization: Bearer <Agent Key>` | task.claim；入库/上传还需 ingest，且只作用于任务绑定来源 |
| Source | `Authorization: Bearer <来源 Key>` | 来源 ID 必须匹配、所有者启用、来源启用；ingest，文件额外 upload |
| Client-read | 独立查询 Key，或用户会话 | 仅所属工作区已发布内容 |
| Client-sync | 独立查询 Key，或用户会话 | sync 不隐式授予 read；游标独立 |
| 学习 | 用户会话，或独立 Client feedback | 记录按真实身份隔离；能力探测还需 read 或 sync，推荐还需 read |
| User | `wzus_` Bearer 或 `wzstudy_user` Cookie | 本人工作区；Cookie 写入同源 Origin，不能用管理/来源/Agent Key 代替 |
| Admin | 管理员用户会话，或管理员鉴权支持的管理 Key/兼容会话 | 系统用户和管理员 Agent 运维；不扩大资料归属 |
| Trigger | `Authorization: Bearer <计划专用 Key>` | 只能触发预配置计划 |

显式 Authorization 优先于 Cookie，错误 Key 不会借 Cookie 提权；可选 `X-WzStudy-User` 必须匹配当前用户。不要在 query/body 指定其他用户身份。

### 用户会话（仅明确授权的登录流程）

```http
POST /api/v1/user/auth/login HTTP/1.1
Content-Type: application/json

{"email":"<用户邮箱>","password":"<由安全输入提供>","session_mode":"token"}
```

成功读取 `data.access_token/token_type/expires_at/user_id/role`；安全保存而不回显。`POST /auth/login` 是统一浏览器登录，只有 email/password，不能给它塞 session_mode。令牌探测用 `GET /user/auth/session`，退出用 `POST /user/auth/logout`。改密 `/user/auth/password` 会撤销旧会话，不能作为无副作用自检。

## 3. Agent 运行时协议

**系统模型用已有工具，下面 HTTP 由宿主管理**。Agent Key 不能直接调用 `/ingest/{source_id}`，Source Key 也不能调用 `/agent/*`。任务 ID、来源、attempt 由领取结果确定，不接受模型自选来源。

### 3.1 状态与领取

- `GET /agent/status`：`data.agent/server_time/lease_seconds`。
- `POST /agent/tasks/claim`：`{"capacity":1,"wait_seconds":0,"version":"<版本>","hostname":"<设备别名>"}`。capacity 范围 0–4，wait_seconds 0–25；一般用 1。成功 200 为 `data.task`，无任务 204。
- 任务携带 `id/source_id/constraints/budget/attempts/lease_expires_at/deadline/cancel_requested`，还可能有 `plan_id/plan_run_id/queue_expires_at`。
- 不盲目重试 claim；断线可能已分配任务。不要并发领取多于宿主实际可执行的任务。

### 3.2 心跳、事件、查重

```http
POST /api/v1/agent/tasks/<任务UUID>/heartbeat HTTP/1.1
Authorization: Bearer <由宿主注入>
Content-Type: application/json

{}
```

心跳不带 attempt；响应有 `lease_expires_at/cancel_requested/cancel_reason/status`。应按服务端租约和运行时策略续租，收到取消或租约冲突立即停止新副作用。

事件正文：

```json
{"attempt":1,"events":[{"seq":1,"kind":"notice","text":"已确认来源，开始整理"}]}
```

- `attempt` 取 task.attempts，不是永久常量 1。事件最多 200 条/批、HTTP 正文 ≤1 MiB，seq 1..100000；事件文本/总量受服务端策略限制。
- kind 为 `round_start/assistant/tool_call/tool_result/notice/finish`，可带 `tool/summary/at`。不记录秘密、无关私有内容或隐藏推理。
- 事件身份是 task+attempt+seq，响应 `accepted/highest_seq`。不要分配新 seq 重复报告同一次事件或改变旧事件含义。

候选查重：

```json
{"attempt":1,"items":[{"external_id":"article:stable-id","url":"https://example.org/a"}]}
```

提交到 `POST /agent/tasks/{id}/duplicates`，每批 1–20，候选至少有 external_id 或 url；响应 `data.items` 为 `external_id/exists`，不暴露正文。默认 skip 的匹配包含同工作区已有资料与删除保护，不是全站搜索。音视频应在下载前查重。

### 3.3 资产与任务入库

- `POST /agent/uploads`：multipart 重复字段名为 `files`，但本接口**恰好一个文件**。成功 201 的 `data` 是一个 Asset 对象，包含真实 ID 等登记信息；上传本身未完成条目入库。
- `POST /agent/tasks/{id}/items`：`{"attempt":1,"items":[Content,...]}`，每批 1–20、HTTP 正文 ≤8 MiB。还受任务剩余条目、域名、数据类型、租约、取消状态约束。
- 音视频条目引用上传返回的资产，例如 `assets:[{"id":"<资产UUID>"}]`。不能把来源页面 URL 当成媒体资产；不能构造 MIME/大小蒙混服务端。
- 回执包含 receipt_id/source_id/received_at、created/updated/unchanged/skipped/published/items 等实际结果。默认 duplicate_policy=skip，全部重复可能成功但 created=0/skipped>0。
- task.content_type=video/audio 时，必须有相应真实媒体资产，错误为 `content_type_mismatch`。工具层 `ref` 不属于 REST 字段，字幕必须先解析为真实正文。

### 3.4 完成与归还

```json
{"attempt":1,"status":"succeeded","summary":"按真实回执说明结果","stats":{"rounds":3,"tool_calls":2}}
```

发往 `POST /agent/tasks/{id}/complete`，成功 204；终态仅 succeeded/failed/cancelled，可带结构化 error。不要用完成请求冒充回滚；已入库的内容不撤回。

优雅退出可发 `POST /agent/tasks/{id}/release`，正文 `{"attempt":1,"reason":"进程退出"}`，成功 204；归还租约不代表成功。`task_claimed/lease_expired/task_cancelled` 时不能继续写当前任务。已有运行时会做有限重试，模型/外部调度不要无界叠加。

## 4. Source 录入端

### 4.1 只读连通检查

`GET /ingest/{source_id}/status`，Source ingest。只验证自己的启用状态、发布字段和有效录入权限，不需要 read/sync，不读资料库、不创建资料。该来源须为 manual。

### 4.2 JSON 批量资料

```http
POST /api/v1/ingest/<来源UUID> HTTP/1.1
Authorization: Bearer <来源 Key>
Content-Type: application/json

{"items":[{"external_id":"article:stable-id","title":"真实资料标题","format":"markdown","kind":"article","content":"有出处的原创整理正文","url":"https://example.org/a","category":"编程","content_mode":"learning","language":"zh-CN","tags":["学习"]}]}
```

单批 1–100、≤4 MiB，整批成功或回滚；同 source+external_id 更新同 ID，规范化内容相同返回 unchanged。批次遗漏不删除，批内 external_id 不重复。

当前新增/实际更新入库即发布；发布指所属用户及其获授权客户端可读，不是全站公开或已通知客户端。相同内容重放不等于重新发布已下架资料，已删除内容不可通过换 ID 绕过保护。最终以回执和对应记录实际状态判断。

### 4.3 本地文件入库

`POST /ingest/{source_id}/files`，需 ingest+upload。multipart：

| 字段 | 含义 |
|---|---|
| metadata | 序列化的单个 Content JSON 字符串，不是 `{items:[...]}` |
| files | 实际文件；单文件文章/音频/视频，轮播最多 20 张 |

服务默认总上传上限 200 MiB，以实例配置为准；校验实际 MIME、类型、数量、文件名与来源归属。与 `/agent/uploads` 不同，此接口把文件登记及资料入库作为同一业务事务，不需要再发一次普通 ingest。媒体重复上传会新增不可变资产，超时后不要自动重传。

如果只做来源资料采集推送，可使用已有 `wzstudy-push` Skill 的预检/入库流程；不要因本 Skill 覆盖了更多 API 就改用管理凭据日常录入。

## 5. Content 字段

字节限制按 UTF-8，不按中文字符数计算。未知字段不随意透传。

| 字段 | 类型/约束 |
|---|---|
| external_id/title | 必填字符串；分别 1–512 / 1–500 字节 |
| summary/content | 字符串；分别 ≤8000 / 262144 字节 |
| format/kind | text 或 markdown；article/audio/video/carousel，默认 text/article |
| url | 可空 HTTP(S)，≤2048 字节，无用户名密码/fragment |
| author/category/language | ≤200/200/35 字节；分类/语言可继承来源默认值 |
| tags | ≤20 个字符串，每个 ≤80 字节；规范化去重排序 |
| published_at/source_updated_at | 可选 RFC3339，未知省略，不作为可靠同步游标 |
| assets | 已上传登记的资产引用，不能伪造 ID/来源/真实类型 |
| content_mode | default/news/learning，与 category 独立；更新省略保留原模式 |
| platform/outline | 原始平台显示名/整理大纲；不能把大纲冒充完整字幕或已读全文 |
| duration_ms/orientation | 可选真实时长；landscape/portrait；未知不编造 |
| chapters | 音视频章节，≤300；每项 id/title/start_ms，时间为非负毫秒 |
| subtitles | 音视频可选 ≤8 轨；id/language/format/content，format=srt/vtt，每轨正文 ≤256 KiB |

Agent 模型的 `ingest_items` schema 只开放其中一部分字段，以宿主工具定义为准；不能认为 REST 支持即模型工具支持。

## 6. 查询、媒体、增量同步与推荐

### 查询（Client read 或用户会话）

- `GET /sources`：来源的安全公开描述，不返回 Key/上游内部状态。
- `GET /items`：已发布快照；kind、source_id（最多 20 个 UUID，逗号分隔）、category、tag、q、content_mode 可筛选；普通列表 limit 1–100、offset 0–10000，q ≤200 字节。反馈去重/时间窗等扩展参数见随附 OpenAPI，不把所有列表参数套在每个接口上。
- `GET /items/{id}`：未发布/下架/不存在返回 404，不回退读取 draft。
- `GET /media/{id}`：返回授权媒体二进制，媒体仍需凭据，不把带令牌 URL 分享出去。
- `GET /catalog/collections` 与 `/{id}`：公开目录合集与可见章节；普通列表上限 20。

### 同步（Client sync 或用户会话）

`GET /changes?after=0&limit=100&source_id=<可选来源UUID列表>`。

```json
{"data":{"changes":[],"next_cursor":100,"high_watermark":100,"has_more":false}}
```

`after` 非负 int64；按 cursor 升序；过滤页可能 changes 为空却推进 next_cursor。始终原子提交本地缓存和 next_cursor，依据 has_more 继续；after 超前应检查环境/备份，不擅自重置或跳过历史。首次从 0 回放 upsert/delete，变更来源筛选则重建。不能用 offset 末页时间取代游标。

`GET /catalog/collections/changes` 使用单独合集游标，每页最多 20；资料/合集/反馈/学习游标互不替代。

### 推荐与学习模式

- `GET /client/recommendations?limit=20&cursor=...`：用户会话或 read+feedback Client；使用 next_cursor/has_more，身份绑定的短期快照，不是持久同步游标。查询不自动记录查看/推荐。
- `GET /client/learning/modes/categories?mode=news|collection|route`。
- `GET /client/learning/modes/items?mode=news|collection|route|review|random`；collection 要 collection_id，route 要 roadmap_id，可选 node_id；random 可带 seed。limit 1–100，offset 0–1000000。
- 普通模式查询/统计接受用户会话或 Client read/sync；复习和个性化状态仍需要学习数据权限。资讯按 content_mode=default/news，不按 category 是否叫“资讯”；learning 不自动进入资讯。

## 7. 本人工作区、来源和资料管理（User）

所有修改必须有用户明确意图；发布、下架、删除、轮换 Key、身份迁移尤其不能作为自检。先 GET 获取最新 revision；409 后重新读取、比较，不直接覆盖。

- `GET /workspace/workspace`：本人工作区概况；路径确实有两段 workspace。
- 来源：`GET/POST /workspace/sources`，`GET/PUT /workspace/sources/{id}`；`POST /{id}/refresh`、`POST /{id}/key`。创建 Source 可用 kind=manual、integration=agent|skill，permissions 显式选 ingest/upload。**PUT 是可编辑配置替换，不是 PATCH**，slug/kind 不可改；permissions 省略/null 保留、[] 撤销。轮换带 revision，会立即废止旧 Key。
- 查询端：`GET/POST /workspace/clients`，`GET/PUT /workspace/clients/{id}`；`GET/PUT/POST /{id}/key` 分别为查看、验证补存、轮换，查看也会暴露秘密。创建时显式指定所需 read/sync/feedback，避免依赖默认权限。
- `GET /workspace/intakes?source_id=<可选UUID>&limit=20&offset=0`：已提交回执，不包含条目正文或 Key；提交时快照不代表之后一直公开。
- `/workspace/identity-migration` 的 GET 是预检，POST 是显式身份迁移，不自动执行。

### 手动草稿

`POST /workspace/items` / `PUT /workspace/items/{id}`：

```json
{"revision":0,"status":"draft","content":{"title":"用户授权创建的资料","format":"markdown","content":"正文","kind":"article"}}
```

新建为 revision=0，更新使用最新记录 revision；支持可选来源绑定。此管理输入与来源入库 `{items:[...]}` 不同。`POST /workspace/items/{id}/submit|publish|unpublish|delete` 使用对应记录 revision；删除和下架不是一个操作，不能混为“清理缓存”。批量删除另用 `/workspace/items/batch-delete`，先核对所有对象与版本。

### 分类、内容模式与目录移动

`POST /workspace/items/organize`，每批 1–100，原子执行：

```json
{"items":[{"id":"<资料UUID>","revision":3}],"category":"编程","content_mode":"learning"}
```

可另传 collection_id、collection_revision 移到目标合集；`collection_id=""` 表示移出全部目录合集，省略不改。category 为空清空主题；content_mode=default 明确重置。快捷元数据操作不应顺带公开待审核正文/附件。

## 8. 目录合集、路线图、个人合集与渠道

这几类不是同一种资源，不要混用 ID、revision 或发布动作。

| 资源 | 路径 | 规则 |
|---|---|---|
| 目录合集管理 | `/workspace/catalog/collections`、`/{id}`、`/{id}/delete` | User；新建 revision=0，title/summary/chapters；保存即生效，客户端只见已发布成员 |
| 目录合集查询 | `/catalog/collections`、`/{id}`、`/changes` | read/sync 或 User；独立同步游标 |
| 学习路线管理 | `/workspace/roadmaps`、`/{id}`、`/{id}/delete` | User；新建 revision=0；最多 300 节点、12 层，唯一根，根标题等于路线名，无环且全可达 |
| 个人合集 | `/client/learning/collections`、`/{collection_id}`、`/commands` | 学习身份；create/rename/add/remove/reorder/delete，事件 ID 幂等及 expected_revision |
| 本人渠道配置 | `GET/PUT /workspace/channel` | User；private/public/invited，显式 source_ids；修改共享范围必须明确授权 |
| 渠道访问 | `/channels`、`/{channel_id}/items`、`/{channel_id}/items/{item_id}`、对应媒体路径 | User；只返回获授权渠道的已发布资料，不共享学习记录 |

目录章节格式为 `{id,title,item_id}`，新章 id 可为空由服务端生成。路线节点字段为 id/parent_id/title/note/item_ids/collapsed，根 parent_id 为空；不要删除用户未要求移除的节点或不可见占位成员。编辑目录/路线不会伪造个人学习完成状态。

## 9. 私人学习、反馈、草稿与导入

先 `GET /client/learning/capabilities` 检查 `identity/version/read/write/features`；普通 Client 能力探测需 feedback 且 read 或 sync，用户会话使用用户身份。只读查询不能冒充真实查看、完成复习或学习时长。

### 学习事件

`POST /client/learning/events`，正文 `{"events":[...]}`，每批 1–100，HTTP 正文 ≤1 MiB，整批原子。每条 event_id/item_id 为真实非零 UUID，action 必填。

```json
{"events":[{"event_id":"<稳定事件UUID>","item_id":"<真实资料UUID>","action":"review_plan","value":true,"review_intervals_minutes":[20,60,1440]}]}
```

action：

- view：真实有效查看，不从预取/查询推断。
- review_plan：value=true/false，设置/取消计划，可选自定义间隔。
- review：完成一次真实复习，使用真实 occurred_at。
- progress：position、mode、expected_revision；允许主动回看但拒绝旧版本覆盖。
- record_create / record_delete：record_id；创建 text ≤300 字符，可带位置、parent_id、as_danmaku；draft_revision 可原子清空匹配草稿。
- duration：session_id、递增 session_sequence、start_at/end_at；单段最多两分钟，重叠区间按并集计算，不伪造计时。

position.version 是资料公开 revision；媒体用 time_ms，文章用 block_id=body/source 和 UTF-16 offset，轮播使用真实 image_id。事件重试必须复用相同 event_id 与全部载荷；ID 相同内容不同返回冲突。

### 读取、草稿、反馈

- `GET /client/learning?item_id=<可选UUID列表>&limit=20&offset=0`、`GET /client/learning/events?after=0`、资料 records 列表/详情/stats，详见完整索引。
- `GET /client/learning/items/{item_id}/draft`、`POST /client/learning/drafts/commands`：命令为 event_id/item_id/text/expected_revision，可带 parent_id/position；初次 expected_revision=0，清空仍保留版本墓碑。
- 草稿历史通过 `/items/{item_id}/draft/history`、`/drafts/events/{event_id}` 读取，私密内容不能随意放入采集正文或进度日志。
- `POST /client/feedback` 的正文是 `{events:[FeedbackInput]}`；action 为 recommended/impression/read/like/record/favorite，区别于学习 action。读状态用 GET 同路径，事件游标用 `/client/feedback/events`。
- `/import-legacy*` 只在用户明确核验归属并授权后调用；不是普通同步或覆盖恢复。参数、修订、墓碑与目标不存在条件见 schema/目标版本，不自行导入旧磁盘数据，也不删除原本机资料。

## 10. 采集任务、模板、身份与计划（User）

优先 `/workspace/agent/*`。管理员兼容入口 `/admin/agent/*` 作用于管理员自己的工作区；内置运行时、模型配置、部署、程序发布另有管理员能力。

### 一次性任务

```http
POST /api/v1/workspace/agent/tasks HTTP/1.1
Authorization: Bearer <用户会话>
Content-Type: application/json

{"title":"收集官方文章","goal":"收集 Go 官方博客的近期文章，整理成中文学习摘要并保留出处","constraints":{"max_items":3,"content_type":"article","duplicate_policy":"skip","allow_domains":["go.dev"],"language":"zh-CN"},"budget":{"max_rounds":30,"max_tool_calls":40,"timeout_seconds":300}}
```

source_id 可省略，由服务端绑定/创建当前工作区的 Agent 收集来源；可选 assigned_agent_id 指定本人采集端。goal ≤4000、title ≤200 UTF-8 字节；max_items 还受服务端策略约束。

- GET `/workspace/agent/status`、`/agents`、`/tasks`、`/tasks/{id}`、`/tasks/{id}/events` 读取本人状态/事件。
- POST `/tasks/{id}/cancel`：运行中取消在下一次心跳生效，不等于回滚。
- POST `/tasks/{id}/retry`：普通失败/取消/过期任务，正文 `{}`；不能对计划产生的终态任务调用，应通过计划生成新运行。
- POST `/tasks/{id}/delete`：只删除已结束任务及事件，计划历史保留必要摘要，不删除已入库资料。
- `/task-templates` 保存的是表单配置，模板变化不会追溯修改既有计划。
- `/agents/{id}/key`、`/package`、`/delete` 涉及凭据、带密钥部署包或身份删除，需要独立授权；身份仍有未结束任务时删除返回 agent_in_use。

### 采集计划

管理基路径 `/workspace/agent/collection-plans`：列表/新建、`/{id}` 读取/PUT、`/preview` 只读预览、`/{id}/state` 启停、`/{id}/delete` 归档、`/{id}/run` 手动运行、`/{id}/runs` 历史、`/{id}/trigger-key` 管理外部凭据。

```json
{"name":"每天官方文章","enabled":false,"task":{"goal":"采集 Go 官方博客近期文章并保留出处","constraints":{"max_items":3,"allow_domains":["go.dev"],"duplicate_policy":"skip"},"budget":{"max_rounds":30,"max_tool_calls":40,"timeout_seconds":300}},"schedule":{"kind":"daily","timezone":"Asia/Shanghai","time_of_day":"09:00"},"queue_ttl_seconds":3600,"catch_up_seconds":3600}
```

示例默认暂停；新建/编辑/预览/启用均不等于立即执行。启停用 `{revision,enabled}`，归档用 `{revision}`，手动运行用稳定 `event_id`。

schedule.kind 为 manual/once/interval/daily/weekly/unread_inventory/webhook；每日/每周按 IANA 时区，单次 at 必须带明确时区偏移。interval_minutes 范围 1–10080，weekdays=0..6（周日0）。队列 TTL 60–604800 秒、补跑窗口30–604800秒，默认均3600；同一计划默认不重叠。

未读库存条件用：

```json
{"schedule":{"kind":"unread_inventory","timezone":"Asia/Shanghai","interval_minutes":10,"unread_condition":{"operator":"lt","threshold":20}}}
```

上述为 `/preview` 请求体，可只读检查时间与当前库存，不创建任务。operator=lt/lte/eq/gte/gt，threshold 必填0..1000000；统计本人公开资料的真实未读状态，不是全部用户资料。创建后一个检查间隔再检查，不立即补采；手动运行该模式也会先判断条件。

### 外部触发（Trigger）

```http
POST /api/v1/collection-triggers/<计划UUID> HTTP/1.1
Authorization: Bearer <计划专用触发 Key>
Content-Type: application/json

{"event_id":"调用方稳定事件编号","sent_at":"<当前发送时间RFC3339>","url":"https://example.org/a"}
```

仅允许预设为 webhook 的已启用计划；url/keyword 必须被计划 allow_url/allow_keyword 显式开放并符合域名限制。没有开放时只发 event_id/sent_at，不添加示例 url。不能覆盖 owner/source/budget/constraints。

- 请求 ≤4 KiB，event_id 为1–128字节；sent_at 必须在服务器前后5分钟。重试保留事件ID及参数，只可刷新 sent_at。
- 每计划每分钟最多一个新外部事件，另有 IP/进程限制；429 遵守 Retry-After。
- 响应仅 run_id/task_id/dispatch_status/reason，queued 只代表已排队，不是采集成功；skipped/blocked 应如实报告。
- 暂停停止新触发，不取消既有任务；归档撤销触发凭据，保留历史。超时重试使用同一逻辑事件，不能随意换 ID 产生新任务。

## 11. 管理员接口与错误处理

`/admin/users` 为账号管理；`/admin/agent/runtime` 及 start/stop/restart/test-connection、models/deployments/releases 为内置采集端/模型/程序管理。不属于普通采集任务，运行测试也可能调用外部付费模型。必须用户明确授权，不以“检查配置”为由读取模型密钥、重启服务或生成含密钥 ZIP。

| 状态 | 常见 code | 处理 |
|---|---|---|
| 400/415 | invalid_request/invalid_json/content_type_mismatch/unsupported_media_type | 核对类型、已开放字段、正文、媒体和对应接口 schema |
| 401/403 | unauthorized/user_login_required/permission_denied/source_disabled/csrf_denied | 只检查已授权身份及目标；不取其他 Key 或绕过 CSRF |
| 404/410 | not_found/接口退役 | 核对实例版本、权限、公开状态及是否退役，不回退读取他人草稿 |
| 409 | conflict/task_claimed/lease_expired/task_cancelled/agent_in_use | revision 冲突刷新后比较；租约/取消停止写；身份在用不强删 |
| 413 | payload_too_large/item_quota_exceeded | 按该接口限制缩批，不能超任务配额 |
| 429 | 限流/登录繁忙 | 遵守 Retry-After，限制并发，不切换身份绕过 |
| 5xx/断线 | internal_error/not_ready/timeout 等 | 不证明未提交；先查授权状态/回执，报告不确定 |

不要自动重试创建身份/任务、部署、媒体上传或无幂等契约的写入。幂等事件重放保持原逻辑标识和完整正文；管理 PUT 不是 PATCH。收尾不回显令牌、私密草稿、用户密码、原始错误体或未脱敏日志。

## 12. 完整端点索引

以下 185 个 HTTP 操作（152 个路径模板）由随附 `openapi.json` 生成，与仓库路由覆盖测试交叉核验。所有业务路径均加 `/api/v1`。

身份缩写：User=本人用户会话，Client=独立查询客户端（需 read/sync/feedback 中对应权限），Admin=管理员角色；Agent/Source/Trigger 不可互换。表中 schema 名对应 `openapi.json` 的 `components.schemas`；`{...}` 为内联请求结构。查询参数的类型/范围、响应、必填字段以具体 schema 与前文规则为准。

此索引包含敏感管理及历史兼容路径以避免凭空猜接口；列出不等于已授权使用。`POST` 也可能是只读预览或查重，`GET .../key` 也可能泄露凭据，不按方法一刀切判断安全。

### 健康与登录

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| POST | `/auth/login` | 无 | EmailLogin | — | 200 |
| POST | `/auth/logout` | Admin | — | — | 200 |
| GET | `/auth/session` | Admin | — | — | 200 |
| GET | `/health/live` | 无 | — | — | 200 |
| GET | `/health/ready` | 无 | — | — | 200 |
| POST | `/user/auth/login` | 无 | UserLogin | — | 200 |
| POST | `/user/auth/logout` | User | — | — | 200 |
| POST | `/user/auth/password` | User | UserPasswordChange | — | 200 |
| GET | `/user/auth/session` | User | — | — | 200 |

### Agent 运行时

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/agent/status` | Agent | — | — | 200 |
| POST | `/agent/tasks/claim` | Agent | ClaimInput | — | 200 |
| POST | `/agent/tasks/{id}/complete` | Agent | CompleteInput | — | 204 |
| POST | `/agent/tasks/{id}/duplicates` | Agent | {attempt, items} | — | 200 |
| POST | `/agent/tasks/{id}/events` | Agent | EventsInput | — | 200 |
| POST | `/agent/tasks/{id}/heartbeat` | Agent | {} | — | 200 |
| POST | `/agent/tasks/{id}/items` | Agent | ItemsInput | — | 200 |
| POST | `/agent/tasks/{id}/release` | Agent | ReleaseInput | — | 204 |
| POST | `/agent/uploads` | Agent | multipart {files} | — | 201 |

### Source 录入

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| POST | `/ingest/{id}/files` | Source | multipart {metadata, files} | — | 201 |
| GET | `/ingest/{id}/status` | Source | — | — | 200 |
| POST | `/ingest/{source_id}` | Source | {items} | — | 200 |

### 查询与目录

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/catalog/collections` | User / Client | — | limit, offset | 200 |
| GET | `/catalog/collections/changes` | User / Client | — | after, limit | 200 |
| GET | `/catalog/collections/{id}` | User / Client | — | — | 200 |
| GET | `/changes` | User / Client | — | after, limit, source_id | 200 |
| GET | `/items` | User / Client | — | kind, source_id, category, tag, q, limit, offset, exclude, unseen, exclude_since, include_feedback, content_mode | 200 |
| GET | `/items/{id}` | User / Client | — | — | 200 |
| GET | `/items/{id}/learning-stats` | User / Client | — | — | 200 |
| GET | `/media/{id}` | User / Client | — | — | 200, 206 |
| GET | `/sources` | User / Client | — | — | 200 |

### 工作区资料及身份

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/workspace/catalog/collections` | User | — | limit, offset | 200 |
| POST | `/workspace/catalog/collections` | User | CatalogInput | — | 201 |
| GET | `/workspace/catalog/collections/{id}` | User | — | — | 200 |
| PUT | `/workspace/catalog/collections/{id}` | User | CatalogInput | — | 200 |
| POST | `/workspace/catalog/collections/{id}/delete` | User | CatalogRevision | — | 200 |
| POST | `/workspace/catalog/collections/{id}/publish` | User | CatalogRevision | — | 见定义 |
| POST | `/workspace/catalog/collections/{id}/unpublish` | User | CatalogRevision | — | 见定义 |
| GET | `/workspace/clients` | User | — | — | 200 |
| POST | `/workspace/clients` | User | ClientInput | — | 201 |
| GET | `/workspace/clients/{id}` | User | — | — | 200 |
| PUT | `/workspace/clients/{id}` | User | ClientUpdate | — | 200 |
| GET | `/workspace/clients/{id}/feedback` | User | — | item_id, limit, offset | 200 |
| GET | `/workspace/clients/{id}/feedback/events` | User | — | after, limit | 200 |
| GET | `/workspace/clients/{id}/items` | User | — | kind, source_id, category, tag, q, limit, offset, exclude, unseen, exclude_since, include_feedback, content_mode | 200 |
| GET | `/workspace/clients/{id}/key` | User | — | — | 200 |
| POST | `/workspace/clients/{id}/key` | User | Revision | — | 200 |
| PUT | `/workspace/clients/{id}/key` | User | {access_key} | — | 200 |
| GET | `/workspace/clients/{id}/learning` | User | — | limit, offset, item_id, from, until | 200 |
| GET | `/workspace/clients/{id}/learning/events` | User | — | after, limit | 200 |
| GET | `/workspace/clients/{id}/learning/items/{item_id}/records` | User | — | limit, offset | 200 |
| GET | `/workspace/identity-migration` | User | — | — | 200 |
| POST | `/workspace/identity-migration` | User | IdentityMigrationInput | — | 200 |
| GET | `/workspace/intakes` | User | — | source_id, limit, offset | 200 |
| GET | `/workspace/items` | User | — | source_id, category, tag, q, limit, offset, kind, status, sort, content_mode | 200 |
| POST | `/workspace/items` | User | ManagedInput | — | 201 |
| POST | `/workspace/items/batch-delete` | User | {items} | — | 200 |
| POST | `/workspace/items/organize` | User | ItemOrganizationInput | — | 200 |
| GET | `/workspace/items/{id}` | User | — | — | 200 |
| PUT | `/workspace/items/{id}` | User | ManagedInput | — | 200 |
| POST | `/workspace/items/{id}/delete` | User | Revision | — | 200 |
| POST | `/workspace/items/{id}/publish` | User | Revision | — | 200 |
| POST | `/workspace/items/{id}/submit` | User | Revision | — | 200 |
| POST | `/workspace/items/{id}/unpublish` | User | Revision | — | 200 |
| GET | `/workspace/roadmaps` | User | — | limit, offset, q | 200 |
| POST | `/workspace/roadmaps` | User | RoadmapInput | — | 201 |
| GET | `/workspace/roadmaps/{id}` | User | — | — | 200 |
| PUT | `/workspace/roadmaps/{id}` | User | RoadmapInput | — | 200 |
| POST | `/workspace/roadmaps/{id}/delete` | User | RoadmapRevision | — | 200 |
| PUT | `/workspace/settings` | User | Settings | — | 200 |
| GET | `/workspace/sources` | User | — | — | 200 |
| POST | `/workspace/sources` | User | SourceInput | — | 201 |
| GET | `/workspace/sources/{id}` | User | — | — | 200 |
| PUT | `/workspace/sources/{id}` | User | SourceUpdate | — | 200 |
| POST | `/workspace/sources/{id}/key` | User | Revision | — | 200 |
| POST | `/workspace/sources/{id}/refresh` | User | — | — | 200 |
| POST | `/workspace/uploads` | User | multipart {metadata, files} | — | 201 |
| GET | `/workspace/workspace` | User | — | — | 200 |

### 工作区 Agent、任务与计划

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/workspace/agent/agents` | User | — | — | 200 |
| POST | `/workspace/agent/agents` | User | AgentInput | — | 201 |
| GET | `/workspace/agent/agents/{id}` | User | — | — | 200 |
| PUT | `/workspace/agent/agents/{id}` | User | AgentUpdate | — | 200 |
| POST | `/workspace/agent/agents/{id}/delete` | User | {revision} | — | 200 |
| GET | `/workspace/agent/agents/{id}/key` | User | — | — | 200 |
| POST | `/workspace/agent/agents/{id}/key` | User | RevisionInput | — | 200 |
| POST | `/workspace/agent/agents/{id}/package` | User | {access_key, model} | — | 200 |
| GET | `/workspace/agent/binary` | User | — | — | 200 |
| GET | `/workspace/agent/collection-plans` | User | — | include_deleted | 200 |
| POST | `/workspace/agent/collection-plans` | User | CollectionPlanInput | — | 201 |
| POST | `/workspace/agent/collection-plans/preview` | User | {schedule} | — | 200 |
| GET | `/workspace/agent/collection-plans/{id}` | User | — | — | 200 |
| PUT | `/workspace/agent/collection-plans/{id}` | User | CollectionPlanUpdate | — | 200 |
| POST | `/workspace/agent/collection-plans/{id}/delete` | User | {revision} | — | 200 |
| POST | `/workspace/agent/collection-plans/{id}/run` | User | CollectionPlanTrigger | — | 200 |
| GET | `/workspace/agent/collection-plans/{id}/runs` | User | — | limit, offset | 200 |
| POST | `/workspace/agent/collection-plans/{id}/state` | User | {revision, enabled} | — | 200 |
| POST | `/workspace/agent/collection-plans/{id}/trigger-key` | User | {revision, revoke} | — | 200 |
| GET | `/workspace/agent/status` | User | — | — | 200 |
| GET | `/workspace/agent/task-templates` | User | — | — | 200 |
| POST | `/workspace/agent/task-templates` | User | {name, title, goal, constraints, budget, priority, assigned_agent_id} | — | 201 |
| POST | `/workspace/agent/task-templates/{id}/delete` | User | — | — | 200 |
| GET | `/workspace/agent/tasks` | User | — | — | 200 |
| POST | `/workspace/agent/tasks` | User | CollectionTaskInput | — | 201 |
| GET | `/workspace/agent/tasks/{id}` | User | — | — | 200 |
| POST | `/workspace/agent/tasks/{id}/cancel` | User | CancelInput | — | 200 |
| POST | `/workspace/agent/tasks/{id}/delete` | User | {} | — | 200 |
| GET | `/workspace/agent/tasks/{id}/events` | User | — | — | 200 |
| POST | `/workspace/agent/tasks/{id}/retry` | User | {} | — | 200 |

### 学习、反馈与模式

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/client/connection` | Client | — | — | 200 |
| GET | `/client/feedback` | User / Client | — | item_id, limit, offset | 200 |
| POST | `/client/feedback` | User / Client | {events} | — | 200 |
| GET | `/client/feedback/events` | User / Client | — | after, limit | 200 |
| POST | `/client/feedback/import-legacy` | User / Client | {events} | — | 200 |
| GET | `/client/learning` | User / Client | — | limit, offset, item_id, from, until | 200 |
| GET | `/client/learning/capabilities` | User / Client | — | — | 200 |
| GET | `/client/learning/collections` | User / Client | — | limit, offset | 200 |
| POST | `/client/learning/collections/commands` | User / Client | PersonalCollectionCommand | — | 200 |
| POST | `/client/learning/collections/import-legacy` | User / Client | {event_id, collection_id, title, items, created_at, updated_at} | — | 200 |
| GET | `/client/learning/collections/{collection_id}` | User / Client | — | — | 200 |
| POST | `/client/learning/drafts/commands` | User / Client | LearningDraftCommand | — | 200 |
| GET | `/client/learning/drafts/events/{event_id}` | User / Client | — | — | 200 |
| POST | `/client/learning/drafts/import-legacy` | User / Client | LegacyDraftInput | — | 200 |
| GET | `/client/learning/events` | User / Client | — | after, limit | 200 |
| POST | `/client/learning/events` | User / Client | LearningReport | — | 200 |
| POST | `/client/learning/import-legacy-records` | User / Client | {records} | — | 200 |
| POST | `/client/learning/import-legacy-reviews` | User / Client | {reviews} | — | 200 |
| GET | `/client/learning/items/{item_id}/draft` | User / Client | — | parent_id | 200 |
| GET | `/client/learning/items/{item_id}/draft/history` | User / Client | — | parent_id, limit, offset | 200 |
| GET | `/client/learning/items/{item_id}/records` | User / Client | — | limit, offset | 200 |
| GET | `/client/learning/items/{item_id}/records/{record_id}` | User / Client | — | — | 200 |
| GET | `/client/learning/items/{item_id}/stats` | User / Client | — | — | 200 |
| GET | `/client/learning/modes/categories` | User / Client | — | mode, limit, offset | 200 |
| GET | `/client/learning/modes/items` | User / Client | — | mode, limit, offset, category, collection_id, roadmap_id, node_id, q, seed | 200 |
| GET | `/client/recommendations` | User / Client | — | limit, cursor | 200 |

### 渠道共享

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/channels` | User | — | — | 200 |
| GET | `/channels/{channel_id}/items` | User | — | limit, offset, q, kind | 200 |
| GET | `/channels/{channel_id}/items/{item_id}` | User | — | — | 200 |
| GET | `/channels/{channel_id}/items/{item_id}/media/{asset_id}` | User | — | — | 200, 206 |
| GET | `/workspace/channel` | User | — | — | 200 |
| PUT | `/workspace/channel` | User | {revision, name, description, visibility, source_ids, invited_emails} | — | 200 |

### 管理员账号与运维

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| GET | `/admin/agent/agents` | Admin | — | — | 200 |
| POST | `/admin/agent/agents` | Admin | AgentInput | — | 201 |
| GET | `/admin/agent/agents/{id}` | Admin | — | — | 200 |
| PUT | `/admin/agent/agents/{id}` | Admin | AgentUpdate | — | 200 |
| POST | `/admin/agent/agents/{id}/delete` | Admin | {revision} | — | 200 |
| GET | `/admin/agent/agents/{id}/key` | Admin | — | — | 200 |
| POST | `/admin/agent/agents/{id}/key` | Admin | RevisionInput | — | 200 |
| POST | `/admin/agent/agents/{id}/package` | Admin | {access_key, model} | — | 200 |
| GET | `/admin/agent/binary` | Admin | — | — | 200 |
| GET | `/admin/agent/collection-plans` | Admin | — | include_deleted | 200 |
| POST | `/admin/agent/collection-plans` | Admin | CollectionPlanInput | — | 201 |
| POST | `/admin/agent/collection-plans/preview` | Admin | {schedule} | — | 200 |
| GET | `/admin/agent/collection-plans/{id}` | Admin | — | — | 200 |
| PUT | `/admin/agent/collection-plans/{id}` | Admin | CollectionPlanUpdate | — | 200 |
| POST | `/admin/agent/collection-plans/{id}/delete` | Admin | {revision} | — | 200 |
| POST | `/admin/agent/collection-plans/{id}/run` | Admin | CollectionPlanTrigger | — | 200 |
| GET | `/admin/agent/collection-plans/{id}/runs` | Admin | — | limit, offset | 200 |
| POST | `/admin/agent/collection-plans/{id}/state` | Admin | {revision, enabled} | — | 200 |
| POST | `/admin/agent/collection-plans/{id}/trigger-key` | Admin | {revision, revoke} | — | 200 |
| POST | `/admin/agent/deployments` | Admin | {name, slug, model} | — | 200 |
| POST | `/admin/agent/models` | Admin | {provider, base_url, api_key, use_saved_key} | — | 200 |
| GET | `/admin/agent/releases` | Admin | — | — | 200 |
| POST | `/admin/agent/releases` | Admin | multipart {version, platform, arch, notes, file} | — | 201 |
| POST | `/admin/agent/releases/{id}/delete` | Admin | — | — | 204 |
| GET | `/admin/agent/releases/{id}/download` | Admin | — | — | 200 |
| GET | `/admin/agent/runtime` | Admin | — | — | 200 |
| PUT | `/admin/agent/runtime` | Admin | {work_dir, auto_start, model, install_dir, restart} | — | 200 |
| POST | `/admin/agent/runtime/restart` | Admin | {} | — | 200 |
| POST | `/admin/agent/runtime/start` | Admin | {} | — | 200 |
| POST | `/admin/agent/runtime/stop` | Admin | {} | — | 200 |
| POST | `/admin/agent/runtime/test-connection` | Admin | {provider, base_url, api_key, use_saved_key, model, clear_api_key} | — | 200 |
| GET | `/admin/agent/status` | Admin | — | — | 200 |
| GET | `/admin/agent/task-templates` | Admin | — | — | 200 |
| POST | `/admin/agent/task-templates` | Admin | {name, title, goal, constraints, budget, priority, assigned_agent_id} | — | 201 |
| POST | `/admin/agent/task-templates/{id}/delete` | Admin | — | — | 200 |
| GET | `/admin/agent/tasks` | Admin | — | — | 200 |
| POST | `/admin/agent/tasks` | Admin | CollectionTaskInput | — | 201 |
| GET | `/admin/agent/tasks/{id}` | Admin | — | — | 200 |
| POST | `/admin/agent/tasks/{id}/cancel` | Admin | CancelInput | — | 200 |
| POST | `/admin/agent/tasks/{id}/delete` | Admin | {} | — | 200 |
| GET | `/admin/agent/tasks/{id}/events` | Admin | — | — | 200 |
| POST | `/admin/agent/tasks/{id}/retry` | Admin | {} | — | 200 |
| GET | `/admin/users` | Admin | — | — | 200 |
| POST | `/admin/users` | Admin | UserInput | — | 201 |
| PUT | `/admin/users/{id}` | Admin | UserUpdate | — | 200 |

### 外部计划触发

| 方法 | 路径 | 身份 | 请求体 schema / 字段 | 主要 query | 成功状态 |
|---|---|---|---|---|---|
| POST | `/collection-triggers/{id}` | Trigger | CollectionWebhookInput | — | 200 |

