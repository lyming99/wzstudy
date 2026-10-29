# 管理员初始化与邮箱登录验证 · v0.3.0

## 已执行

- `go build ./...`、`go vet ./...`：通过。
- Go 单元/契约测试：54 个测试结果通过（45 个顶层测试），10 个 PostgreSQL 条件用例跳过。
- `CGO_ENABLED=1` 的 Go race 检查：通过；环境更改仅限验证子进程。
- 管理员真实进程验证：7 组通过，包括缺配置拒绝、邮箱归一化、密码原始空白、只存 Argon2id 哈希、错误提示一致、API key 不能登录、重启保留、同邮箱不重设、不同邮箱拒绝及资料不变。
- 结构/PowerShell 验证：14 组通过，包括新旧工作区、相对路径、双工作区拒绝、端口占用保护，以及环境别名按逻辑字段优先于 `.env`、引用密码空白和 `-NoEnvFile`。
- 真实 Chrome 浏览器：24 组通过，涵盖邮箱/密码登录、手机登录布局、文章音视频轮播上传与播放、审核隔离、客户端密钥与收件箱、定时去重、重启恢复、非 JSON 401 和安全退出。没有未捕获 JS 异常或外部网页请求。

实时报告位于根 `.artifacts`：`go-verification.json`、`go-race-verification.json`、`admin-verification.json`、`structure-verification.json`、`live-verification.json`。发行包验证由 `package-verification.json` 单独记录，不凭此文档推断结果。

## 测试安全边界

测试只使用随机密码、随机 API 密钥和隔离临时文件工作区；不读取真实 `.env`、`.local` 或管理员密码。初始化参考工程 wenzflow 仅做源码只读核查，没有修改其源码、配置或数据。

该历史验证未预置公开账号或密码。当前 `start-local.ps1` 首次启动 PostgreSQL 空库时自动生成初始密码，详见 `ADMIN_AUTH.md`；手动连接数据库则可配置 `SYSTEM_ADMIN_*`。

## 未验证

- PostgreSQL 实库与新增初始化条件用例没有执行；需要专用 `TEST_DATABASE_URL`。
- Compose 仅做无插值语法/静态检查，不代表 Docker 镜像构建或容器实机运行已通过。
- 本次不是多用户/RBAC、SMTP、找回密码或密码重置交付。

## 使用说明

详见 `ADMIN_AUTH.md`。已有管理员时，修改初始化密码环境变量不会重设账号密码；不同邮箱会阻止服务启动，避免误覆盖原账号。
