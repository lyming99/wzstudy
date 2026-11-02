# 五种学习模式与客户端接入

## 独立内容模式（当前规则）

内容增加 `content_mode`，取值 `default`（默认）、`news`（资讯）、`learning`（学习），与 `category` 主题分类完全独立。例如可同时设置 `category="人工智能"`、`content_mode="news"`。

- 旧资料及未指定模式的新资料视为默认；默认模式可省略 JSON 字段，保留既有内容哈希，不批量修改旧记录。
- 资讯队列只依据内容模式：默认 + 资讯进入；学习不进入。不根据主题分类、合集归属猜测模式。
- 合集、路线、随机、复习仍按各自原有规则读取，并不因内容标记为学习而自动创建编排或复习计划。
- 管理编辑和链接登记可选择模式；资料菜单、资料批量栏及数据查询多选栏提供独立「设置模式」。快捷设置只更新模式元数据，即时同步公开查询投影，不公开待审核标题、正文或附件，也不覆盖分类、ID、附件和学习记录。
- `POST /workspace/items/organize` 支持 `content_mode`，与 `category/collection_id` 可组合，仍为原子批次。普通正文编辑仍遵循既有审核流程。
- `GET /items`、`GET /workspace/items` 支持独立 `content_mode=default|news|learning` 筛选，不传表示不限模式。
- 旧采集端及旧管理端更新资料时，未传模式保留已有选择；显式传 `content_mode="default"` 才重置为默认。不要通过缺省或清空主题分类来修改模式。

```json
{"title":"AI 行业动态","content":"正文","category":"人工智能","content_mode":"news"}
```


更新日期：2026-10-31。

## 身份与权限

- 新建查询客户端省略 `permissions` 时默认 `read + sync + feedback`，后台默认预设同步调整。
- 显式只读客户端仍是只读；旧记录不自动扩权。需要上报时，在「查询客户端 → 配置权限」启用学习反馈，也可登录学习用户账号。
- 获得 `feedback` 的工作区客户端 Key 可以访问 `/client/learning/*`、`/client/feedback` 和推荐接口。学习状态按 **工作区 + Client ID + 资料 ID** 隔离；共享同一 Key 的设备共享该客户端状态。客户端状态不会合并到拥有者用户会话。
- 用户会话继续按用户稳定 ID 保存学习状态。Cookie 写请求继续要求同源 Origin；显式 Authorization 优先于 Cookie。
- 查询模式只返回当前工作区的已发布资料，不会返回草稿、删除资料或其他工作区的内容。查询本身不生成学习事件。
- 管理、录入、文件上传权限不随学习权限授予。旧无所有者的数据不自动迁入用户或客户端工作区。

## 模式分类与内容查询

基础路径为 `/api/v1`；认证为当前用户会话或所属工作区的查询 Key。

```text
GET /client/learning/modes/categories?mode=news&limit=100&offset=0
GET /client/learning/modes/categories?mode=collection&limit=100&offset=0
GET /client/learning/modes/categories?mode=route&limit=100&offset=0

GET /client/learning/modes/items?mode=news&limit=30&offset=0
GET /client/learning/modes/items?mode=collection&collection_id=<合集UUID>
GET /client/learning/modes/items?mode=route&roadmap_id=<路线图UUID>
GET /client/learning/modes/items?mode=route&roadmap_id=<路线图UUID>&node_id=<节点UUID>
GET /client/learning/modes/items?mode=review
GET /client/learning/modes/items?mode=random&seed=42&limit=30&offset=0
```

查询还可带 `q`。分页 `limit=1..100`，`offset=0..1000000`。未知、重复参数和伪造身份选择参数会被拒绝。

| 模式 | 分类/选择数据 | 内容语义 |
|---|---|---|
| 资讯 `news` | 未分类资料的数量（固定项） | 直接查询全部未分类或分类为“资讯”的资料，按发布时间倒序，不再要求选择资讯分类 |
| 合集 `collection` | 现有后台保存的合集列表 | 按当前合集的章节顺序；下架、草稿和删除资料不返回 |
| 路线 `route` | 现有工作区的路线图列表 | 按根节点到子节点、同级既定顺序去重读取资料；节点折叠状态不截断学习内容。可选单个节点子树 |
| 复习 `review` | 无额外分类 | 仅当前学习身份的有效且已到期计划，按下次时间排序；需要学习数据权限 |
| 随机 `random` | 无额外分类 | 查询当前工作区全部已发布候选，未读优先。已查看、有进度或明确已读均作为已读依据 |

分类响应 `data` 为 `{items:[{id,title,summary,count}], total, limit, offset}`。内容响应为 `{items:[PublicItem], total, limit, offset, has_more, unread_item_ids}`。

随机顺序由 `seed` 确定；服务端保存身份绑定、仅含 ID 的十分钟内存快照。同一轮中上报已读不会把后续分页边界移动；切换一轮使用新 seed。快照不跨服务重启，也不改变数据库游标。已下架/删除资料始终不会因旧快照重新暴露。

合集与路线图的新增、编辑、删除、章节/节点资料管理沿用 `/workspace/catalog/collections/*`、`/workspace/roadmaps/*` 与已有后台，仍使用 revision 冲突保护。合集创建或修改后立即生效，无需发布；合集与路线图中的关联资料仍只有已发布部分可读。

## 周期复习

添加默认计划：

```json
{"events":[{"event_id":"<事件UUID>","item_id":"<资料UUID>","action":"review_plan","value":true}]}
```

