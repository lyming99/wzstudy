# 知序资料数据服务

当前版本按数据录入与数据查询两条链路组织；`design/index.html` 仅保留历史设计参考。正式界面在 **`/admin/`**，不是直接打开 `design` 中的演示 HTML。

前端资源通过 Go `embed` 打包，无 Node.js、CDN 或前端构建依赖。现有 JSON 入库、RSS/Atom、公开目录、增量拉取接口继续保留；客户端通过接口自行查询和推荐。

## 1. Windows 本地启动

在仓库根目录 `D:\project\wzstudy` 运行：

```powershell
.\start-local.ps1
```

- 已安装 Go 1.25+ 时，脚本会从当前源码构建到 `bin/wzstudy-dev.exe`，不覆盖 `dist` 中的发行可执行文件。
- 未安装 Go 时，可使用已交付的 `dist/wzstudy-v0.3.0-windows-amd64.exe`。
- 默认监听 `0.0.0.0:8091`，本机管理入口仍为 `http://127.0.0.1:8091/admin/`；同一局域网设备使用 `http://<服务器局域网IP>:8091/admin/`。监听通配地址 `0.0.0.0` 不能作为浏览器或二维码中的服务地址。
- 仅允许本机访问时，启动或重启显式加 `-Address '127.0.0.1:8091'`。修改配置不影响已运行进程，需重启后生效。
- 局域网访问要求客户端与服务器网络可达、防火墙允许受信网络的 TCP 8091。启动脚本不会创建防火墙规则或公网映射，HTTP 仅用于可信局域网调试；公网部署须使用 HTTPS 和生产存储。
- API 接入仍使用随机管理密钥与只读密钥，保存到 `.local/access-keys.json`；Windows 下会限制新凭据文件的访问 ACL。
- 首次运行 `start-local.ps1 -InitializeAdmin`，在本机输入管理员邮箱和隐藏密码；网页使用邮箱/密码登录，API 密钥不是登录密码。详见同目录 `ADMIN_AUTH.md`。不要将真实密码、密钥或凭据文件公开。
- 默认资料数据位于 `.local/data`，不是浏览器 `localStorage`。初始工作空间没有演示资料。
- 保持终端运行；按 Ctrl+C 停止服务。重启后需重新登录，但资料和文件仍然保留。

### 重启与退出码排查

在另一个终端执行以下命令可重启本项目的本地服务：

```powershell
.\scripts\restart-local.ps1
```

- 重启脚本仅停止端口上属于本项目已知可执行文件的进程；其他程序占用端口时拒绝操作。停止使用强制结束方式，会中断请求，不要在上传或入库期间重启。
- 新版启动器与重启脚本通过仅限本机会话的进程生命周期信号确认这次重启。旧窗口收到明确通知且服务以 `-1` 退出时，显示“由重启脚本停止”，不再误报运行失败；替代实例的启动结果以执行重启的终端为准。通知不会证明替代实例已成功启动。
- 没有收到重启通知的 `-1`（Windows `0xFFFFFFFF`）仍然报错，提示检查其他终端、旧版重启脚本、`Stop-Process` 或终端/任务被停止等情况；仅凭退出码不能确定是谁终止了进程。这不是管理员密码错误的证明，不要因此删除数据或重新生成密钥。
- 更新脚本前已经运行的旧窗口没有上述信号，重启时仍可能显示原来的 `-1` 错误；重新使用新版启动器启动后生效。不需要仅为消除旧窗口报错而再次重启健康实例。
- 可只读检查当前实例是否就绪：`Invoke-RestMethod http://127.0.0.1:8091/api/v1/health/ready`。返回 `ready` 只说明当前实例就绪，不能证明先前实例的退出原因。
- 服务仍在前台运行，需保持启动终端打开；本脚本没有安装 Windows 常驻服务，也不会因未知退出而自动无限重启。

自定义端口或数据目录：

