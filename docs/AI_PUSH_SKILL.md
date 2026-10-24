# AI 采集资料入库 Skill

## 已交付

`skills/wzstudy-push/SKILL.md` 是可复用 Skill，包含采集、出处核对、摘要整理、预检、资料/媒体入库与错误处理流程。实现不依赖第三方 Python 包，Python 3.10+ 即可运行。

| 文件 | 作用 |
| --- | --- |
| `skills/wzstudy-push/SKILL.md` | AI 的触发条件和执行规则 |
| `skills/wzstudy-push/scripts/wzstudy_push.py` | 来源专属客户端，支持 doctor / push / upload |
| `skills/wzstudy-push/references/API.md` | 资料和媒体入库契约 |
| `skills/wzstudy-push/references/SETUP.md` | 一次性来源配置与安全约定 |
| `skills/wzstudy-push/examples/` | 只供格式/预检的示例，不能当真实资料自动入库 |
| `scripts/setup_ai_push.py` | 用户授权下的一次性管理配置工具 |
| `scripts/verify_push_skill.py` | 隔离真实 Go 服务验收 |

## 本地接入状态

已为本系统创建独立的「AI 资料采集」来源（slug `ai-collector`），启用且 `auto_publish=false`。真实来源凭据仅保存在根 `.local/ai-push-client.json`，已限制 Windows ACL，并由 Git/Docker 忽略规则排除。Skill 目录和可分发 ZIP 不含实际来源密钥、管理员密码、管理 API key 或会话 Cookie。

入库默认进入待审核区，而不是直接公开。管理员以后修改来源发布策略时，以实际响应 `published` 数量为准。

本次验证没有把测试文章或媒体写入真实资料库。10 项单测和 8 组真实接口验收使用独立临时工作区；本地真实库只完成专用来源配置和只读认证检查。

## 如何让 AI 使用

可以直接给 AI 这样的指令：

> 使用 wzstudy-push，采集 5 份关于 Go 并发入门的公开学习资料，整理原创学习摘要并标注出处，提交到学习系统待审核，不自动发布。

或者：

> 使用 wzstudy-push，把我提供的这篇 Markdown 和音频文件入库，保留原作者及来源，先预检。

没有主题、链接或文件时，Skill 应先确认范围，不任意填充资料。采集使用 AI 自身已授权的网页/文件工具；客户端脚本不自动抓取网页、不下载外部媒体、不绕过登录或付费墙。

## 手动调用

在仓库根目录运行：

```powershell
# 只读检查，不提交资料
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json doctor

# 预检不联网、不需要密钥
python skills/wzstudy-push/scripts/wzstudy_push.py push --input .artifacts/collected-items.json --dry-run

# 提交经授权、核对过的真实资料
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json push --input .artifacts/collected-items.json

# 上传一个音频/视频/文章，或用多个 --file 传入轮播图片
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json --timeout 300 upload --metadata .artifacts/audio-metadata.json --file .artifacts/lesson.wav
```

从其他目录使用时，脚本、配置、输入文件应使用各自正确的绝对路径。也可使用 `WZSTUDY_CONFIG`；或完整配置 `WZSTUDY_SOURCE_ID`、`WZSTUDY_SOURCE_KEY`，可选 `WZSTUDY_BASE_URL`。

不要将密钥放在命令参数里，也不要让 AI 打印配置内容。

## 可靠性与边界

- 文章默认按来源 URL 生成稳定外部 ID；无 URL 时按内容生成。要稳定更新无 URL 的资料，应显式给 `external_id`。
- 每批最多 100 条且最多 4 MiB，自动拆批；每批原子，多个批次可能部分成功。
- 本地媒体默认 200 MiB，总图片数最多 20。上传使用流式临时文件，不把整个大文件读入内存。
- 写入不自动重试；超时/5xx 可能已经提交，先核对状态。媒体重传会产生新的不可变资产/修订，不是无副作用重试。
- 拒绝重定向、关闭 TLS 校验、远程明文 HTTP，以及把绑定配置的密钥改发其他地址。
- Skill 不审批、不发布、不改来源策略、不创建客户端推送计划。资料入库与最终向客户端投递是两个阶段。
- 原始出处必须核对，不编造作者或日期；版权不明确时使用原创学习摘要和短引用，不整篇复制。

## 测试

```powershell
python -m unittest discover -s skills/wzstudy-push/tests -v
python scripts/verify_push_skill.py
```

真实服务验收需要根 `.artifacts/wzstudy.exe`。输出报告是 `.artifacts/push-skill-verification.json`。测试不读取真实用户密码，不调用真实来源写资料。
