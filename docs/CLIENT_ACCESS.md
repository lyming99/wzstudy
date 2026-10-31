# 独立查询客户端与录入端权限

管理端仅保留“数据录入 / 数据查询”两个一级入口。查询客户端在 **数据查询 → 查询客户端** 管理，录入来源在 **数据录入 → 录入端管理** 管理，不再共用 Source 身份或“全部权限”配置。

## 两种身份

| 身份 | 凭据 | 授权范围 |
| --- | --- | --- |
| Source 录入端 | 新建 `wzsk_` Key | ingest：自己的来源入库与状态检查；upload：自己的文件入库，必须同时有 ingest |
| Client 查询端 | 新建 `wzck_` Key | read：全部已发布资料、公开来源、详情与媒体；sync：公开内容的增量变更及正文 |
| 管理员 | 邮箱会话 / ADMIN_API_KEY | 管理身份、资料与公开状态，不能替代来源 Key 入库 |

Key 前缀不是授权依据。显式迁入 Client 的旧查询 Key 可以保留原前缀；查询认证只认独立 Client 记录，不回退尝试 Source。分类、标签及来源筛选不是租户隔离机制。

## 配置查询客户端

1. 登录 `/admin/`，进入“数据查询 → 查询客户端”。
2. 创建客户端，默认 read + sync，也可只选择其中一项或全部撤销。
3. 安全保存 Key。查询 Key 同时由服务端加密留存，所属账号可从“扫码接入”再次查看；列表/普通详情不会返回 Key。旧版仅存哈希的 Key 无法自动恢复，仍有原 Key 时可验证后补存，遗失时才需显式轮换。
4. 在客户端安全配置中使用 `Authorization: Bearer <查询 Key>`，不要放入 URL、文章、日志、公开网页或浏览器持久存储。
5. 后续请求立即按照最新权限、启用状态和 Key 校验。停用或轮换只影响该客户端，不影响任何录入端或其他客户端。

“已启用”不代表设备在线。服务端可在显式 feedback 授权后保存本客户端的推荐/曝光、已读、喜欢、学习记录及收藏反馈；不计算推荐排序。

## 扫码快速接入

新建/显式轮换的 Key 弹窗，以及客户端列表中的“扫码接入”，可在浏览器本地生成包含服务 URL、客户端 ID/名称及查询 Key 的二维码。已加密保存的 Key 会在所属账号的扫码弹窗中自动填入；旧版只存哈希的 Key 需提供原值验证补存，或在遗失时明确确认轮换。不会静默替换原 Key。加密密钥配置和备份注意事项见 `CLIENT_QR.md`。

二维码采用 `wzstudy.query_client` v1 UTF-8 JSON；客户端读取后应确认目标并调用 `GET /api/v1/client/connection`，核对真实身份后添加连接。该端点只接受启用且有 read 或 sync 的查询 Key，不接受管理员 Cookie/Key、全局只读或录入凭据。

二维码等同于 Key，没有独立过期时间。127.0.0.1/localhost 只指扫码设备自身，手机需要可达地址和正确的监听/防火墙配置，功能不会自动开放网络端口。详细协议、客户端导入代码与实际 App 的集成边界见 `CLIENT_QR.md`。

## 查询接口

| 权限 | 接口 |
| --- | --- |
| read | `GET /api/v1/sources`、`GET /api/v1/items`、`GET /api/v1/items/{id}`、`GET /api/v1/media/{id}` |
| sync | `GET /api/v1/changes?after=<cursor>` |

- read 不隐式授予 sync，sync 也不隐式授予目录/媒体读取。
- 列表支持 kind/source_id/category/tag/q/limit/offset，过滤取交集，始终返回公开快照。
- 增量响应保存 next_cursor，has_more=true 时继续补拉；空过滤页也可能推进游标。修改来源筛选集合需从 0 重建缓存。
- 客户端结合自己的学习记录和算法决定推荐顺序。服务端不提供推送日程、收件箱或个性化推荐排序。
- `examples/subscribe.py` 提供主动轮询与 SQLite 资料/游标原子提交示例，使用 WZSTUDY_CLIENT_KEY，兼容旧 READ_API_KEY 环境变量。