```powershell
.\start-local.ps1 -Address "127.0.0.1:8092" -DataDirectory "D:\study-data"
```

如果组织策略不允许运行脚本，请使用已批准的策略，或通过环境变量运行 Go/可执行文件；无需修改系统级执行策略。

## 2. 手动运行与环境变量

服务入口仍为同一仓库的 `cmd/api`：

```powershell
$env:APP_ENV = "development"
$env:STORE = "file"
$env:DATA_DIR = "D:\study-data"
$env:HTTP_ADDR = "0.0.0.0:8091"
# 在本机安全配置中设置两个不同的随机密钥，不要使用固定公开示例值：
$env:ADMIN_API_KEY = "<32–256 字符的随机管理密钥>"
$env:READ_API_KEY = "<另一个随机只读密钥>"
go run wzstudy/cmd/api
```

上面的占位符必须替换为实际随机值。也可在仓库根目录运行 `go run ./cmd/api`。

| 变量 | 默认值 / 规则 |
| --- | --- |
| `APP_ENV` | `development`；另支持 `test`、`production` |
| `HTTP_ADDR` | `0.0.0.0:8091`，监听所有 IPv4 网卡；仅本机可显式设为 `127.0.0.1:8091` |
| `STORE` | 服务默认 `postgres`；本地脚本显式使用 `file`；`memory` 仅用于临时测试 |
| `DATA_DIR` | 服务默认 `data`；本地脚本使用仓库根目录 `.local/data`，存放文件快照与上传附件 |
| `DATABASE_URL` | PostgreSQL 模式必填 |
| `ADMIN_API_KEY` | 32–256 字符；仅 Bearer 管理 API 自动化，不用于浏览器登录 |
| `SYSTEM_ADMIN_EMAIL` | 首次初始化管理员的邮箱；之后可省略，同邮箱不改密码，不同邮箱拒绝 |
| `SYSTEM_ADMIN_PASSWORD` | 首次初始化密码，8–128 UTF-8 字节；原始空白保留，仅持久化 Argon2id 哈希 |
| `SYSTEM_ADMIN_DISPLAY_NAME` | 可选，默认“知序管理员” |
| `READ_API_KEY` | 与管理密钥不同；公开目录、公开媒体、changes |
| `UPLOAD_MAX_MB` | 200，支持 1–1024；单次上传所有文件合计大小限制 |
| `POLL_ENABLED` | `true`；只控制 RSS 资料采集轮询，不向学习客户端分发数据 |
| `CORS_ORIGINS` | 默认空，同源自动允许；跨源需配置完整精确 Origin，不支持通配符 |

`file` 模式是**单进程开发存储**。不要让多个进程共用同一份 `workspace.json`；生产环境强制使用 PostgreSQL。

## 3. 首次使用

1. 登录后进入「数据录入 → 录入端管理」创建 Source，或「数据查询 → 查询客户端」创建 Client，分别保存一次性 Key。
2. 在「学习资料库」上传音频、视频、文章或多张轮播图片，也可直接编辑文章。
3. 草稿先「提交审核」，待审核内容再「审核并发布」。编辑已发布资料不会立即覆盖客户端的旧版本。
4. 在「内容来源」添加公开 RSS / Atom 订阅，手动采集或开启后台轮询。
5. 「采集链接」保存原始链接、摘要和出处，**不会自动抓取任意网页正文或下载外部媒体**。
6. 在「数据录入 → 入库记录」核对真实回执；在「接入指引」查看采集程序、Skill 与 MCP 配置。
7. 在「数据查询」预览公开查询结果；学习客户端独立维护学习算法、记录与进度。

### 文件与播放