用户自定义间隔：

```json
{"events":[{"event_id":"<事件UUID>","item_id":"<资料UUID>","action":"review_plan","value":true,"review_intervals_minutes":[20,60,1440,4320,10080]}]}
```

每次完成复习上报真实时间：

```json
{"events":[{"event_id":"<事件UUID>","item_id":"<资料UUID>","action":"review","occurred_at":"2026-10-31T10:00:00Z"}]}
```

- 默认间隔为分钟序列 `[20,60,540,1440,2880,5760,10080,21600,43200]`，是产品采用的递增复习安排，不宣称是个体最优的医学/学习效果方案。
- 自定义最多 30 项，每项为 `1..525600` 的整数分钟；用完后循环使用最后一项。
- 新计划首次即可进入复习；每次完成后以 `occurred_at`（省略则为服务端接收时间）加本阶段间隔，计算 `next_review_at`。
- `review_active` 表示计划仍有效；`review_pending` 保留旧版一次性待办语义。完成一次后 `review_pending=false`，但 `review_active=true` 的周期计划仍会在下次到期被查询。
- `review_step` 和 `review_intervals_minutes` 随状态保存。重复请求必须复用同一事件 ID 与原始载荷，不会重复计数或推进计划。
- 对已有有效计划再次提交不带自定义间隔的“加入复习”，不会提前下次时间。显式设置新周期从第一阶段重新安排。
- 取消用 `review_plan + value=false`，清除下次时间，不删除历史复习事件。

客户端「更多操作」提供默认加入复习、自定义周期和完成复习；远程计划不再创建本地待办。显式本地资料仍使用原有本地待办逻辑。

## 内容字段

资料输入、保存、发布和公开查询均保留以下可选字段；后台资料编辑器可维护原始平台、大纲以及视频章节/字幕 JSON。

```json
{
  "platform":"哔哩哔哩",
  "duration_ms":300000,
  "orientation":"landscape",
  "outline":"# 内容大纲\n\n- 问题背景\n- 方法与练习",
  "chapters":[{"id":"intro","title":"简介","start_ms":0}],
  "subtitles":[{
    "id":"zh-CN",
    "language":"zh-CN",
    "format":"srt",
    "content":"1\n00:00:00,000 --> 00:00:02,000\n字幕正文"
  }]
}
```

`orientation` 为 `landscape/portrait`；章节时间非负、严格递增且不能超出已知时长。视频章节最多 300 个，字幕最多 8 轨，字幕格式为 SRT 或 WebVTT；单段大纲或字幕正文最大 262144 UTF-8 字节。`chapters/subtitles` 支持音频和视频（2026-11-01 更新）。未提供平台名时，服务端按原始 URL 的真实 hostname 识别已知平台，不信任 URL 路径中的平台字样。

客户端：

- 喜欢和收藏保留交互与选中状态，不显示次数，也不为显示这些次数额外请求统计。
- 来源优先展示真实平台名；旧缓存可从原始链接推断，不把采集系统名称作为平台。
- 无大纲/字幕时隐藏相应入口，有数据时可查看大纲、跳转章节、切换字幕。
- 内联字幕随资料缓存，不虚构可下载附件 ID。
- Markdown 通过 `wenz_richtext` 的 MarkdownCodec 与只读编辑器渲染；图片仍经原有受控资源层，不将查询 Key 放进内容或链接。
- 横屏视频的居中布局基于实际顶部覆盖高度计算，不再使用固定 Alignment 偏移。

## 升级与验证

先升级服务端，再升级客户端。旧服务若没有模式接口，客户端会显示读取失败，而不是把缓存排序伪装成服务端模式查询。仅修改源码不代表线上已部署；部署、运行服务重启和移动端安装包发布需要另行执行。

当前不自动扩权旧客户端、不自动归属旧无主数据，也不改动生产数据库或现有服务进程。Web 播放器、歌词/翻译/单句循环与共享渠道的后续实现见 `LEARNING_OPTIMIZATION.md`。


## 2026-10-31 补充：合集与资料整理

- 合集不再有独立的发布/下架流程。创建、更新、移动资料后立即更新查询投影及合集增量游标；旧发布/下架兼容路径返回 410。
- 启动时仅升级已有所有者的旧合集，令其最新定义生效；不认领无主档案，不公开资料草稿正文，升级重复执行不会反复产生事件。
- `POST /workspace/items/organize` 支持单条或最多 100 条原子整理：`items:[{id,revision}]`，可带 `category`、`collection_id`、`collection_revision`。
- `category` 留空仅清空主题分类；省略则不改分类。（旧规则已被独立内容模式替代），合集归属和资料分类是两项独立属性，不会在移动时擅自覆盖分类。
- `collection_id` 为目标合集 UUID，必须带该合集当前 revision；空字符串表示移出全部合集，省略表示不调整合集关系。移动将先移除原合集关系，再追加到目标合集；保留已在目标中的章节身份与顺序。
- 分类设置只改变分类元数据，不会顺带公开尚未审核的正文、标题或附件；资料 ID、附件及学习历史保留。单纯移动合集不改变资料内容修订号，避免无故使阅读进度过期。
- 管理端资料菜单、资料多选栏、数据查询多选栏均提供移动到合集和设置分类。目标容量、所属工作区和修订号会在同一事务中校验，失败不做部分修改。

- 2026-10-31 规则澄清：旧的按分类规则已废弃，改为默认和资讯内容模式统一倒序读取；不会为满足查询而清空既有分类。
