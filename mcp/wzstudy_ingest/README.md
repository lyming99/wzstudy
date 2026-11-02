# WzStudy 统一录入 MCP 适配器

这是可运行的 stdio 服务，不是配置说明占位。它复用 `skills/wzstudy-push/scripts/wzstudy_push.py` 的认证、标准化、请求、上传及回执，不直接写数据库，也不建设第二套资料管理系统。

## 能力与边界

| 工具 | 能力 | 凭据 |
| --- | --- | --- |
| `wzstudy_ingest_status` | 只读检查服务就绪和当前来源的 ingest 权限 | 来源 Key |
| `wzstudy_ingest_items` | 1–100 条资料、一个不超过 4 MiB 的原子 API 批次；支持 dry_run | 来源 Key |
| `wzstudy_upload_files` | 实际媒体/文本上传，仅在明确配置 --allow-root 后提供；支持 dry_run | 来源 Key |
| `wzstudy_catalog_list` / `_get` | 只读查询本人工作区的目录合集与章节 | 用户会话 |
| `wzstudy_catalog_save` | 新建（revision=0）或按最新 revision 覆盖合集；保存即生效 | 用户会话 |
| `wzstudy_catalog_delete` | 只删除合集与章节关系，不删除其中资料或学习记录 | 用户会话 |
| `wzstudy_roadmap_list` / `_get` | 只读查询本人工作区学习路线图与全部节点 | 用户会话 |
| `wzstudy_roadmap_save` | 新建或覆盖路线图节点树；最多 300 节点、12 层，根标题须等于路线名 | 用户会话 |
| `wzstudy_roadmap_delete` | 只删除路线图，不删除资料或个人学习进度 | 用户会话 |

目录合集与学习路线图工具**默认不出现**，只有在显式配置用户会话凭据后才注册；未配置时工具清单与旧版本完全一致。两类工具复用后端 `/workspace/catalog/collections`、`/workspace/roadmaps`，不新建第二套存储。

不提供资料查询、审核发布、身份管理、任意 HTTP、外链下载、推送或任意本机文件读取。入库成功即对客户端发布，不经过人工审核；回执中的 `published` 数量表示本次已公开、客户端可直接查询的资料数。

工具参数只能是资料、合集/路线图内容及允许文件，不接受 base_url、source_id、source_key、user_token、config 等覆盖参数。凭据在启动时从用户明确配置加载，不进入工具列表、响应、日志或 URL。写入不自动重试；提交不确定时返回 commit_uncertain，应先核对结果。文件上传不是无副作用重试，可能新增资产和资料修订。

### 合集与路线图写入规则

- 新建用 `revision=0`（省略即 0）；更新必须带当前 revision，否则本地即拒绝，服务端仍以 409 冲突为准。
- 先 `_get` 读取最新 revision，不要覆盖其他窗口的修改；保存/删除都不是“清理缓存”。
- 合集 `chapters` 每项只接受 `id`、`title`、`item_id`；新章节省略 `id` 由服务端生成，`item_id` 必须是本人工作区中真实存在的资料。
- 路线图节点接受 `id`、`parent_id`、`parent_index`、`title`、`note`、`item_ids`、`collapsed`。缺省 `id` 时适配器生成稳定 UUID；`parent_index` 指向同一数组中的父节点，便于从任意树的写法直接提交，不能与 `parent_id` 同时给出。
- 需恰好一个根节点（`parent_id`/`parent_index` 都为空），根标题必须等于路线图 `title`；节点标题非空，节点说明 ≤4000 字符，每个节点最多关联 20 份资料。
- 两者都支持离线 `dry_run`，返回将要使用的 method/path/body 而不联网。
- 鉴权失败会返回 `user_session_required`（401）或 `workspace_denied`（403）；来源 Key、查询 Client Key 都**不能**替代用户身份。

## 启动与配置

需要项目现有 Python 3.11+ 环境，无第三方 Python 依赖。运行目录不必是项目根，但脚本与共享 Skill 目录需要一起存在。

1. 在“数据录入 → 录入端管理”创建专用来源，kind=manual、integration=mcp、权限 ingest/upload。
2. 将一次性来源 Key 保存到仅本机可读的私有配置；不要使用管理员或查询客户端 Key。
3. 在 AI 应用的 MCP 设置中配置 stdio 命令。应用负责工具权限与调用确认；项目不会自动添加服务或修改其信任设置。

私有配置模板（不是实际凭据）：

```json
{"base_url":"http://127.0.0.1:8091","source_id":"<录入来源 UUID>","source_key":"<专用 wzsk_ Key>"}
```

