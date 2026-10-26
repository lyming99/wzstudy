# WzStudy 统一录入 MCP 适配器

这是可运行的 stdio 服务，不是配置说明占位。它复用 `skills/wzstudy-push/scripts/wzstudy_push.py` 的认证、标准化、请求、上传及回执，不直接写数据库，也不建设第二套资料管理系统。

## 能力与边界

| 工具 | 能力 |
| --- | --- |
| `wzstudy_ingest_status` | 只读检查服务就绪和当前来源的 ingest 权限 |
| `wzstudy_ingest_items` | 1–100 条资料、一个不超过 4 MiB 的原子 API 批次；支持 dry_run |
| `wzstudy_upload_files` | 实际媒体/文本上传，仅在明确配置 --allow-root 后提供；支持 dry_run |

不提供资料查询、审核发布、身份管理、任意 HTTP、外链下载、推送或任意本机文件读取。新来源默认待审核，已有来源以管理员配置和实际 published 回执为准。

工具参数只能是资料及允许文件，不接受 base_url、source_id、source_key、config 等覆盖参数。凭据在启动时从用户明确配置加载，不进入工具列表、响应、日志或 URL。写入不自动重试；提交不确定时返回 commit_uncertain，应先核对结果。文件上传不是无副作用重试，可能新增资产和资料修订。

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

单测覆盖协议发现、初始化、参数隔离、无 id 通知、未知版本、离线预检、文件目录边界及不确定写入不重试。隔离集成测试启动真实 Go 服务和 MCP 子进程，验证 JSON 幂等入库、实际音频文件上传、回执、权限及不回显 Key，不提交真实用户资料。
