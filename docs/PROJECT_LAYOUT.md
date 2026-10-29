# 目录职责与兼容约定

## 根目录是唯一工程基准

`go.mod` 所在的仓库根目录是构建与工具基准。Go 入口保留在 `cmd/api`、`cmd/migrate`；`internal/{adminui,config,feed,httpapi,study}` 不搬动、不改模块名。管理前端继续通过 `internal/adminui/embed.go` 内嵌，而不是从进程当前目录读取 HTML。

| 目录 | 内容 | 是否提交 |
| --- | --- | --- |
| `cmd`、`internal`、`migrations` | Go 业务代码与数据库迁移 | 是 |
| `docs` | 运行、API、结构及验证说明 | 是 |
| `scripts` | 启动、检查、打包工具 | 是 |
| `design` | 与服务隔离的原始设计稿、设计验证 | 是 |
| `examples` | 客户端对接示例 | 是 |
| `bin` | 从源码启动时的开发二进制 | 否 |
| `dist` | 已验证发行二进制、ZIP、校验清单 | 否 |
| `.artifacts` | 测试专用二进制、临时目录、日志、截图、报告 | 否 |
| `.local` | 本地管理/只读密钥、工作区快照、附件 | 否，严禁打包或公开 |
| `agent` | 独立采集端项目（agent loop + function calling），通过 Agent Protocol 与服务端通信 | 是 |

## 启动路径

- 根 `start-local.ps1` 委托 `start-postgres-local.ps1`，只启动 PostgreSQL；`scripts/start-local.ps1` 和 `internal/start-local.ps1` 委托根入口。
- `scripts/start-dev.ps1` 默认走同一本机 PostgreSQL；显式 `-Postgres` 连接自行配置的数据库，`-Memory` 的服务启动已禁用。
- 路径计算基于脚本位置，不受调用者当前目录影响。`.env` 的 `DATA_DIR` 与新入口相对参数按仓库根目录解析；旧入口相对 `-DataDirectory` 按原 `internal` 目录解析。
- `-Check` 输出解析后的路径与端口，不读取密钥正文、不写文件、不构建、不启动服务。
- 从源码构建使用 `.local/postgres-local` 中的临时目录，不覆盖发行二进制；无源码的发行包需包含 API 和迁移程序。

## 工作区保护

本次整理前未发现本项目活动进程；`.local` 通过同盘目录移动归位，原凭据文件的 SHA-256、ACL 和工作区文件数均校验一致。迁移记录仅包含验证布尔值和数量，保存在 `.artifacts/structure-migration.json`，不含密钥正文。

新的本地入口不再打开文件工作区，也不会自动把旧 `internal/.local` 或根 `.local/data` 的账号和资料迁入 PostgreSQL；原数据和密钥保留不动。需要迁移时应先停止旧进程，完整备份原数据及密钥，并单独制定、验证迁移方案。PostgreSQL 数据卷、附件目录和连接凭据需要成套备份。

## Docker

Docker 只使用 Go 源码、内嵌资源与文档，不应收到本机工作区或测试产物。`.dockerignore` 同时覆盖根与旧层级的 `.local`、`.artifacts`、`data`、`dist`、`bin` 和临时缓存。

API 默认数据目录为 `/var/lib/wzstudy`，镜像构建阶段创建该目录并授予应用用户所有权；Compose 使用 `api_data` 命名卷持久化附件，并另用 `postgres_data` 保存数据库。不能只备份数据库而不备份附件卷。

上述配置修复不等于容器/实库已经运行验证。实际验证边界以 `VERIFICATION_MANAGEMENT.md` 和本轮 JSON 报告为准。

## 验证与发行

所有正式工具在 `scripts` 中定位仓库根目录，所有输出写根 `.artifacts` 或 `dist`。发行 ZIP 通过显式白名单打包 PostgreSQL 启动器、Compose 配置、API 和迁移程序，绝不打包 `.local`。便携版无须 Go 源码，但仍需可用的本机 Docker PostgreSQL。

`verify_structure.py` 使用临时夹具验证入口与兼容策略，不读取真实密钥；`verify_live.py` 是历史文件模式隔离回归。`verify_package.py --static` 只检查包及启动器预检；不加 `--static` 时使用独立临时项目与本机 Docker 进行实库验证，并仅清理经标签确认属于该夹具的资源。

## 管理员认证更新（v0.3.0）

密码算法位于 `internal/auth`，只读版本信息位于 `internal/buildinfo`；管理员单例使用现有私有工作区记录。根入口为空 PostgreSQL 库生成受保护的初始密码，普通重启直接沿用已存密码哈希。完整说明见 `ADMIN_AUTH.md`。