- 音频：MP3、WAV、M4A、OGG、FLAC。
- 视频：MP4/M4V、MOV、WebM。
- 文章文件：UTF-8 TXT、MD、Markdown，单份文本正文最多 262144 字节。
- 轮播：PNG、JPEG、GIF、WebP，最多 20 张；拒绝 SVG、HTML 和伪装类型。
- 文件按 UUID 落盘，不使用原始文件名作为路径；服务端检查实际内容类型。
- 媒体按原始文件保存，**不转码、不提取时长、不生成视频缩略图**；能否播放取决于实际编码与浏览器支持。
- 管理员可预览私有资料；只读密钥仅能读取仍处于公开快照中的附件。媒体支持 HTTP Range。
- 此版的资料编辑用于修改标题、摘要、分类、正文等；更换媒体文件请上传为新资料。
- 删除为软删除，会同步下架公开内容；附件保留在备份中，不自动回收。

## 4. 客户端对接

三类凭据不混用；新客户端优先使用独立来源 Key，按权限访问：

| 凭据 | 用途 |
| --- | --- |
| 管理密钥 / 管理会话 | 管理页面、管理 API，不替代来源密钥进行客户端入库 |
| 来源专属密钥 | 仅 ingest/upload，入库限自身来源；不能查询资料 |
| 查询客户端 Key | 独立 Client 的 read/sync，只读公开资料、媒体及增量变更 |
| 旧全局只读密钥 | READ_API_KEY，仅兼容旧接入 |

主要接口（JSON 返回均使用 `{ "data": ... }` 包装）：

```text
POST /api/v1/ingest/{source_id}
POST /api/v1/ingest/{source_id}/files
GET  /api/v1/items
GET  /api/v1/media/{asset_id}
GET  /api/v1/changes?after=0
```

客户端主动调用 `/items`（类型/分类/标签/关键词/来源筛选与分页）、`/items/{id}` 和 `/changes`。在本地将公开资料与学习记录结合，计算个性化推荐；服务端不实现推荐算法。`/changes` 的空页也应保存 `next_cursor`，`has_more=true` 时继续拉取。

旧推送计划、收件箱和 SSE 路由已移除，历史计划不再执行。已有资料、附件、Key 和游标不重置；旧 `inbox` 权限不再生效，也不会自动授予阅读权限。

详细请求格式、并发版本号和恢复规则参见同仓库 `docs/MANAGEMENT.md`、`docs/openapi.json`。运行服务的 `/docs` 与 `/openapi.json` 也提供接口文档。

## 5. PostgreSQL 部署与迁移

1. 安全配置两类访问密钥与 `DATABASE_URL`。
2. 执行 `go run wzstudy/cmd/migrate`，在原有迁移基础上追加应用 `000002_management_workspace.sql` 与 `000003_input_query_identities.sql`；不要修改已应用的 `000001_initial.sql`。
3. 设置 `STORE=postgres`、持久化的 `DATA_DIR`，启动服务。
4. 生产环境设置 `APP_ENV=production`，通过 HTTPS 反向代理访问。管理 Cookie 在生产模式强制 `Secure`。
5. HTTPS 代理后如服务自身接收 HTTP，应在 `CORS_ORIGINS` 配置管理页面的精确 HTTPS Origin，并正确保留 Cookie 和 Range 头。
6. 多实例部署必须共用数据库和附件卷；仅共享数据库、不共享 `DATA_DIR/uploads` 会导致部分实例无法读取文件。浏览器管理会话保存在进程内，还需要粘性会话/同实例路由；首版建议单实例部署，Bearer API 不依赖该会话。
7. 客户端消费音视频和轮播需要按新增的 `kind/assets` 契约适配；原有文章请求与接口路径继续可用。

生产模式拒绝 `file` 和 `memory`，schema readiness 会检查工作空间表和客户端索引，缺少 000003 时拒绝就绪。结构整理与文件模式回归不包含 PostgreSQL 实库/容器运行测试；迁移与 PostgreSQL 存储适配已实现，但部署前仍应在实际测试数据库执行迁移及回归。

## 5.1 旧身份拆分与回退

