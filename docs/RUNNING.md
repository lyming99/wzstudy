# 温知资料数据服务

当前版本按用户工作区隔离资料；`design/index.html` 仅保留历史设计参考。正式共用工作台为 **`/workspace/`**，所有账号使用同一邮箱密码登录入口，管理员权限由服务端根据配置并核验账号后判定；原 `/admin/` 页面保留为兼容入口。

前端资源通过 Go `embed` 打包，无 Node.js、CDN 或前端构建依赖。现有 JSON 入库、RSS/Atom、公开目录、增量拉取接口继续保留；客户端通过接口自行查询和推荐。

## 1. Windows 本地启动

标准入口必须使用 PostgreSQL，不会回退到文件存储。需要 Windows PowerShell、Docker Desktop（引擎已启动）、Docker Compose v2，以及源码启动所需的 Go 1.25+。发行包若同时包含 API 与迁移可执行文件，可不安装 Go。仅接受本机 Docker 命名管道或 Unix socket，不操作 SSH/TCP 远端上下文；首次可能需要下载镜像。

在项目根目录双击 `start-local.cmd`，或运行：

```powershell
.\start-local.ps1
```

`start-postgres-local.ps1` 和 `start-postgres-local.cmd` 仍是兼容入口。启动器使用项目根目录 `.env` 保存配置，并创建或复用本项目专属 Docker PostgreSQL 数据卷；数据库就绪、迁移成功后才启动 API。数据库默认监听 `127.0.0.1:15432`，服务默认监听 `0.0.0.0:8091`，可信局域网设备可直接访问；需要仅本机访问时使用 `-Address 127.0.0.1:8091`。自动选择其他空闲端口时以终端显示的工作台地址为准。服务在前台运行，Ctrl+C 仅停止 API。此模式仅适用于可信网络开发；Docker 管理者可以读取容器环境中的数据库密码。

首次使用空数据库时，可在 `.env` 同时设置 `SYSTEM_ADMIN_EMAIL` 和 `SYSTEM_ADMIN_PASSWORD`；只设置其中一项会拒绝初始化，有密码时也可通过 `-AdministratorEmail` 指定邮箱。未设置两项时，默认使用 `admin@wzstudy.local` 和随机生成的密码，写入 `.env`。密码保留原始空白，至少 8、最多 128 个 UTF-8 字节；`.env` 值的成对外层引号会去除，不做变量或转义展开。已有管理员始终保留，修改密码配置不会重置它；如果配置的管理员邮箱与已有账号不一致，服务会拒绝启动，不自动转移权限或覆盖账号。已有部署未配置邮箱时，使用之前保存的管理员邮箱作为兼容回退。

**`.env` 含明文密码和 API 密钥**，启动器会将其 Windows ACL 限制为当前用户，且不向终端输出密码。已有文件属于当前用户时，脚本只收紧访问权限、不重新指定所有者，通常无需以管理员身份运行；若文件属于其他用户或当前账户无权修改其访问规则，脚本会停止并提示联系文件所有者处理，不会为了启动而开放权限。不要提交、分享或删除 `.env`；应将 `.env`、PostgreSQL 数据卷和附件目录一同安全备份。旧版 `connection.json` 存在且 `.env` 未登记卷身份时，启动器会核验旧凭据、卷归属及旧标记，将兼容的设置迁入 `.env`；旧文件保留不自动删除。未完成初始化的旧管理员密码文件也只在需要时核验并迁入；配置冲突会停止。旧的 `-InitializeAdmin` 和文件模式 `-ProfileDirectory` 不再适用于此入口。

### 本机数据库初始化

`start-local.ps1` 使用根 `.env` 中的 `DATABASE_URL`、`POSTGRES_PASSWORD`、`ADMIN_API_KEY` 和 `READ_API_KEY`，不会用同名进程变量覆盖文件。没有配置数据库密码时生成随机密码并构造本机连接串；只配置 `POSTGRES_PASSWORD` 时使用该密码并构造连接串。缺少 API 密钥时也会生成，连同卷身份、初始化状态一起安全写入 `.env`，再用同一数据库密码启动专属 Docker PostgreSQL、迁移和 API。写入失败或检测到配置文件并发修改时停止，不会用临时密码创建数据库。