```powershell
python D:\project\wzstudy\mcp\wzstudy_ingest\server.py --config <私有配置绝对路径>
# 需要文件上传时，显式指定允许目录，可重复设置：
python D:\project\wzstudy\mcp\wzstudy_ingest\server.py --config <私有配置绝对路径> --allow-root <授权媒体目录>
```

### 启用合集/路线图管理（可选）

这两组工具需要**独立**的用户会话令牌，不能与来源 Key 互换。先在工作台登录同一账号，用 `session_mode=token` 调用 `POST /api/v1/user/auth/login` 获取 `wzus_` 令牌（有效期 8 小时），单独保存为私有配置：

```json
{"user_token":"<wzus_ 用户会话令牌>"}
```

```powershell
python D:\project\wzstudy\mcp\wzstudy_ingest\server.py --config <来源配置> --workspace-config <用户会话配置>
```

- 用户会话配置只接受 `base_url`、`user_token` 两个字段；省略 `base_url` 时回退到已校验的来源地址。
- 若两者指向不同 origin 会直接拒绝启动，不会把用户令牌转发到另一台服务器。
- 也可用环境变量 `WZSTUDY_USER_TOKEN`（配合 `WZSTUDY_BASE_URL`）；同样不会从未配置的路径自动读取令牌。
- 令牌过期或撤销后，工具返回 `user_session_required`；重新登录获取新令牌并重启进程，不要用来源 Key 顶替。
- 管理工具只作用于该用户自己的工作区，管理员 API Key 不因此获得他人工作区访问权。

在 AI 应用中使用的通用服务定义（字段形状以该应用设置为准）：

```json
{
  "name":"wzstudy-ingest",
  "transport":"stdio",
  "command":"python",
  "arguments":["<项目绝对路径>/mcp/wzstudy_ingest/server.py","--config","<私有配置绝对路径>"]
}
```

也兼容共享客户端的 WZSTUDY_SOURCE_ID / WZSTUDY_SOURCE_KEY 环境变量对和 WZSTUDY_BASE_URL。不要将真实值填入工具调用或公开配置。服务端点固定到启动配置，拒绝重定向及非 HTTPS 的远程地址；回环 HTTP 仅用于本机开发。

文件路径解析为真实绝对路径后须位于任一允许目录内，符号链接越界同样拒绝。只上传明确授权的文件；授权目录应专用于媒体且不由不可信进程同时修改。适配器仍复用文件类型、数量、大小和文件名校验，服务端再校验实际内容。

## 协议支持范围

- MCP 2026-07-28：每个请求在 params._meta 中携带 protocolVersion、clientInfo、clientCapabilities；实现 server/discover、tools/list、tools/call、ping 和 complete 结果。
- 兼容初始化模式的 2025-11-25、2025-06-18、2025-03-26：initialize、notifications/initialized 后调用工具。
- 不支持的现代协议版本返回 -32022 及 supported 列表；不宣称支持订阅、任务、资源、提示词、扩展协商或远程 Streamable HTTP。
- stdio 每行一个 UTF-8 JSON-RPC 消息，stdout 仅协议消息；通知不返回响应，也不会把无 id 的工具调用当作写入执行。请求限制 5 MiB。
- 同步处理调用，网络超时通过 --timeout 设置，默认 60 秒、允许 1–300 秒；不实现中途撤销已提交写入的承诺。取消通知不会回滚服务端已提交事务。

核对实现时使用的官方规范：

- https://modelcontextprotocol.io/specification/2026-07-28/basic/lifecycle
- https://modelcontextprotocol.io/specification/2026-07-28/server/discover
- https://modelcontextprotocol.io/specification/2026-07-28/server/tools
- https://modelcontextprotocol.io/specification/2025-11-25/basic/transports

本实现仅实现以上声明的工具子集，不把通过单元测试等同于通过所有第三方 MCP 应用认证。

## 验证

```powershell
python -m unittest discover -s mcp/wzstudy_ingest/tests -v
go build -o .artifacts/wzstudy.exe ./cmd/api
python scripts/verify_two_chains.py
```

单测覆盖协议发现、初始化、参数隔离、无 id 通知、未知版本、离线预检、文件目录边界及不确定写入不重试；另覆盖合集/路线图工具的注册开关、分页与查询、revision 规则、路线图节点归一化与非法树拒绝、用户会话凭据的来源绑定。隔离集成测试启动真实 Go 服务和 MCP 子进程，验证 JSON 幂等入库、实际音频文件上传、回执、权限及不回显 Key，不提交真实用户资料。

合集与路线图工具目前只做了离线单测（使用 Fake 客户端，不连真实服务）；未在真实工作区创建或删除合集/路线图，也不把单测通过等同于第三方 AI 应用端到端认证。