启动不会自动迁移旧查询/混合来源。先备份工作区/数据库及附件，再在管理端“旧身份待拆分”预检中确认所有混合 Key 的保留归属。纯查询 Key 仅转入 Client，混合 Key 不复制给两侧；另一侧的新 Key 仅本次显示。停用、空权限不扩权，资料 ID、source_id、附件和内容游标不重置。

迁移后回退需要成套恢复迁移前备份，不能只换回旧可执行文件。文件模式切勿让新旧进程同时打开同一工作区。详见 `MANAGEMENT.md`。

## 6. 备份与限制

- 文件模式：备份完整 `.local/data`（`workspace.json` 与 `uploads`），并单独安全备份 `.local/access-keys.json`。
- PostgreSQL 模式：备份数据库与附件卷，并保持相同时间点；管理/只读密钥由部署环境安全管理。
- 上传事务返回不确定存储错误时，会保留不可变文件，避免误删已提交记录的附件；可能遗留未登记文件，不要在服务运行时盲目清理。
- 首版为单管理员工作空间，不包含多用户/RBAC、配额计费、媒体转码、对象存储、附件垃圾回收或全文检索服务。
- 管理列表及 CSV 导出沿用现有接口的分页边界；导出最多前 10000 份资料。
- 管理会话存在服务进程内，最长 8 小时；退出会撤销会话，服务重启后需重新登录。

## 7. 验证

```powershell
python scripts/verify_go.py
go vet ./...
python scripts/verify_structure.py
# Windows：隔离验证前台启动、已通知重启与未知强制终止；不操作真实工作区
New-Item -ItemType Directory -Force .artifacts | Out-Null
go build -o .artifacts/wzstudy-launcher-test.exe ./cmd/api
python scripts/verify_local_service.py
New-Item -ItemType Directory -Force .artifacts | Out-Null
go build -o .artifacts/wzstudy.exe ./cmd/api
python scripts/verify_admin.py
python scripts/verify_live.py
python scripts/verify_two_chains.py
python -m unittest discover -s mcp/wzstudy_ingest/tests -v
```

`scripts/verify_live.py` 使用隔离临时文件存储、随机测试密钥和本机 Chrome，不读取现有凭据/数据库，也不向外部网页发请求。需要 Python Playwright；FFmpeg 仅用于生成浏览器播放测试样本，不是服务运行依赖。

验证报告与真实服务截图生成到 `.artifacts/`；`design/` 继续保留最初的独立原型，不作为正式管理入口。

## 8. 目录与旧入口兼容

- 统一入口为根 `start-local.ps1`，实现位于 `scripts/start-local.ps1`；`scripts/start-dev.ps1` 默认也沿用文件持久化模式。只有显式 `-Memory` 才使用易失内存。
- `.local`、`.artifacts`、`dist`、`design` 已归位到仓库根。业务 Go 包仍位于 `internal`，内嵌资源路径不变。
- 本次搬迁是在无本项目服务进程占用时完成；原密钥字节、凭据 ACL 和工作区文件数保持一致，没有重置数据。
- 启动器兼容仍在 `internal/.local` 的旧工作区；两处同时存在工作区时必须传 `-ProfileDirectory`。不自动迁移或覆盖任一份。
- `-Check` 只返回路径，不读取密钥、不构建、不生成文件、不启动服务；可从任意当前目录调用根入口。
- `-SkipBuild` 使用便携版。开发构建在 `bin`，测试构建在 `.artifacts`，发行包在 `dist`，互不覆盖。
- 旧 `internal/start-local.ps1` 是委托入口，旧相对 `-DataDirectory` 仍以 `internal` 为基准。新入口相对路径以仓库根为基准。
- Docker 将附件保存在 `/var/lib/wzstudy` 的 `api_data` 卷，数据库和附件卷需一起备份；根及旧层级私有目录均不进入构建上下文。

详细目录职责见同目录 `PROJECT_LAYOUT.md`。