`-NoEnvFile` 是例外的进程环境模式：它不读取或写入 `.env`，仅接受已创建且卷归属匹配的数据库，并要求进程提供完整数据库连接、两个 API 密钥及 `WZSTUDY_LOCAL_INSTANCE_ID`、`WZSTUDY_LOCAL_VOLUME_STATE=created`；不能用它自动初始化新库或生成无法持久保存的密码。

可以配置指向本机的 `DATABASE_URL` 来指定数据库用户、库名和端口，例如 `postgres://<用户>:<URL编码后的密码>@127.0.0.1:15432/<库名>?sslmode=disable`。用户名和库名须以字母或下划线开头，只包含 ASCII 字母、数字和下划线，最长 63 字符；主机仅支持 `127.0.0.1` 或 `localhost`，仅支持 `sslmode=disable` 且不接受其他查询参数（本机 Docker 容器未配置 TLS）。连接串没有指定端口时使用 PostgreSQL 标准端口 5432；没有配置连接串时默认本机端口 15432。配置的端口与 `-DatabasePort` 不一致会报错，不自动切换端口。URL 中没有密码时，使用 `POSTGRES_PASSWORD` 或生成随机密码；两处同时提供密码时必须一致。密码按 UTF-8 原样使用；URL 中的保留字符需百分号编码。密码允许 1–256 UTF-8 字节，不接受 NUL 或换行；请使用足够强的密码。

`.env` 中的本机卷身份和状态用于核对 Docker 标签；已登记的数据卷缺失、归属不符或凭据不完整时停止，不创建替代空库。不能通过修改 `.env` 来重设现有 PostgreSQL 用户密码：如果新连接或密码与卷中实际数据库不匹配，迁移或连接会失败，原数据不会被重置。不要删除数据卷或配置来绕过保护。要连接已有外部数据库，请改用 `scripts/start-dev.ps1 -Postgres` 并配置适合该数据库的 TLS。

### 附件存储路径

根 `.env` 配置媒体附件路径：

```dotenv
DATA_DIR=.local/postgres-local/data
```

可改为绝对路径（例如 `DATA_DIR=D:\study-media`）或相对项目根目录的路径；带空格可用成对引号。`-DataDirectory` 优先于进程环境 `DATA_DIR`，进程环境优先于 `.env`；`-NoEnvFile` 不读取 `.env`，无配置时回到上述默认路径。修改配置需重启 API；**仅修改路径不会搬迁已有附件**，切换前请停止服务、备份并确认所需文件已迁移。PostgreSQL 记录仍保存在 Docker 数据卷，`DATA_DIR` 不改变数据库的位置；需要一起备份数据库、附件目录和连接凭据。旧文件模式的账号、资料及附件不会自动迁移或删除。

```powershell
.\start-local.ps1 -ApiPort 8092 -DataDirectory "D:\study-media"
.\start-local.ps1 -Check
```

`-Check` 只解析附件路径和命令行端口，不解析或校验 `DATABASE_URL`、不读取 PostgreSQL 凭据文件、不输出 `.env` 其他字段，也不启动 Docker 或连接数据库。显式端口占用时安全退出；保存的数据库端口不能被启动参数悄然改写。若已登记的数据库卷或凭据丢失，启动器拒绝创建替代，需先恢复原数据。`python scripts/verify_postgres_launcher.py` 只使用临时项目和模拟 Docker；`python scripts/verify_postgres_admin_env.py` 使用单独的临时项目和真实本地 Docker PostgreSQL 验证配置与登录，并在归属校验通过后清理测试容器和数据卷。

默认监听所有 IPv4 网卡，可信局域网内的设备可以直接访问。如需只允许本机访问，先停止现有 API（不要在上传或入库时操作），再显式绑定回环地址：

