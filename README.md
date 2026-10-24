# WzStudy · 知序学习资料管理系统

接收客户端上传的文章、音频、视频和卡片轮播，管理 RSS 来源、审核发布、专题合集及即时/定时投递。管理页面由 Go 内嵌提供，正式入口为 `/admin/`，无需单独部署前端。

## 快速启动（Windows）

在仓库根目录运行：

```powershell
.\start-local.ps1 -InitializeAdmin
```

打开 `http://127.0.0.1:8091/admin/`。首次在本机终端配置管理员邮箱，并隐藏输入/确认密码；随后使用邮箱和密码登录。初始化完成后，重启只需 `./start-local.ps1`。不会预置公开账号或密码。文件存储是单进程本地开发模式，不是浏览器演示数据。

- 本地工作区：`.local/data`，包括 `workspace.json` 和 `uploads`。
- 本地 API 密钥：`.local/access-keys.json`（不是网页登录密码），不要提交、公开或随发行包分发。
- 已有工作区在本次目录整理中保留原密钥和访问权限；未重新生成密钥。
- 如果旧工作区仍位于 `internal/.local`，启动器会自动沿用；新旧两处同时有工作区时必须显式指定 `-ProfileDirectory`，不会擅自选取或覆盖。
- 默认从源码构建到 `bin/wzstudy-dev.exe`；`-SkipBuild` 使用 `dist` 中的便携可执行文件。启动开发版不会覆盖发行包。
- 已运行的端口会被拒绝重复启动。不要让两个文件存储进程共用同一数据目录。

只检查路径，不启动、不读取密钥：

```powershell
.\start-local.ps1 -Check
```

## 工程结构

下图是源码仓库；便携 ZIP 仅包含运行文件与文档，不包含 Go 源码。

```text
wzstudy/
├─ cmd/                     # api 服务、数据库迁移命令
├─ internal/                # 私有 Go 包
│  ├─ adminui/              # 内嵌管理界面 HTML/CSS/JS
│  ├─ config/               # 配置及校验
│  ├─ feed/                 # RSS/Atom 采集
│  ├─ httpapi/              # HTTP、认证、上传与管理接口
│  ├─ study/                # 领域逻辑、文件/内存/PostgreSQL 存储
│  └─ start-local.ps1       # 旧路径兼容入口，不保存第二套数据
├─ migrations/              # 不可改写已应用的数据库迁移
├─ docs/                    # API、运行指南和验证记录
├─ scripts/                 # 启动、验证和发行工具
├─ design/                  # 独立 HTML 设计原型，不连接正式服务
├─ examples/                # 客户端对接示例
├─ bin/                     # 开发构建输出，已忽略
├─ dist/                    # 发行包、校验清单，已忽略
├─ .artifacts/              # 隔离测试日志、截图和报告，已忽略
├─ .local/                  # 本地密钥与持久工作区，已忽略
└─ start-local.ps1           # 根目录统一入口
```

Go 模块根目录就是本目录；`internal` 中的 Go 包和 `embed` 资源没有搬迁，API 路径与业务数据格式保持不变。

## 启动模式

| 入口 | 模式 | 用途 |
| --- | --- | --- |
| `./start-local.ps1` | file | 默认本地持久工作区 |
| `./scripts/start-dev.ps1` | file | 与根入口相同，不再默认为易失内存 |
| `./scripts/start-dev.ps1 -Memory` | memory | 明确选择的临时测试，停止后元数据丢失 |
| `./scripts/start-dev.ps1 -Postgres` | postgres | 使用根 `.env` / 环境变量，先迁移再运行 |
| `./internal/start-local.ps1` | file | 旧入口兼容；相对 `-DataDirectory` 仍相对 `internal` |

`-ProfileDirectory` 指定密钥/默认数据的工作区；`-DataDirectory` 只覆盖数据位置。新入口中的相对路径以仓库根目录为基准。已有数据缺少密钥时会拒绝自动生成新密钥。

## 构建与验证

```powershell
go build ./...
go vet ./...
python scripts/verify_go.py
python scripts/verify_structure.py
```

管理员真实初始化/登录验证使用 `python scripts/verify_admin.py`（先构建 `.artifacts/wzstudy.exe`），不会读取真实账号。

真实浏览器回归需要 Python Playwright、本机 Chrome 和用于生成样本的 FFmpeg：

```powershell
New-Item -ItemType Directory -Force .artifacts | Out-Null
go build -o .artifacts/wzstudy.exe ./cmd/api
python scripts/verify_live.py
```

PostgreSQL 用例默认跳过；使用专用测试数据库配置 `TEST_DATABASE_URL` 后运行 `python scripts/verify_go.py --postgres`。不要指向生产数据库。

## Docker / PostgreSQL

根目录 `docker-compose.yml` 配置 PostgreSQL 与 API；API 附件使用独立的 `api_data` 持久卷，容器中的 `DATA_DIR=/var/lib/wzstudy` 已准备非 root 写权限。数据库备份必须与附件卷保持一致时间点。根及旧层级的密钥、工作区和测试产物都从 Docker 构建上下文排除。

