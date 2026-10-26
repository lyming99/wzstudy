# 查询客户端反馈与推荐去重

## 业务边界

服务器继续只处理资料录入和客户端数据查询，不增加推送或推荐排序算法。现在查询客户端可以把实际推荐/曝光及学习动作上报到自己的反馈空间，查询时按这些记录排除旧资料。

**GET 候选、同步内容、管理员预览或打开详情都不会自动记为推荐/已读。** 客户端选定推荐项后上报 recommended，真正展示后可上报 impression；用户动作再按实际发生时间上报，不能把“查询返回”误当“用户看过”。

反馈按 **Client ID + 资料 ID** 隔离，不是全站共享排除表，也不是按 Key 文本隔离。轮换 Key 后仍保留同一客户端的历史；另建客户端不会继承其他身份历史。多设备/多用户独立去重时应分别建立身份；共享一个客户端的设备会共享反馈。

## 授权

在“数据查询 → 查询客户端 → 配置权限”显式勾选 **上报学习反馈（feedback）**。新建 API、已有记录与界面默认仍为 read/sync，**不会自动扩权或换 Key**。

- POST 反馈需要启用的独立 Client Key 和 feedback 权限。
- 自己的状态/事件读取接受 read、sync、feedback 任一权限。
- 候选资料查询仍需要 read；仅反馈权限不获得内容查询、来源、媒体或同步能力。
- 反馈写入不接受管理员 Cookie/Key、全局只读 Key 或 Source Key。不能指定其他 client_id。
- 管理员可只读查看指定客户端反馈及模拟去重候选；此操作不生成真实学习事件。

扫码 v1 的六字段载荷保持不变。身份校验可返回 feedback，但仍要求 read 或 sync 才允许作为查询连接配对。旧扫码解析器应更新允许的权限列表，不把 feedback 当成录入权限。

## 批量上报

```http
POST /api/v1/client/feedback
Authorization: Bearer <查询客户端 Key>
Content-Type: application/json
```

```json
{
  "events": [
    {
      "event_id": "11111111-2222-4333-8444-555555555555",
      "item_id": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
      "action": "recommended",
      "value": true,
      "item_revision": 4,
      "occurred_at": "2026-10-25T20:00:00Z"
    }
  ]
}
```

示例 ID 无效于真实库。每批 1–100 条、整个批次原子提交：其中任何事件无效、资料未公开、事件 ID 冲突或权限失效，均不保存本批的新事件。

| action | 含义 | value=false |
| --- | --- | --- |
| recommended | 客户端实际选定为一次推荐 | 不允许；历史事实不撤销 |
| impression | 实际展示/曝光 | 不允许 |
| read | 标记已读 | 支持改回未读 |
| like | 喜欢 | 支持取消喜欢 |
| record | 学习记录 | 支持取消当前记录标记 |
| favorite | 收藏 | 支持取消收藏 |

- event_id、item_id 必须是非零 UUID，大小写/标准格式会规范化。
- **事件发生时只创建一次 event_id，并先持久保存待发送数据。超时重试使用相同 ID 和相同内容，不要重新生成。**
- 相同客户端同 ID 同内容返回 duplicate，不重复计数；同 ID 不同内容为 409。不同客户端可以独立使用同一事件 ID。
- value 默认 true；recommended/impression 只能为 true。
- occurred_at 可省略，此时使用服务器接收时间。显式时间需为 RFC3339，不能早于 2000 年或超出服务器当前时间五分钟；离线历史可补报。
- 状态按发生时间更新；旧时间的迟到事件只进入历史计数，不覆盖较新状态。相同时间以服务器接受序号为准，批内按数组顺序处理。撤销必须使用新事件 ID。
- item_revision 默认绑定当时公开版本；允许上报较早修订号（服务校验不超过当前公开修订，旧版本展示由客户端记录），不允许引用超过当前公开修订的草稿。下架/删除后不接受新事件，但已接受事件可幂等重试。
- record 且 value=true 时，可附 note（最多 4000 UTF-8 字节，不含 NUL）和 progress（0–1）。其他动作或撤销不得带 note/progress。较新 record 替换当前记录内容，撤销清空当前 note/progress；历史事件保留原记录。

成功回执包含 accepted、duplicates、events（event_id/sequence/duplicate）和本批涉及的 states。每个客户端有独立反馈 sequence，重试不递增；反馈**不会修改资料修订、来源或公开内容 changes 游标**。

## 反馈状态与事件

```http
GET /api/v1/client/feedback?limit=20&offset=0
GET /api/v1/client/feedback?item_id=<UUID>,<UUID>
GET /api/v1/client/feedback/events?after=0&limit=100
```

状态结果包括 items、total、summary、event_counts 和 high_watermark：

