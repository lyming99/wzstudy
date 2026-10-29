# 用户工作区与账号管理

## 当前权限边界

**管理员也有自己的资料与学习工作区，并额外拥有用户管理权限；所有人的资料按账号隔离。**

| 身份 | 可以操作 | 不可以操作 |
|---|---|---|
| 管理员用户会话 | 本人工作区的全部普通功能；额外创建、修改、停用/启用用户及重置用户密码 | 直接读取、修改其他用户的私人资料与学习记录 |
| 管理员 `ADMIN_API_KEY` | 用户账号管理自动化 | 代替用户会话读写个人资料或学习数据 |
| 普通用户会话 | 自己的资料录入、发布/下架、附件、来源、查询客户端、合集、路线图、学习与个人设置 | 操作其他用户数据、管理用户或授予管理员角色 |
| 用户创建的录入来源 Key | 该来源的状态、JSON 入库和授权后的文件上传 | 读取资料库、访问其他来源、管理账号、代替学习用户 |
| 用户创建的查询客户端 Key | 所属用户工作区内的已发布资料、媒体和授权后的增量同步 | 录入/上传、读取草稿、读写私人学习记录、跨用户查询 |
| 旧全局 `READ_API_KEY` | 无隐式工作区访问权 | 不能读取全局资料或冒充用户 |

“发布”现在表示资料可以被**本人及本人创建的查询客户端**读取，不表示向其他用户或互联网公开。

## 共用工作台与兼容入口

- `/workspace/` 是共用工作台。所有账号通过同一邮箱密码表单登录，无需选择身份；每个账号都能使用自己的资料工作区。服务端核验管理员邮箱和密码后额外授予用户管理权限，管理员首页同时显示个人资料概览与用户管理入口。普通用户看不到管理视图；服务端始终独立校验管理权限，不依赖前端隐藏菜单。原有 `/workspace/#library`、`/workspace/#roadmaps` 和 `/workspace/#users` 等有权限的深层链接仍可直接打开。
- `/admin/`：保留为原有管理员账号管理兼容入口。
- `/learn/`：所有账号的个人学习页面。和资料工作台共享用户登录，可查看自己的已发布资料，保存喜欢、收藏、笔记、草稿、进度和复习状态。

新部署仍使用原来的管理员初始化方式。管理员原账号的稳定 ID 即本人工作区所有者，不需创建第二个账号，也不会自动认领历史无主资料或其他用户的资料。新建或修改用户时不能新增与管理员邮箱相同的账号。旧版可能已有同邮箱、不同密码的独立账号：系统优先验证管理员密码；若使用的是原普通用户密码，则仍以普通用户身份访问原工作区，不获得管理权限。双方资料不自动合并，管理员可将旧普通用户改为不同邮箱；若密码相同，统一登录优先进入管理员账号，应先改用不同邮箱以避免歧义。管理员能够重置用户密码，因此不应将此边界表述为无法通过密码重置接管用户账号。

不开放匿名注册。浏览器统一使用 `/auth/login`，管理员与普通用户均使用 `wzstudy_user` 用户会话，管理员能力由服务端验证会话主体而非仅比对客户端提交的邮箱。浏览器成功切换账号会撤销该浏览器的旧会话，其他设备和原生客户端令牌不受影响；旧版本遗留的管理员 Cookie 会在用户会话校验时清理。打开的相关页面通过无身份信息的跨标签通知和焦点校验更新会话状态。

## 用户接口

基础路径 `/api/v1`。完整字段和所有业务路由见 `/openapi.json`。

```text
POST /auth/login                 # 统一 email/password；响应 role=admin 或 user
GET  /auth/session
POST /auth/logout
GET  /admin/users
POST /admin/users
PUT  /admin/users/{id}            # revision + 用户资料，password 可选

POST /user/auth/login            # 用户兼容接口，管理员也可使用；支持原生客户端 token 模式
GET  /user/auth/session
POST /user/auth/logout
POST /user/auth/password         # current_password + password

GET  /workspace/workspace
POST /workspace/items
PUT  /workspace/items/{id}
POST /workspace/uploads
POST /workspace/items/{id}/publish
POST /workspace/items/{id}/unpublish
POST /workspace/items/{id}/delete
GET  /workspace/sources
POST /workspace/sources
GET  /workspace/clients
POST /workspace/clients
```

原 `/api/v1/admin` 下的资料、来源、客户端、设置、合集和路线图接口已迁移到 `/api/v1/workspace`，接受普通用户或管理员的本人用户会话。**没有保留跨用户读写资料的兼容后门。** 原管理员目录下只保留用户管理。

