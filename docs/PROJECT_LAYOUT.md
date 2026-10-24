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

## 启动路径

- 根 `start-local.ps1` 仅委托 `scripts/start-local.ps1`。
- `scripts/start-dev.ps1` 默认也走同一文件持久化工作区。易失模式必须显式写 `-Memory`；PostgreSQL 必须显式写 `-Postgres`。
- `internal/start-local.ps1` 只保留旧命令兼容，不构建第二份程序、不生成第二套默认密钥。
- 路径计算基于脚本位置，不受调用者当前目录影响。新入口相对参数按仓库根目录解析；旧入口相对 `-DataDirectory` 按原 `internal` 目录解析。
- `-Check` 输出解析后的路径与存在状态，不读取密钥正文、不写文件、不构建、不启动服务。
- 从源码构建的 `bin/wzstudy-dev.exe` 与发行包分离，避免开发启动破坏发行校验清单。

## 工作区保护

本次整理前未发现本项目活动进程；`.local` 通过同盘目录移动归位，原凭据文件的 SHA-256、ACL 和工作区文件数均校验一致。迁移记录仅包含验证布尔值和数量，保存在 `.artifacts/structure-migration.json`，不含密钥正文。

对于尚未整理的旧副本：

1. 仅有旧 `internal/.local` 工作区时，启动器沿用旧位置，不自动移动数据。
2. 根和旧位置都存在工作区时拒绝猜测，要求明确 `-ProfileDirectory`。
3. 已有 `workspace.json` 或上传目录但缺少对应密钥文件时，拒绝自动生成新密钥。
4. 绝不能让两个文件存储进程共享一个 `workspace.json`；端口检查不等于跨进程数据锁。不同端口实例必须显式使用不同工作区。
5. 需要迁移其他有数据的工作区时，先停止使用它的进程，再整体备份数据和原密钥，校验后再切换；不能在线剪切目录。

## Docker

Docker 只使用 Go 源码、内嵌资源与文档，不应收到本机工作区或测试产物。`.dockerignore` 同时覆盖根与旧层级的 `.local`、`.artifacts`、`data`、`dist`、`bin` 和临时缓存。

API 默认数据目录为 `/var/lib/wzstudy`，镜像构建阶段创建该目录并授予应用用户所有权；Compose 使用 `api_data` 命名卷持久化附件，并另用 `postgres_data` 保存数据库。不能只备份数据库而不备份附件卷。

上述配置修复不等于容器/实库已经运行验证。实际验证边界以 `VERIFICATION_MANAGEMENT.md` 和本轮 JSON 报告为准。

## 验证与发行

所有正式工具在 `scripts` 中定位仓库根目录，所有输出写根 `.artifacts` 或 `dist`。发行 ZIP 通过显式文件白名单构建，根入口和 `scripts/start-local.ps1` 一并打包，绝不递归打包 `.local`。便携版无 Go 源码时直接运行内嵌管理资源的可执行文件。

`verify_structure.py` 使用临时夹具验证入口与兼容策略，不读取真实密钥；`verify_live.py` 使用隔离随机端口、随机密钥和文件工作区；`verify_package.py` 只启动/停止自己解压生成的服务进程。

## 管理员认证更新（v0.3.0）

密码算法位于 `internal/auth`，只读版本信息位于 `internal/buildinfo`；管理员单例使用现有私有工作区记录。根入口支持 `-InitializeAdmin` 本机隐藏输入，普通重启直接沿用已存密码哈希。完整说明见 `ADMIN_AUTH.md`。