```powershell
.\start-local.ps1 -Address 127.0.0.1:8091
# 使用其他 API 端口并只允许本机：.\start-local.ps1 -Address 127.0.0.1:8092
# 只预览参数，不启动服务或测试网络：.\start-local.ps1 -Check
```

默认绑定 `0.0.0.0:8091`（所有 IPv4 网卡）；系统可能使用 IPv6 双栈通配套接字，也可能允许 IPv6 访问，请同时检查 IPv4/IPv6 的网络与防火墙边界。启动信息会列出活动网卡的候选 IPv4；根据手机与电脑所连接的网络选择实际可达的地址。二维码中的服务地址填写例如 `http://192.168.1.10:8091`，不要使用 `0.0.0.0`、`127.0.0.1` 或 `/workspace` 路径。也可用 `-Address '192.168.1.10:8091'` 只绑定一个指定的本机 IPv4；`-Address` 不能与 `-ApiPort` 同时使用。`-Lan` 仍可作为兼容参数传入，等同于默认行为。数据库仍只监听本机。

启动器不会修改 Windows 防火墙；如手机不能连接，请检查当前网络配置、受信网络的防火墙入站规则和 Wi-Fi 客户端隔离。局域网模式会扩大 API 暴露范围，HTTP 明文传输密码和查询 Key，仅在可信网络临时使用，正式服务请配置 HTTPS，不要直接暴露到公网。

### 重启与退出码排查

```powershell
.\scripts\restart-local.ps1
```

如之前的 API 端口不是 8091，重启时传入实际 `-ApiPort` 或 `-Address`。重启仅停止端口上属于本项目的已识别可执行文件；遇到其他程序、Docker 不可用或缺少构建条件时拒绝停止。重启使用强制结束方式，会中断请求，不要在上传或入库期间操作。新旧服务使用本地生命周期信号确认显式重启；未经确认的强制退出仍报错。旧文件模式进程重启后也会切换到 PostgreSQL，但不会自动迁移数据。

可只读检查就绪状态：`Invoke-RestMethod http://127.0.0.1:8091/api/v1/health/ready`。此脚本不会安装 Windows 常驻服务。

## 2. 手动运行与环境变量

服务入口仍为同一仓库的 `cmd/api`：

```powershell
$env:APP_ENV = "development"
$env:STORE = "postgres"
$env:DATABASE_URL = "<已配置的 PostgreSQL 连接串>"
$env:DATA_DIR = "D:\study-media"
$env:HTTP_ADDR = "127.0.0.1:8091"
# 在本机安全配置中设置两个不同的随机密钥，不要使用固定公开示例值：
$env:ADMIN_API_KEY = "<32–256 字符的随机管理密钥>"
$env:READ_API_KEY = "<另一个随机只读密钥>"
# 空数据库还须安全设置 SYSTEM_ADMIN_EMAIL 和 SYSTEM_ADMIN_PASSWORD。
go run ./cmd/migrate
go run ./cmd/api
```

上面的占位符必须替换为实际随机值。也可在仓库根目录运行 `go run ./cmd/api`。

