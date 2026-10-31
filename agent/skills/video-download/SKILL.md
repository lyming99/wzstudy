---
name: video-download
description: 用 yt-dlp 下载视频/音频并上传资产，完成视频/音频任务入库
when_to_use: 任务数据类型为 video 或 audio，或目标要求保存视频/音频文件时
---

# 视频 / 音频采集（yt-dlp）

适用：任务数据类型为 `video` 或 `audio`，或目标明确要求保存视频、音频文件。

## 流程

1. 可用 `fetch_url` 查看页面，确认标题、作者与发布时间。
2. 调用 `download_media`：
   ```json
   {"url": "https://www.bilibili.com/video/BV...", "format": "video"}
   ```
   返回本地路径、标题、时长与大小：
   ```json
   {"path": "C:\\...\\wzstudy-media-xxx\\标题.mp4", "title": "...", "size": 12345678, "format": "video"}
   ```
3. 调用 `upload_asset` 上传文件：
   ```json
   {"path": "C:\\...\\wzstudy-media-xxx\\标题.mp4"}
   ```
   返回资产：`{"id":"...","name":"标题.mp4","mime":"video/mp4","size":12345678}`。
4. 调用 `ingest_items`，条目必须包含：
   - `kind`：`video` 或 `audio`
   - `external_id`：视频 ID（如 BV 号）或稳定页面 ID
   - `title`、`summary`、`url`
   - `assets`：上一步返回的资产对象数组
5. 调用 `finish_task` 结束。

## 约束

- 视频任务必须提交视频文件资产；音频任务必须提交音频文件资产，否则服务端拒绝入库（`content_type_mismatch`）。
- 下载受 `max_bytes` 限制（默认 200MB，可用任务预算调整）。
- 只下载任务允许的域名，不要绕过登录、付费墙或版权限制。
- 未安装 yt-dlp 时 `download_media` 会提示；安装后加入 PATH，或设置 `AGENT_YTDLP_PATH` 指向可执行文件。
- 下载文件位于系统临时目录的 `wzstudy-media-*` 下，`upload_asset` 只接受该目录中的文件。
