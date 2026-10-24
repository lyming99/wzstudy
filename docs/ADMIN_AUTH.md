# 系统管理员初始化与邮箱登录 · v0.3.0

## 与 wenzflow 对齐的规则

参考工程仅作为源码参考，没有修改其代码、配置或数据。WzStudy 使用同样的启动引导语义：

1. 没有系统管理员时，必须明确提供管理员邮箱和密码；没有公开默认账号或默认密码。
2. 首次初始化在监听 HTTP **之前**完成，写入原有存储事务。
3. 已有管理员时，同邮箱启动不重设密码、显示名或身份；不提供初始化变量也可正常重启。
4. 配置了不同邮箱时拒绝启动，不替换旧管理员、不覆盖原资料。
5. 不提供匿名注册、初始化或提权的 HTTP 接口。客户端来源不属于可登录的普通用户。
6. 密码使用独立随机盐的 Argon2id（64 MiB、3 次、并行度 1），持久存储中没有明文密码。

## Windows 本地初始化（推荐）

在仓库根目录的本机 PowerShell 运行：

```powershell
.\start-local.ps1 -InitializeAdmin
```

终端依次要求管理员邮箱、密码、确认密码。密码隐藏输入，不作为命令行参数，不写入本地 API 密钥文件；确认后初始化并启动服务。请在本机输入，不必向助手提供真实密码。

初始化完成后，访问 `http://127.0.0.1:8091/admin/`，使用刚配置的**邮箱和密码**登录。重启时只需：

```powershell
.\start-local.ps1
```

`-InitializeAdmin` 仅用于首次创建，**不是密码重置命令**。重复使用不会修改已有密码。邮箱登录允许邮箱首尾空白及大小写差异，密码逐字节匹配，不去除首尾空白。

## 环境变量 / Docker / PostgreSQL

首选配置名称与 wenzflow 一致：

| 配置 | 规则 |
| --- | --- |
| `SYSTEM_ADMIN_EMAIL` | 首次必填，严格邮箱格式，归一化为小写 |
| `SYSTEM_ADMIN_PASSWORD` | 首次必填，8–128 UTF-8 字节，无 NUL；建议 12 位以上随机密码 |
| `SYSTEM_ADMIN_DISPLAY_NAME` | 可选，默认“知序管理员”，最多 120 UTF-8 字节 |

兼容 `BOOTSTRAP_ADMIN_EMAIL/PASSWORD`；显示名还兼容 `SYSTEM_ADMIN_USERNAME`、`BOOTSTRAP_ADMIN_DISPLAY_NAME/USERNAME`。同一逻辑配置项（包括其兼容别名）优先使用进程环境，不会从 `.env` 覆盖非空进程值。密码原始空白保留；`.env` 最外层单/双引号可去除，不执行其中代码、变量或转义。

可复制根 `.env.example`，在本机安全编辑上述字段。默认文件模式启动器只从 `.env` 取管理员引导字段，**API 密钥仍沿用 `.local/access-keys.json`**。Docker/PostgreSQL 开发模式使用根 `.env` / 进程环境中的配置。

初始化成功后可以从环境中移除初始化密码，再重启验证；账户哈希已经持久保存。不要把带真实密码的 `.env` 提交、打包或截图公开。留在环境中的无效非空配置仍可能在配置校验阶段被拒绝。

- `-NoEnvFile`：不读取根 `.env`，用于隔离验证或明确只使用进程环境。
- `-Check`：只返回工作区与可执行路径，不读取密码、不开服务、不初始化账号。
- `scripts/start-dev.ps1 -Postgres`：配置环境后先迁移再启动，首次创建规则相同；交互 `-InitializeAdmin` 仅用于本地文件模式。
- Docker Compose 将 `SYSTEM_ADMIN_*` 传入 API，账户保存于 PostgreSQL；附件仍保存在独立 `api_data` 卷。

## 存储与旧资料兼容

- 文件模式：管理员哈希记录存入原 `.local/data/workspace.json` 的私有记录，原资料、来源、附件和客户端密钥保持不变。
- PostgreSQL：使用已有 `workspace_records`，不需要改写已应用的迁移；现有全局写事务锁内再次检查单例，防止多进程并发创建多个管理员。
- 管理账户不会出现在公开目录、客户端收件箱、资料 CSV 或工作区摘要中。成功登录/读取会话只返回安全身份信息，不返回密码或哈希。
- 如果旧版只有 API 密钥而没有管理员账户，升级后首次启动需要上述一次初始化。旧 API 密钥不会自动变成密码，也不会自动创建一个公开的账号。
- 普通重启不需要重新输入密码。不能通过更换初始化环境变量重置已有管理员。

## 浏览器会话与 API 密钥分工

```text
POST /api/v1/auth/login
Content-Type: application/json

{"email":"<管理员邮箱>","password":"<管理员密码>"}
```

- 登录成功返回 HttpOnly、SameSite=Strict 的 8 小时会话 Cookie。生产强制 Secure，必须通过 HTTPS 代理访问。
- 原 `{ "access_key": ... }` 浏览器登录不再接受。`ADMIN_API_KEY` 仍用于管理 API 自动化；`READ_API_KEY` 和来源专属密钥的权限与用途保持原样。
- 登录密码、会话 Cookie、API 密钥都不会存入前端 localStorage。
- 邮箱不存在和密码错误使用相同的 401 消息；不因邮箱是否存在而返回不同提示。
- 登录 JSON 最多 4 KiB；真实连接 IP 每分钟最多 10 次尝试，最多同时执行 2 个密码哈希。超过限制返回 429 和 Retry-After。
- 不信任未经配置的 X-Forwarded-For；同一反向代理可能共享限流桶。需要大规模代理部署时应明确设计可信代理策略，不能直接信任外部传入的 IP 头。
- 服务重启后需重新登录，账号与资料保留；退出会撤销当前会话。

## 验证与边界

`python scripts/verify_admin.py` 使用随机端口、随机测试密码和独立临时目录验证真实首启、缺配置失败、密码哈希、旧密钥不能登录、重启保留、同邮箱不重设以及不同邮箱拒绝。不会读取真实 `.env`、`.local` 或真实管理员密码。

`verify_live.py` 完成邮箱登录后的全业务浏览器回归；`verify_package.py --source` 验证便携版与源码入口。PostgreSQL 条件用例需专用 `TEST_DATABASE_URL`，没有实库执行时必须明确标记未验证。

本次只实现单一系统管理员，未增加开放用户注册、SMTP 验证、找回密码、密码重置或多用户/RBAC。请安全保管真实密码及工作区备份。
