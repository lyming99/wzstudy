# 系统管理员初始化与邮箱登录 · v0.3.0

> 管理员使用同一账号和会话访问自己的资料、学习功能，并额外拥有用户管理权限；不能直接访问其他用户的私人资料。入口为 `/workspace/` 与 `/learn/`，详见 `USER_WORKSPACES.md`。


## 与 wenzflow 对齐的规则

参考工程仅作为源码参考，没有修改其代码、配置或数据。WzStudy 使用同样的启动引导语义：

1. 服务首次创建系统管理员时必须收到邮箱和密码；本机启动器可在配置均为空时生成随机密码并写入 `.env`，没有公开默认密码。
2. 首次初始化在监听 HTTP **之前**完成，写入原有存储事务。
3. 已有管理员时，同邮箱启动不重设密码、显示名或身份；不提供初始化变量也可正常重启。
4. 服务收到与已有管理员不同的邮箱时拒绝启动，不替换旧管理员、不覆盖原资料；默认本机启动器会传入已配置邮箱供服务核对，但不会重设已有密码。
5. 不提供匿名注册、初始化或提权的 HTTP 接口。客户端来源不属于可登录的普通用户。
6. 账号数据库中使用独立随机盐的 Argon2id（64 MiB、3 次、并行度 1），不保存明文密码。本机启动器将首次生成的初始密码写入受 Windows 用户 ACL 保护的 `.env`；该文件包含明文秘密，须限制访问并安全备份。

## Windows 本地初始化（推荐）

在仓库根目录的 `.env` 中同时设置 `SYSTEM_ADMIN_EMAIL` 和 `SYSTEM_ADMIN_PASSWORD`，然后运行 `.\start-local.ps1`。该入口启动本机 Docker PostgreSQL，仅在空库首次创建管理员时使用这些配置；普通启动以 `.env` 为准，显式 `-AdministratorEmail your@email.example` 覆盖邮箱。只设置邮箱或密码时拒绝初始化（显式参数可以提供邮箱），避免意外使用默认账号。

若邮箱和密码均未配置，默认使用 `admin@wzstudy.local` 和随机初始密码，也可通过 `-AdministratorEmail` 指定邮箱。生成的邮箱和密码写入 `.env`，不会创建 `admin-initial.json`；`.env` 含明文秘密，不要分享或提交，应限制访问并安全备份。若旧版未完成初始化的随机密码文件存在，启动器会校验并迁入；与新配置冲突时保留双方并报错。

访问终端显示的工作台地址，使用邮箱和密码统一登录，无需选择身份；服务端核验管理员密码并根据配置邮箱判定管理权限。旧 `/admin/` 页面继续可用。普通重启不重置已有管理员；配置的邮箱与已有管理员不一致时，服务会拒绝启动，不自动转移身份。旧文件模式的 `-InitializeAdmin` 不再适用于此入口。邮箱允许首尾空白及大小写差异，密码逐字节匹配，不去除首尾空白。

## 环境变量 / Docker / PostgreSQL

首选配置名称与 wenzflow 一致：

| 配置 | 规则 |
| --- | --- |
| `SYSTEM_ADMIN_EMAIL` | 首次必填，严格邮箱格式，归一化为小写 |
| `SYSTEM_ADMIN_PASSWORD` | 首次必填，8–128 UTF-8 字节，无 NUL；建议 12 位以上随机密码 |
| `SYSTEM_ADMIN_DISPLAY_NAME` | 可选，默认“温知管理员”，最多 120 UTF-8 字节 |

兼容 `BOOTSTRAP_ADMIN_EMAIL/PASSWORD`；显示名还兼容 `SYSTEM_ADMIN_USERNAME`、`BOOTSTRAP_ADMIN_DISPLAY_NAME/USERNAME`。默认本机 Docker 启动器以 `.env` 为准；`scripts/start-dev.ps1 -Postgres` 则按逻辑配置项优先使用非空进程环境。密码原始空白保留；`.env` 最外层单/双引号可去除，不执行其中代码、变量或转义。

可复制根 `.env.example`，为默认本地 PostgreSQL 启动器或单独的 `scripts/start-dev.ps1 -Postgres` 安全编辑上述字段。Docker Compose 和手动运行也支持这些环境变量；不要把真实密码提交或分享。

初始化成功后可以从环境中移除初始化密码，再重启验证；账户哈希已经持久保存。不要把带真实密码的 `.env` 提交、打包或截图公开。留在环境中的无效非空配置仍可能在配置校验阶段被拒绝。

- `-NoEnvFile`：不读取根 `.env`；本机启动器仅允许使用已创建且归属匹配的卷，必须在进程环境提供完整数据库、API 密钥及卷身份信息，不能借此生成新库凭据。
- `-Check`：只返回解析后的路径和端口，不读取密码、不开服务、不初始化账号。
- `scripts/start-dev.ps1 -Postgres`：配置连接串及管理员环境后先迁移再启动。
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

`verify_live.py` 是邮箱登录后的历史文件模式浏览器回归；`verify_package.py` 使用隔离的本机 Docker PostgreSQL 验证发行包，`--static` 仅检查包结构，不代表实库通过。PostgreSQL 条件用例需专用 `TEST_DATABASE_URL`，没有实库执行时必须明确标记未验证。

本次只实现单一系统管理员，未增加开放用户注册、SMTP 验证、找回密码、密码重置或多用户/RBAC。请安全保管真实密码及工作区备份。
