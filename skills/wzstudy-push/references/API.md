# WzStudy 客户端入库契约

本项目 v0.3.0 接口；完整契约在项目 `docs/API.md`、`docs/MANAGEMENT.md`、`docs/openapi.json`。运行时可读服务 `/docs`、`/docs/management`、`/openapi.json`，不要据此读取管理员凭据。

## 权限

- 日常入库：`Authorization: Bearer <来源专属 wzsk_ 密钥>`，仅能操作自己的来源。
- 管理员密钥、邮箱密码及只读密钥不能代替来源密钥入库。
- `doctor`：`GET /api/v1/health/ready` 加 `GET /api/v1/clients/{source_id}/inbox?after=0&limit=1`，只验证连接与来源权限，不提交资料，也不修改客户端游标。

## 文章 / 外部链接

`POST /api/v1/ingest/{source_id}`，`application/json`：

```json
{
  "items": [{
    "external_id": "example:stable-content-id",
    "kind": "article",
    "title": "真实资料标题",
    "summary": "AI 原创整理的简短摘要",
    "content": "# 学习要点\n\n这里是授权范围内的整理正文。\n\n## 出处\n\n附真实来源链接并注明非全文。",
    "format": "markdown",
    "url": "https://example.org/source",
    "author": "仅填写确认过的原作者",
    "category": "学习方法",
    "language": "zh-CN",
    "tags": ["示例"]
  }]
}
```

示例不是实际采集结果，不直接提交到用户真实库。

| 字段 | 服务端约束 |
| --- | --- |
| 请求 | 1–100 条，最多 4 MiB UTF-8，单批全成或全败 |
| external_id | 非空，最多 512 字节，同一来源内唯一 |
| title | 非空，最多 500 字节 |
| summary / content | 最多 8000 / 262144 字节 |
| format | text / markdown |
| kind | article / audio / video / carousel |
| author / category / language | 最多 200 / 200 / 35 字节 |
| tags | 最多 20 个，各最多 80 字节 |
| url | 可空，HTTP(S)，最多 2048 字节，无用户名密码和 fragment |
| published_at / source_updated_at | 可省略；真实 RFC3339 时间，不编造原站日期 |

`kind` 为媒体且没有资产时必须有有效外部 URL；登记 URL 不表示下载文件。无出处 URL 的文章使用内容哈希 ID，正文变化会生成新 ID；要持续更新同一资料请显式指定稳定 ID。

响应：`{"data":{"created":1,"updated":0,"unchanged":0,"published":0,"items":[...]}}`。相同来源、external_id 和相同规范化内容返回 unchanged，不增加 revision。客户端跨多批可能部分成功，保留批次回执。

## 本地媒体

`POST /api/v1/ingest/{source_id}/files`，multipart/form-data：

- `metadata`：单个 Content JSON，至少含 title；建议显式 external_id / kind。
- `files`：一个音频/视频/文章文件，或 1–20 张轮播图片。

元数据示例：

```json
{"external_id":"lesson:audio-001","title":"授权的音频课程","kind":"audio","category":"语言学习","language":"zh-CN","tags":["课程"]}
```

服务端生成资产 ID、路径、MIME 与大小，不接受伪造登记；文本文件须 UTF-8。支持常见 MP3/WAV/M4A/OGG/FLAC、MP4/MOV/WebM、TXT/Markdown、PNG/JPEG/GIF/WebP，不接受 SVG/HTML。浏览器能否播放取决于原始编码，系统不转码。

## 发布与失败

新专用来源默认 `auto_publish=false`，正常入库为 pending。若管理员以后修改来源策略，响应可能 published>0，必须据实报告。Skill 不调用 `/admin/items/.../publish`、不更改来源配置、不创建客户端投递计划。

- 401：密钥或来源不匹配，停止，不回显密钥。
- 403：来源停用，通知管理员，不绕过。
- 409：版本/状态冲突，不盲目覆盖；删除的外部 ID 不能自动复活。
- 413：检查正文/批次/媒体大小。
- 5xx、网络中断、非 JSON：写入结果可能不确定，确认系统状态后再决定重试；媒体重传可能新增资产与修订。
- 3xx：客户端拒绝跟随，不把来源凭据转发给新地址。