| 变量 | 默认值 / 规则 |
| --- | --- |
| `APP_ENV` | `development`；另支持 `test`、`production` |
| `HTTP_ADDR` | 服务自身默认 `0.0.0.0:8091`；本机 PostgreSQL 启动器默认设置为 `0.0.0.0:8091`，可用 `-Address 127.0.0.1:8091` 仅本机访问 |
| `STORE` | 服务默认 `postgres`；本地启动脚本强制使用 `postgres` |
| `DATA_DIR` | 服务默认 `data`；本地启动从 `.env`、进程环境或参数解析，默认 `.local/postgres-local/data`，存放媒体附件而非 PostgreSQL 数据 |
| `DATABASE_URL` | 手动 PostgreSQL 模式必填；本机启动器可选，只允许本机 Docker 连接地址 |
| `ADMIN_API_KEY` | 32–256 字符；用于 Bearer 管理 API 自动化，不用于浏览器登录；未配置独立查询 Key 加密密钥时也用于派生加密密钥 |
| `CLIENT_KEY_ENCRYPTION_KEY` | 可选，32–256 字符的稳定随机秘密；用于加密保存的查询客户端 Key，未设置时从 `ADMIN_API_KEY` 派生。须安全备份，不要随意更换；更换配置会使旧密文无法回读但不影响 Key 认证，详见 `CLIENT_QR.md` |
| `SYSTEM_ADMIN_EMAIL` | 首次初始化管理员的邮箱；之后可省略，同邮箱不改密码，不同邮箱拒绝 |
| `SYSTEM_ADMIN_PASSWORD` | 首次初始化密码，8–128 UTF-8 字节；原始空白保留，仅持久化 Argon2id 哈希 |
| `SYSTEM_ADMIN_DISPLAY_NAME` | 可选，默认“温知管理员” |
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

项目通过 `DATABASE_URL` 指定 PostgreSQL 用户、密码、主机、端口和数据库，例如：

```text
DATABASE_URL=postgres://<数据库用户>:<URL编码后的密码>@<数据库主机>:5432/<数据库名>?sslmode=require
```

将根目录 `.env.example` 复制为 `.env` 并填入真实连接信息；凭据中有 URL 保留字符时应编码。远程或生产环境应启用 TLS，并按数据库证书配置验证方式。不要提交或分享包含真实密码的 `.env`。

- **本机连接已有 PostgreSQL**：`scripts/start-dev.ps1 -Postgres` 会读取 `.env` 中的 `DATABASE_URL`，先执行数据库迁移再启动。还需安全配置两个不同的 `ADMIN_API_KEY` 和 `READ_API_KEY`；它们不是管理员网页密码。首次使用空数据库还需配置管理员邮箱和密码。
- **Docker Compose 内置 PostgreSQL**：当前 Compose 总会启动自带数据库，并由 `POSTGRES_PASSWORD` 和内置用户名、库名生成容器网络中的连接地址；它**不会读取示例中的 `DATABASE_URL`**，同样需要配置两种 API 密钥。Compose 密码请使用 URL 安全字符。
- **默认本地启动**：`start-local.ps1` 使用专属本机 Docker PostgreSQL，从 `.env` 读取本机 `DATABASE_URL` 和 `POSTGRES_PASSWORD`；未提供密码则生成并写入 `.env`，不连接外部数据库。现有文件数据不会自动复制到 PostgreSQL；媒体文件位于 `DATA_DIR`，需要与数据库一起备份。

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
# Windows：使用模拟 Docker 和隔离服务验证 PostgreSQL 启动、数据保护和重启
python scripts/verify_postgres_launcher.py
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

- 根 `start-local.ps1` 委托 `start-postgres-local.ps1`；`scripts/start-local.ps1`、`scripts/start-dev.ps1` 的默认入口和 `internal/start-local.ps1` 也使用 PostgreSQL。显式内存模式的启动已禁用。
- `scripts/start-dev.ps1 -Postgres` 是连接已有 PostgreSQL 的单独路径，需要自行配置连接串与 API 密钥。
- `-Check` 返回解析后的路径和端口，不读取密钥内容、不构建、不生成文件、不启动服务；可从任意当前目录调用。
- 从源码构建使用临时构建目录；`-SkipBuild` 需要 `dist` 中同时具备 API 与迁移可执行文件，不会回退文件存储。
- 旧 `internal/start-local.ps1` 是委托入口，其相对 `-DataDirectory` 仍以 `internal` 为基准；新入口和 `.env` 中的相对路径以项目根为基准。
- 旧 `.local` 和 `internal/.local` 文件工作区不会被自动搬迁或覆盖。另一套 `docker-compose.yml` 的附件保存在 `api_data` 卷，与本机启动器的 `DATA_DIR` 不同；数据库和附件需一起备份。

详细目录职责见同目录 `PROJECT_LAYOUT.md`。
