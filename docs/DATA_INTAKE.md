# 统一数据录入：采集程序 / Skill / MCP

系统只负责数据录入与客户端查询。三种录入方式共用同一套 Source 身份、HTTP API、字段校验、幂等更新、媒体登记和入库回执，不分别建设资料管理系统。

管理端入口为“数据录入”：资料管理、录入端管理、接入指引、入库记录四个内页。手动维护和 RSS 属于辅助录入能力，不是第三条业务主线。

## 建立录入端

在“录入端管理”创建来源，选择接入方式 collector/skill/mcp；该字段只标记来源，不改变权限。外部接入的 kind 仍为 manual，RSS 则为 rss。

- 新 Key 前缀 wzsk_，仅创建/轮换时显示一次，服务端只存哈希。
- 默认权限 ingest + upload，支持只授予 ingest 或显式撤销全部；不能授予 read/sync。
- 文件入库必须同时具有 ingest/upload。录入 Key 不可查询资料库或访问管理 API。
- 默认 auto_publish=false；已有来源以实际策略为准。停用来源不自动下架存量资料。
- 若提示旧身份待拆分，应先完成备份和显式迁移，不能用编辑来源丢弃旧查询权限。

## 采集程序

```text
GET  /api/v1/ingest/{source_id}/status
POST /api/v1/ingest/{source_id}
POST /api/v1/ingest/{source_id}/files
Authorization: Bearer <录入来源 Key>
```

status 只读检查自己的启用状态、发布策略和有效录入权限，不写资料或来源。

JSON 正文为 `{ "items": [Content, ...] }`，单批 1–100 条、最多 4 MiB，整批成功或回滚。使用稳定 external_id：同一 source_id + external_id 更新同一资料，内容相同返回 unchanged，不增加资料修订。

文件使用 multipart 的 metadata 与 files；服务端校验实际 MIME、类型、数量、大小及来源资产归属。登记外链 URL 不等于实际下载或上传音视频。参数约束见 `API.md`。

RSS 在录入端管理中配置公开 RSS/Atom 地址与 60–86400 秒的间隔；自动采集需服务运行且 POLL_ENABLED 开启。支持立即采集和去重，拒绝内网、回环、保留地址及不安全跳转。

## Skill

沿用 `skills/wzstudy-push` 的目录及 push 命令；这里的 push 仅表示入库，不表示主动分发给学习客户端。

```powershell
python skills/wzstudy-push/scripts/wzstudy_push.py --config <私有来源配置> doctor
python skills/wzstudy-push/scripts/wzstudy_push.py push --input <资料JSON> --dry-run
python skills/wzstudy-push/scripts/wzstudy_push.py --config <私有来源配置> push --input <资料JSON>
```

私有配置包含 base_url/source_id/source_key。Skill 不使用查询客户端 Key 或管理员密码；不更改来源策略、审核发布或读取全局资料库。新建专用来源建议 integration=skill。

单批原子，多批请求不是整体事务。网络中断/5xx/非 JSON 可能意味着提交状态不确定，先核对已完成回执，不自动重试写入，尤其不要反复上传媒体。

## MCP

适配器入口：`mcp/wzstudy_ingest/server.py`，使用相同私有来源配置和共享 Python HTTP 客户端。新建专用来源建议 integration=mcp。

- 默认只提供 `wzstudy_ingest_status`、`wzstudy_ingest_items`。
- 启动时显式设置 `--allow-root` 后才提供 `wzstudy_upload_files`；解析后的文件路径必须在允许目录内。
- 不提供资料查询、审核、身份管理、任意 HTTP、任意文件读取或外链下载工具。
- 工具参数不能覆盖 endpoint/source_id/source_key/config；密钥仅在进程启动时从明确配置加载。
- 一个工具调用最多提交一个 100 条/4 MiB 的原子批次；支持离线 dry_run。
- stdio 支持现代按请求元数据及旧版初始化流程；具体支持范围、运行方式与协议参考见 `mcp/wzstudy_ingest/README.md`。

页面只提供无真实 Key 的配置模板，不自动写入 AI 应用的 MCP 设置，不把“模板已展示”误报为“已连接”。

## 公开可见性与回执

成功响应保留 created/updated/unchanged/published/items，新增 receipt_id/source_id/received_at；每项资料包含真实 ID、revision 和状态。

“入库记录”调用 `/api/v1/admin/intakes`，按来源及分页查看已提交回执。回执与资料同事务保存，不保存正文、标题或 Key；失败/回滚批次及历史操作不会伪造成成功记录。状态是提交时快照，后续审核可能改变可见性。

新来源默认待审核，管理员可修正、提交、公开、下架。客户端只通过独立 Client Key 查询公开快照，学习与推荐逻辑不进入录入服务。