先在本机安全配置根 `.env` 中的数据库密码和两类随机密钥，再运行 `docker compose up --build -d`。生产模式必须使用 PostgreSQL 与 HTTPS 反向代理。多实例还需共享附件卷及浏览器管理会话的粘性路由。

结构核查和本地文件模式测试不等于 PostgreSQL/容器已通过实机运行验证；具体已测范围见验证记录。

## AI 采集与入库 Skill

已提供 `skills/wzstudy-push/SKILL.md`，支持 AI 整理文章/摘要并批量入库，或上传音视频与轮播；使用独立来源密钥，默认待审核，不自动发布。说明见 [AI 入库 Skill](docs/AI_PUSH_SKILL.md)。

```powershell
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json doctor
python skills/wzstudy-push/scripts/wzstudy_push.py push --input .artifacts/collected-items.json --dry-run
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json push --input .artifacts/collected-items.json
```

私有来源配置不随 Skill 分发，不要将管理员凭据用于日常 AI 入库。没有具体主题/资料时，Skill 应先确认范围。AI 入库不等于向客户端创建投递计划。

### 哔哩哔哩视频采集 Skill

新增 [bilibili-collect](skills/bilibili-collect/SKILL.md)，接收用户提供的 BV/av 号、单视频链接和 b23.tv 分享短链，支持指定分 P、定量批次、真实标题/UP 主/发布时间/时长、稳定去重 ID，以及明确授权后的 MP4 下载和可访问中英字幕保存。

默认**只采集信息与原始链接**，不下载、不入库、不发布、不自动抓取全集/UP 主空间。输出的 `items.json` / 单条 `metadata.json` 可以交给 `wzstudy-push` 预检，再按独立授权登记链接或上传实际完成的视频。下载/字幕依赖原站正常访问，不绕过登录、版权、地区或风控限制；字幕不等于自动语音转写。

```powershell
# 只检查本机依赖，不联网、不读取 Cookie
python skills/bilibili-collect/scripts/bilibili_collect.py doctor

# 离线校验示例；BV0000000000 是占位符，真实采集前必须换成用户提供的视频
python skills/bilibili-collect/scripts/bilibili_collect.py collect BV0000000000 --dry-run

# 离线单测与本地合成音视频集成测试，不写入正式学习资料库
python -X utf8 -m unittest discover -s skills/bilibili-collect/tests -v
```

依赖安装、参数与回执说明见 [使用说明](skills/bilibili-collect/references/USAGE.md)。真实提取需要 Python 3.11+ 与 yt-dlp；下载合并还需要 FFmpeg/ffprobe。仅离线预检不需要第三方依赖。

### 按分区、tag、关键词与日期发现视频

另提供 [bilibili-discover](skills/bilibili-discover/SKILL.md)，用于还没有具体 BV 链接时自动获取有界候选，并逐条核实发布时间、分区及真实标签，导出视频数据 JSON/CSV、来源链接和兼容入库的 `items.json`。支持 all/any 条件、北京时间日期/滚动窗口、跨次 BV 去重与显式统计刷新；仅授权后衔接 `bilibili-collect` 下载或保存字幕。

```powershell
# 离线依赖检查
python skills/bilibili-discover/scripts/bilibili_discover.py doctor

# 示例条件仅供预检，不会联网或实际采集
python skills/bilibili-discover/scripts/bilibili_discover.py run --config skills/bilibili-discover/examples/task.json --dry-run

# 隔离筛选/预算/状态/接口模拟测试，不访问真实B站视频或正式资料库
python -X utf8 -m unittest discover -s skills/bilibili-discover/tests -v
```

数据筛选只用 Python 3.11+ 标准库，并复用相邻采集器的字段规则；支持用户授权的 JSON 导入后离线筛选。旧 `tid`、新 `tid_v2` 与准确分区名称不混用/不猜上下级；仅新分区或名称时还需关键词/tag 提供候选，或导入已有数据。网页接口可能变化或受限，有限分页不代表日期范围全量；不绕过签名/登录/风控，不伪造设备标识，不自动安装定时任务。配置、导入格式与回执说明见 [条件采集使用说明](skills/bilibili-discover/references/USAGE.md)。

## 文档与边界

- [系统管理员初始化与邮箱登录](docs/ADMIN_AUTH.md)
- [运行、配置与备份](docs/RUNNING.md)
- [HTTP API](docs/API.md) / [管理与推送契约](docs/MANAGEMENT.md) / [OpenAPI](docs/openapi.json)
- [目录职责与兼容规则](docs/PROJECT_LAYOUT.md)
- [邮箱认证验证记录](docs/VERIFICATION_AUTH.md)
- [管理版验证记录](docs/VERIFICATION_MANAGEMENT.md)；原有 [API 基线记录](docs/VERIFICATION.md) 保留不覆盖。
- [HTML 设计稿](design/README.md) 仅供设计参考，正式业务入口为服务 `/admin/`。

链接录入仅登记链接与摘要；RSS/Atom 可以实际采集。推送是持久收件箱与增量/SSE 通道，不含终端 App、APNs/FCM、已读回执或媒体转码。当前为单管理员工作空间。
