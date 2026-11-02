# wzstudy-agent

独立的采集端进程：从 WzStudy 服务端领取收集任务，用 agent loop + function calling 完成抓取、整理与入库。

完整协议见 `../docs/AGENT_PROTOCOL.md`，部署与运维见 `../docs/AGENT_COLLECTION.md`。

## 快速开始

```powershell
go build -o wzstudy-agent.exe ./cmd/wzstudy-agent
```

配置可以直接写在当前目录的 `agent.env`（KEY=VALUE），再用启动脚本运行；环境变量优先于文件，`AGENT_CONFIG` 可指定其他路径。

```powershell
.\wzstudy-agent.exe doctor   # 不写数据的自检
.\wzstudy-agent.exe once     # 执行一个任务
.\wzstudy-agent.exe run      # 常驻轮询
```

## 命令

| 命令 | 说明 |
|---|---|
| `run` | 常驻：claim → 执行 → 上报 → 完成；空闲按 `AGENT_POLL_SECONDS` 轮询 |
| `once` | 领取并执行一个任务后退出，适合容器 job / cron |
| `doctor` | 校验配置、服务端连通性与权限，不调用模型、不写数据 |
| `version` | 打印版本 |

## 结构

```
cmd/wzstudy-agent      命令入口与依赖装配
internal/config        环境变量配置与校验
internal/serverapi     服务端协议客户端（重试、错误契约）
internal/worker        claim/心跳/完成/释放状态机
internal/loop          agent loop、预算、重复检测、事件上报
internal/provider      OpenAI 兼容 Provider（tools + tool_calls）
internal/tool          工具定义/调用/结果/注册表
internal/collect       fetch_url、fetch_subtitle、媒体下载/上传、ingest_items 等
internal/extract       HTML → 标题/描述/正文
internal/skill         SKILL.md 扫描与按需加载
skills/                随包分发的技能
scripts/e2e.py         端到端联调脚本
```

## 设计边界

- 只写不读：不查询资料库、不发布、不改来源配置。
- 不执行命令：没有 shell/python 执行能力，工具面固定为抓取、入库、加载技能、结束。
- 抓取只允许公网 http(s)；私网、回环、链路本地地址默认拒绝，可用 `AGENT_ALLOW_PRIVATE_NETWORK=true` 仅在本机测试中放开。
- 模型密钥只存在于采集端；服务端只看到任务、进度与条目。

## 可选字幕上传

AI 可以自主决定是否获取和上传字幕，不要求每份音视频都有字幕。`fetch_subtitle` 获取公开 SRT/WebVTT 文件，保留完整正文并返回本任务 `subtitle_ref`；这一步尚未上传。AI 在 `ingest_items.subtitles` 中选择引用 ref，或直接提交真实的内联字幕正文后，字幕才随音视频条目上传关联；不提交 `subtitles` 就不上传。

支持每份资料最多 8 轨、单轨 256 KiB。没有字幕或获取失败时仍可完成媒体采集；不自动转录、生成、翻译字幕，也不绕过来源访问限制。完整工具用法见 `skills/video-download/SKILL.md`。此改动不新增管理工作台的本地字幕文件选择按钮，也不改变发布审批。

## 测试

```powershell
go test ./...
python scripts/e2e.py   # 需要先能构建 ../cmd/api
python scripts/e2e_subtitles.py  # 隔离服务 + 实际 Agent + 模拟模型，验证可选字幕闭环
```