管理端“资料查询”使用管理员会话预览公开结果，不保存额外 Key；它不证明某个查询客户端已被正确授权。真实 Key 权限需由该客户端或隔离集成测试验证。

## 管理 API

```text
GET/POST /api/v1/admin/clients
GET/PUT  /api/v1/admin/clients/{id}
POST     /api/v1/admin/clients/{id}/key
```

创建：

```json
{"slug":"reader-desktop","name":"桌面学习端","description":"仅查询公开资料","enabled":true,"permissions":["read","sync"]}
```

创建/轮换响应为 `{ "data": { "client": { ... }, "access_key": "<仅本次返回>" } }`；服务端只保存哈希。

PUT 是完整可编辑配置替换，正文包含 slug/name/description/enabled/permissions/revision，slug 不可变。权限只接受 read/sync/feedback；feedback 必须显式授予，不改变旧默认或原有授权；创建省略/null 默认两项，更新省略/null 保留，显式 `[]` 撤销全部。

查询 Key 不能调用 `/admin/*`、`/auth/session`、来源入库或文件上传。录入 Key 不能查询资料、公开媒体或变更流。无匹配角色凭据通常为 401；已识别客户端但停用/缺少权限为 403；管理修改的过期 revision 为 409。

## 旧数据迁移

启动和页面读取不自动迁移。管理端出现“旧身份待拆分”横幅时，先备份，再查看预检：

- 只有 ingest/upload 或旧 null 的来源继续作为录入端；显式 [] 或仅 inbox 不扩权。
- 纯 read/sync 旧身份将原 Key 哈希迁入 Client，原 source_id 继续作为资料出处，不再承载认证。
- 混合读写身份必须选择旧 Key 保留给 ingest 或 query，另一侧生成新 Key，仅操作成功时显示一次。
- 所有混合选择及当前 revision 必须完整有效，整批成功或回滚，不悄悄复制同一 Key 给两类角色。
- 保留启停状态、权限子集、资料 ID、来源归属、附件和内容游标；待迁移来源禁止普通编辑/轮换以免丢弃查询归属。
- 迁移后如需回退，应恢复成套备份，不能只退回旧程序。

接口及请求体见 `MANAGEMENT.md` 的 identity-migration 章节。

`READ_API_KEY` 作为旧版全局查询凭据继续兼容。停用单个 Client 不能撤销已经泄露的全局 Key；需要独立规划它的轮换，不在本轮自动改用户凭据。

## 验证

```powershell
python scripts/verify_go.py
go vet ./...
go build -o .artifacts/wzstudy.exe ./cmd/api
python scripts/verify_two_chains.py
python scripts/verify_live.py
```

上述验证使用隔离临时工作区、随机凭据及合成资料，不操作真实库。`scripts/verify_client_access.py` 保留为两链路综合回归的兼容入口。PostgreSQL 实机测试需要专用测试库，不能指向生产数据库。


## 客户端反馈与去重

客户端可显式申请 feedback 权限，上报推荐/曝光、已读、喜欢、学习记录和收藏，按事件 ID 幂等保存。`POST /api/v1/client/feedback` 仅写本身份；`GET /api/v1/items?exclude=recommended,read` 或 `unseen=true` 可过滤旧候选，支持时间窗口和本页状态。管理员只读查看反馈或按身份模拟查询，不替用户上报。默认 read/sync 及所有旧 Key 不自动扩权。详见 `docs/CLIENT_FEEDBACK.md`（文档目录内为 `CLIENT_FEEDBACK.md`），示例为 `examples/feedback_client.py`。