- summary 按资料计数：recommended、seen、read、liked、recorded、favorited。
- event_counts 按已接受事件次数计数，包括撤销动作；幂等重试不计数。
- 每个 actions 项包含 active、正向 count、最新状态时间、最近正向时间、事件 ID、公开修订及可选记录内容。
- active 表示当前状态，count 表示累计正向行为；取消喜欢不会抹掉历史发生次数。
- 事件流按反馈 sequence 升序分页，保存 next_cursor，按 has_more 继续读。它独立于 `/changes`，不要混用两个游标。
- 状态和笔记只属于此身份。服务器不会为旧资料或管理员预览编造反馈。

## 候选去重

```http
GET /api/v1/items?exclude=recommended,read&include_feedback=true&limit=20
GET /api/v1/items?unseen=true&limit=20
GET /api/v1/items?exclude=recommended&exclude_since=2026-10-01T00:00:00Z
```

exclude 支持逗号分隔：

- recommended：曾有正向推荐事件。
- seen：曾有任何正向推荐、曝光或学习行为；即便后来取消喜欢/已读，仍不叫“从未见过”。
- read / liked / recorded / favorited：只按对应**当前 active 状态**排除，撤销后该条件不再排除。

多个条件是 **OR**，任一匹配就排除。`unseen=true` 是 exclude=seen 的简写。`exclude_since` 限制反馈时间窗口，可实现推荐冷却期；撤销类状态使用最新状态时间，历史推荐/seen 使用最近正向行为时间。

排除在排序和 limit/offset 分页之前执行，不能先拿一页再本地删掉而得到稀疏页。文件和 PostgreSQL 查询使用同一语义。include_feedback 返回本页 ID 对应的状态字典，无历史时 actions 为空。

**默认无反馈参数的 GET /items 行为不变。** 使用反馈过滤或 include_feedback 需要可识别的独立 Client read Key，不接受全局/管理员身份冒充某个客户端。过滤默认按资料 ID 跨修订保留历史：资料重新发布不会自动变成“从未推荐”；需要重读可调整状态或查询时间窗口。

这是有状态候选分页，不是冻结推荐会话。反馈会使候选集合变化：每次推荐轮次从 offset=0 重新取，稳定分页期间先收集候选，再上报选定项。两个并发设备仍可能同时读到相同候选；v1 不提供预占/派单锁，客户端需自行协调并上报真实结果。

下架仍从公开查询消失，`/changes` 的删除墓碑及缓存清理规则不受影响。

## 管理界面

- 客户端行“反馈记录”：只读统计、状态与学习记录，笔记按文本转义显示。
- 数据查询页：选择模拟客户端，再选择排除条件、时间窗口；会返回该身份的公开候选及状态。未选客户端时不能套用任何个人反馈。
- 管理预览端点：`GET /admin/clients/{id}/feedback`、`/feedback/events`、`/items`；均需管理员权限，不向普通客户端暴露他人数据。

## 可复用客户端示例

`examples/feedback_client.py` 只依赖标准库，使用扫码导入后已校验的 ConnectionConfig：

```python
from feedback_client import FeedbackClient, FeedbackOutbox, make_event

api = FeedbackClient(config)  # HTTPS；本机开发需显式允许 HTTP
outbox = FeedbackOutbox(private_outbox_path, config)
candidates = api.candidates(exclude=('recommended', 'read'))
# App 自己的算法选定推荐项之后，而不是遍历所有候选上报。
chosen = candidates['items'][0]
outbox.enqueue(make_event(chosen['id'], 'recommended', item_revision=chosen['revision']))
outbox.flush(api)  # 超时/错误保留原事件，重试时 ID 不变
# 实际展示或用户点击时另建相应动作：
outbox.enqueue(make_event(chosen['id'], 'read', item_revision=chosen['revision']))
outbox.enqueue(make_event(chosen['id'], 'favorite', value=True))
outbox.flush(api)
outbox.close()
```

队列按 origin+Client ID 隔离、跨进程重开恢复；验证完整成功回执后才移除一批事件。队列文件不保存 Key，但包含资料 ID、笔记等个人学习信息，实际客户端负责 ACL/加密、生命周期、删除/导出和用户隐私告知。服务端事件历史同样需按部署策略备份与保护，不会自动删除历史记录。

本仓库仍没有实际学习 App 的摄像头/学习按钮源码；交付后端、管理界面、协议和可运行客户端接入代码，不宣称修改了外部手机 App。

## 存储与验证

PostgreSQL 仅新增迁移 `000004_client_feedback.sql`，不改 000001–000003。新增 feedback_event/state/clock 记录与索引；旧身份/Key 不迁移不扩权。升级生产 PG 前请备份并显式执行迁移，新版就绪检查会拒绝缺少反馈索引的旧 schema。

文件存储沿用现有版本与事务式落盘，新反馈记录按需写入，启动不回填历史。内容、媒体、公开快照和 changes 游标不因反馈而重写。

测试：`go test ./...`、`python -m unittest discover -s examples -v`、`python scripts/verify_feedback.py`。专用 PostgreSQL 环境未提供时实库测试仍待执行，不把文件库验证当作 PG 通过。