创建用户示例（管理员认证）：

```json
{"email":"learner@example.test","display_name":"学习用户","password":"<安全生成并私下交付的初始密码>"}
```

用户原生客户端登录：

```json
{"email":"learner@example.test","password":"<用户密码>","session_mode":"token"}
```

`session_mode=token` 在成功响应中返回 `access_token`，后续使用 `Authorization: Bearer <access_token>`。浏览器默认使用 HttpOnly、SameSite=Strict 的 `wzstudy_user` Cookie，不在 JSON 中回传令牌；生产环境 Cookie 必须 Secure/HTTPS，Cookie 写请求必须具有本站 Origin。

密码使用随机盐 Argon2id；服务端仅保存会话令牌的 SHA-256，不持久化明文密码或明文会话令牌。会话有效期 8 小时；每个用户最多保留 16 个有效会话。退出只撤销当前会话；停用、重置密码或自行改密撤销全部旧会话，重新启用不会复活旧令牌。

浏览器 API 可附加 `X-WzStudy-User: <当前登录用户ID>` 作为**一致性保护**：Cookie 被其他标签切换为另一个用户时，旧标签的请求会被拒绝，而不是写入新账号。该字段不是所有权选择器，不能赋予访问他人的能力。

## 数据隔离实现与契约

- 所有业务请求由服务端从用户会话、来源 Key 或查询客户端 Key 确定唯一工作区，不接受 `user_id/owner_id/client_id` 作为普通用户的租户选择器。
- 来源、资料具有不可由输入指定的 `owner_id`；每个用户可以使用相同的来源 slug、客户端 slug 和外部资料编号。
- 附件、入库回执、设置、查询客户端、合集、路线图、反馈、草稿、个人合集、记录和学习游标均处在所有者记录命名空间内。详情、写入、引用和媒体读取与列表使用同一作用域，不靠前端筛选隔离。
- 内容增量流与合集增量流按用户独立计数；其他用户的变更不会推进本人的游标或泄漏其全局活动量。
- 来源 Key 由其拥有者建立作用域；查询 Key 由所属用户建立作用域。停用用户也会阻止其来源/查询 Key 继续读写。RSS 后台任务按来源的所有者进入工作区，不会把采集结果记到系统或其他用户。
- 学习接口的历史路径 `/client/learning`、`/client/feedback` 保留，但现在必须使用用户登录。能力版本为 2，明确返回 `user_id`、`identity=user`。
- 学习响应中的旧字段名 `client_id` 为兼容保留；使用用户会话时值是当前用户的稳定 ID，不是应用客户端 ID。会话轮换、跨设备登录和密码重置不改变数据所有者。
- 对于其他用户的资料/来源/媒体/合集/路线等 ID，按记录不存在处理；向他人资料上报学习事件同样被拒绝。

## 存量数据与迁移

新增迁移：`000007_learning_users.sql`、`000008_user_workspaces.sql`。PostgreSQL 启动前按现有迁移流程执行；就绪检查会检测所需索引。文件存储继续使用原版本的工作区文件，保留原数据与身份，不需要清库。

**历史无所有者的资料、来源、附件、客户端、学习记录和游标保留在旧空间，但不会自动归给管理员、首个登录用户、同邮箱用户或所有用户。** 历史来源/查询 Key 没有所有者时不再有业务数据访问权。账号管理页面也不提供批量接管资料的接口。

若确需迁入个人账号，需先备份，并在用户明确确认来源与归属后，使用该用户的工作台或本人来源 Key 显式录入；不能通过伪造 `owner_id` 重新绑定现有记录。此版本不提供未经核验的批量归属迁移。

## 验证

- `go test ./...`：核心存储、路由、权限、用户会话与用户隔离回归。
- `go vet ./...`：静态检查。
- `python scripts/verify_user_workspaces.py`：隔离临时文件存储与临时端口，运行真实服务和浏览器闭环；不读取或修改 `.local`。
- `python scripts/verify_shared_workbench.py`：隔离环境验证共用工作台、角色切换、管理权限和跨标签会话同步。
- `python scripts/verify_workspace_home.py`：隔离环境验证首页真实统计、用户资料隔离、快捷入口、深层链接及移动端布局。
- PostgreSQL 契约测试仅在配置专用 `TEST_DATABASE_URL` 时执行，并使用独立临时 schema；未配置时明确跳过，不能将文件/内存测试视为数据库实测。
