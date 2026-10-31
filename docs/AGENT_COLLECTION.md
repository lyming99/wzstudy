# 采集 Agent 部署与运维

本模块由两个独立项目组成：

```
┌──────────────────────────────┐        HTTP (Bearer wzag_...)        ┌─────────────────────────────┐
│ wzstudy 服务端                │ ◀─────────────────────────────────── │ wzstudy-agent 采集端         │
│  - 采集端身份 / 任务 / 租约    │                                      │  - claim 任务                │
│  - 进度事件 / 入库回执         │ ───────────────────────────────────▶ │  - agent loop + function call│
│  - 复用现有 Source 入库链路    │        任务、取消信号                  │  - fetch_url / ingest_items  │
└──────────────────────────────┘                                      │  - skill 目录                │
                                                                       └─────────────────────────────┘
```

- 服务端不含任何 LLM 依赖，模型凭据只存在于采集端。
- 采集端只出站访问服务端与公网；服务端不会反向连接采集端。
- 采集端写入的资料仍走现有 `Source` 入库、审核/发布与客户端查询链路。

## 一、服务端启用

采集模块**默认开启，无需任何配置**。启动服务后，所有账户都可以在 `/workspace/` 的「Agent 服务 → 收集任务」派发任务；管理员的「系统管理 → 收集任务」使用管理员个人工作区。

- 现有数据库需要应用迁移 `000009_agent_collection.sql`；`start-local.ps1` 启动时会自动执行，手动部署请运行 `go run ./cmd/migrate`。
- 普通账户可以在 `/workspace/` 的「Agent 接入」中创建自己的采集端身份，并通过 `/api/v1/workspace/agent/*` 派发任务；管理员使用 `/api/v1/admin/agent/*` 管理管理员个人工作区与内置能力。
- 如需关闭：设置 `AGENT_ENABLED=false` 并重启，采集端协议 `/api/v1/agent/*` 返回 404；工作区与管理页面会显示“未启用”，管理员配置入口仍保留以便重新启用。
- 其余参数均有默认值，仅在需要调整租约、配额或事件大小时才需配置：

```powershell
$env:AGENT_LEASE_SECONDS = "300"
$env:AGENT_TASK_MAX_ITEMS = "100"
$env:AGENT_TASK_MAX_TIMEOUT_SECONDS = "1800"
$env:AGENT_EVENT_MAX_BYTES = "8192"
$env:AGENT_EVENT_TOTAL_BYTES = "262144"
```

## 二、派发任务

所有账户登录 `/workspace/` 后都可以使用「Agent 服务 → 收集任务」：普通账户调用 `/api/v1/workspace/agent/*`，管理员调用 `/api/v1/admin/agent/*`。两种入口都绑定当前用户的工作区，任务、事件、模板、采集端身份与入库资料按账户隔离。

在「收集任务」板块可以：

- 查看待领取、执行中、已完成、失败/过期计数；
- 点击「派发任务」，选择来源、填写采集目标、配额、轮次、工具调用上限、超时、优先级、允许域名，可指定某个采集端；
- 在任务列表查看入库进度、尝试次数、采集端与更新时间；
- 打开详情查看统计与逐轮事件（轮次、工具调用、工具结果、结束）；
- 对进行中的任务「取消」，对失败/取消/过期任务「重试」。

板块依赖的接口：`/api/v1/workspace/agent/status`、`/tasks`、`/agents` 与 `/api/v1/workspace/sources`；管理员页面使用对应的 `/api/v1/admin/agent/*`。

自动化场景也可以直接调用 API。普通账户使用用户会话令牌和 `/workspace/agent` 前缀；管理员可用管理员 API Key 和 `/admin/agent` 前缀：

```bash
# 普通账户：创建自己的采集端（access_key 只返回一次）
curl -X POST http://127.0.0.1:8091/api/v1/workspace/agent/agents \
  -H "Authorization: Bearer $USER_SESSION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"slug":"home-collector","name":"家庭采集端","description":"采集公开技术文章"}'

# 普通账户：创建收集任务（source_id 必须是本人工作区的来源；留空自动创建）
curl -X POST http://127.0.0.1:8091/api/v1/workspace/agent/tasks \
  -H "Authorization: Bearer $USER_SESSION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "goal": "采集 Go 官方博客最近三篇文章并整理成学习资料",
    "constraints": {"max_items": 3, "language": "zh-CN", "tags": ["Go"], "allow_domains": ["go.dev"]},
    "budget": {"max_rounds": 6, "max_tool_calls": 12, "timeout_seconds": 300}
  }'

# 查看进度
curl http://127.0.0.1:8091/api/v1/workspace/agent/tasks/<task-id> -H "Authorization: Bearer $USER_SESSION_TOKEN"
curl http://127.0.0.1:8091/api/v1/workspace/agent/tasks/<task-id>/events -H "Authorization: Bearer $USER_SESSION_TOKEN"

# 取消 / 重试 / 删除已结束任务
curl -X POST http://127.0.0.1:8091/api/v1/workspace/agent/tasks/<task-id>/cancel \
  -H "Authorization: Bearer $USER_SESSION_TOKEN" -H "Content-Type: application/json" -d '{"reason":"不再需要"}'
curl -X POST http://127.0.0.1:8091/api/v1/workspace/agent/tasks/<task-id>/retry \
  -H "Authorization: Bearer $USER_SESSION_TOKEN" -H "Content-Type: application/json" -d '{}'
curl -X POST http://127.0.0.1:8091/api/v1/workspace/agent/tasks/<task-id>/delete \
  -H "Authorization: Bearer $USER_SESSION_TOKEN" -H "Content-Type: application/json" -d '{}'
```

管理员将上面的 `/workspace/agent` 换成 `/admin/agent`，并使用管理员会话或管理员 API Key 即可。

## 三、构建与配置采集端

### 使用分发包（推荐）

`dist/` 下已提供：

```
dist/wzstudy-agent.exe                         采集端程序（Windows x64）
dist/wzstudy-agent-v0.1.0-windows-amd64.zip    采集端分发包
dist/wzstudy-full-v0.4.0-windows-amd64.zip     一体化发布包（服务端内置 agent + 分发包）
dist/agent.env.example                         配置模板
dist/start-agent.ps1 / start-agent.cmd         启动脚本（默认 doctor 自检）
dist/skills/                                   随包技能
dist/AGENT_README.md                           使用说明
```

一体化发布包由 `scripts/package-full.ps1` 生成：先构建 agent，再构建内嵌 agent 的服务端，最后把服务端、迁移工具、启动脚本、文档与 agent 分发包一起打包。

解压后编辑 `agent.env`，填写 `WZSTUDY_SERVER_URL`、`WZSTUDY_AGENT_KEY`、模型配置，运行 `start-agent.cmd`（自检）→ `start-agent.cmd run`（常驻）。

配置文件加载顺序：进程环境变量 > `AGENT_CONFIG` 指定的文件 > 当前目录 `agent.env`。

### 在后台发布程序包

管理员登录后进入「系统管理 → 收集任务 → 程序发布」，可以上传采集端分发包：

- 支持 `.exe` / `.zip` / `.msi` / `.tar.gz`，单个不超过 64 MiB；
- 填写版本号（如 `0.1.0`）、平台（Windows/Linux/macOS）、架构（amd64/arm64）与更新说明；
- 同一「版本 + 平台 + 架构」不可重复；删除后可以重新上传；
- 程序包保存在 `DATA_DIR/agent-releases/<release-id>`，元数据随工作区记录保存；
- 下载需要管理员会话或管理员 API Key，返回原始文件名；删除会同时移除文件。

对应接口（均需管理员鉴权）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/agent/releases` | 发布列表 |
| POST | `/api/v1/admin/agent/releases` | multipart 上传（version/platform/arch/notes/file） |
| GET | `/api/v1/admin/agent/releases/{id}/download` | 下载程序包 |
| POST | `/api/v1/admin/agent/releases/{id}/delete` | 删除程序包 |

### 普通账户：自行配置采集端

普通账户在「Agent 服务 → Agent 接入」中：

- 点击「下载程序」获取单独的 `wzstudy-agent.exe`；
- 点击「新建接入」创建自己的采集端身份。在密钥弹窗中选择模型供应商、填写模型名称和 API Key，点击「下载agent程序」即可得到包含采集端程序、`agent.env`、启动脚本和 skills 的完整包；
- 创建后可在「采集端身份」列表点击「查看密钥」再次查看 Agent 密钥，不需要重新轮换；旧版本创建的身份没有明文副本，采集端下次领取任务时服务端会用认证密钥自动补存，若暂时不连接也可轮换一次后查看；
- 列表操作中的「删除」可移除身份。删除需要提交当前 `revision`；如果该身份还有待领取或被领取中的任务，服务端返回 `409 agent_in_use`，需先取消或删除这些任务。删除后密钥立即失效，原标识可以重新使用；
- 也可以复制弹窗中的 `agent.env` 模板，自行填写 `WZSTUDY_SERVER_URL`、`WZSTUDY_AGENT_KEY`、`AGENT_MODEL_BASE_URL`、`AGENT_MODEL_API_KEY`、`AGENT_MODEL`；
- 解压 ZIP 后运行 `start-agent.cmd`，或把 `agent.env` 与程序放在同一目录运行 `wzstudy-agent.exe run`。

弹窗中的 `agent.env` 预览会把 Agent 密钥和模型 API Key 打码；下载的 ZIP 和「复制完整 agent.env」使用真实值。ZIP 包内会写入当前 Agent 密钥和模型配置；模型 API Key 只存在于压缩包，服务端不保存、不代管，也不会替普通账户启动采集端。打包成功后，服务端会记住模型供应商、模型名称和 Base URL，下次「下载agent程序」自动带入；模型 API Key 仍需重新填写。对应的打包接口是 `POST /api/v1/workspace/agent/agents/{id}/package`，请求需携带 `access_key` 和 `model` 配置。普通账户看不到「服务端部署」「一键部署」和「程序发布」。

### 管理员：一键部署采集端（仅内置能力）

管理后台「系统管理 → Agent 接入」提供：

- **一键部署**：选择模型供应商（DeepSeek / OpenAI / 通义千问 / 自定义）、填写模型名称与 API Key（可选自定义 Base URL），服务端创建采集端身份并直接下载 zip。包内 `agent.env` 已预填模型配置，解压后运行 `start-agent.cmd` 即可启动（API Key 留空时需自行补填）。
- **模型选择**：一键部署弹窗中点击「查询模型」后自动展开完整候选列表，点击箭头可再次展开；已有输入不会过滤掉其他模型。支持点击、方向键与 Enter 选择，Esc 收起列表，也可手动输入。切换供应商或修改 API Key / Base URL 会清空旧候选；过期请求不会覆盖当前配置。专项浏览器回归：构建 `.artifacts/wzstudy.exe` 后运行 `python scripts/verify_deploy_model_picker.py`（隔离服务和模拟模型列表，不使用真实模型密钥）。
- **下载程序**：直接下载服务端内置的 `wzstudy-agent.exe`。
- **录入端列表**：查看、配置、停用 Agent 录入来源，并打开接入说明。

模型密钥只写入下载的压缩包，不保存到服务端、不写入日志。`agent.env` 中的模型配置：

```ini
AGENT_MODEL_BASE_URL=https://api.deepseek.com/v1
AGENT_MODEL_API_KEY=sk-...
AGENT_MODEL=deepseek-chat
```

### 数据类型与工具预算

派发收集任务时可以指定数据类型：

- 视频 / 音频：先通过 `/agent/uploads` 上传媒体文件，条目 `assets` 引用返回的资产 `id`；服务端会按上传登记信息校验 MIME 和大小，并在首次入库时把资产绑定到任务来源；
- 文章 / 图文：条目类型必须匹配；
- 不限：接受任意类型。

最大轮次默认 **100 轮**（最大 1000），工具调用上限默认 **100 次**（最大 1000），可在派发表单中调整。

### 服务端部署模式（仅管理员）

如果采集端与服务端在同一台机器，可以直接由服务端托管 agent 进程。该内置采集端、模型密钥和运行日志只对管理员开放，普通账户必须自行运行采集端：

- 在「系统管理 → Agent 接入 → 服务端部署」填写**程序目录**（默认安装目录下的 `agent`）与**工作目录**（默认 `agent\work`），两者都可以修改；
- 同一面板可配置 **AI 模型**：供应商（DeepSeek / OpenAI / 通义千问 / 自定义）、模型名称、API Key、Base URL，支持从供应商 `/models` 查询模型；
- 程序目录需包含 `wzstudy-agent.exe` 与 `agent.env`；工作目录不存在时会自动创建；
- 服务端启动 agent 时通过环境变量注入模型配置，覆盖 `agent.env` 中的同名配置；API Key 不回显，留空保存时保留原密钥；
- 保存后可「启动采集端 / 停止」，页面显示运行状态、PID 与最近日志；
- 勾选「服务端启动时自动运行」后，服务端重启会自动拉起 agent；
- 默认路径可用环境变量 `AGENT_INSTALL_DIR`、`AGENT_WORK_DIR` 覆盖；
- 服务端只管理一个本地 agent 进程；停止时终止进程，未完成任务由租约超时回收。

接口（均需管理员鉴权）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/admin/agent/runtime` | 读取运行时状态与日志 |
| PUT | `/api/v1/admin/agent/runtime` | 保存工作路径与自动启动 |
| POST | `/api/v1/admin/agent/runtime/start` | 启动 agent |
| POST | `/api/v1/admin/agent/runtime/stop` | 停止 agent |

RSS 采集已拆到「资料数据服务 → RSS 订阅」，与 Agent 接入互不影响。

发布构建会把 agent 程序嵌入服务端，因此一键部署不依赖外部文件；开发构建未嵌入时接口返回 503 并提示使用完整发布包。

### 从源码构建

```powershell
cd agent
go build -o wzstudy-agent.exe ./cmd/wzstudy-agent
```

| 环境变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `WZSTUDY_SERVER_URL` | 是 | — | 服务端地址，如 `http://127.0.0.1:8091` |
| `WZSTUDY_AGENT_KEY` | 是 | — | `wzag_` 开头的采集端密钥 |
| `AGENT_MODEL_BASE_URL` | 否 | `https://api.openai.com/v1` | OpenAI 兼容端点，自动追加 `/chat/completions` |
| `AGENT_MODEL_API_KEY` | 否 | 空 | 本地模型可留空 |
| `AGENT_MODEL` | 是 | — | 模型名 |
| `AGENT_SKILLS_DIR` | 否 | `skills` | SKILL.md 所在目录 |
| `AGENT_WORK_DIR` | 否 | `.state` | 本地工作目录（当前仅保留） |
| `AGENT_POLL_SECONDS` | 否 | `5` | 空闲轮询间隔 |
| `AGENT_WAIT_SECONDS` | 否 | `20` | 领取长轮询时长（0-25） |
| `AGENT_MAX_FETCH_BYTES` | 否 | `2097152` | 单次抓取上限 |
| `AGENT_ALLOW_PRIVATE_NETWORK` | 否 | `false` | 是否允许抓取内网/回环地址（仅测试环境开启） |
| `AGENT_LOG_LEVEL` | 否 | `info` | `debug` / `info` / `warn` / `error` |

示例：

```powershell
$env:WZSTUDY_SERVER_URL = "http://127.0.0.1:8091"
$env:WZSTUDY_AGENT_KEY = "wzag_..."
$env:AGENT_MODEL_BASE_URL = "https://api.deepseek.com/v1"
$env:AGENT_MODEL_API_KEY = "sk-..."
$env:AGENT_MODEL = "deepseek-chat"
.\wzstudy-agent.exe doctor   # 校验配置、服务端连通性、权限，不写入数据
.\wzstudy-agent.exe once     # 领取并执行一个任务
.\wzstudy-agent.exe run      # 常驻
```

## 四、Skill 接入

采集端扫描 `AGENT_SKILLS_DIR` 下的 `<name>/SKILL.md` 或 `<name>.md`：

```
skills/
  web-article/SKILL.md
```

frontmatter 支持 `name` / `description` / `when_to_use`。启动时只把目录摘要注入 system prompt，模型通过 `load_skill` 工具按需加载全文（单次运行最多 8 次、单个文件 ≤24 KiB）。

已内置 `agent/skills/web-article` 作为示例。也可以把本项目 `skills/bilibili-collect` 的规则精简后放入采集端目录。

**不支持**脚本型 skill（执行 python/node/shell）：采集端刻意不提供命令执行能力。

## 五、安全模型

服务端：

- 采集端密钥的哈希用于认证，同时在所属工作区记录中保存可查看副本；创建、轮换或查看时返回。可查看副本意味着能访问数据库的人也能读取，不能把它当作不可导出的秘密；模型 API Key 仍不保存。停用立即失效。
- 任务创建时固定来源并校验来源属于当前工作区、已启用且允许 `ingest`。
- 入库时再次校验条目配额、允许域名与字段长度；`source_id` 永远取自任务，不信任采集端。
- 事件文本按字节限额截断并计入任务总量，避免被当作存储放大通道。
- 任务领取与终态在串行写事务中完成，多采集端不会重复消费。

采集端：

- `fetch_url` 仅允许 http/https，解析后校验 IP 并直连已验证 IP（防 DNS 重绑定），拒绝回环/私网/链路本地/多播；重定向最多 3 跳并逐跳校验。
- 网页正文与 skill 内容一律视为不可信数据，system prompt 明确不得改变任务范围或索取凭据。
- 不执行命令、不读取任务未授权的本地文件、日志不记录密钥与模型密钥。

## 六、故障排查

| 现象 | 排查 |
|---|---|
| `doctor` 返回 401 | 密钥错误或已被轮换；重新创建或轮换密钥 |
| `doctor` 返回 404 | 服务端 `AGENT_ENABLED` 未开启 |
| `claim` 长期 204 | 没有待领取任务；任务是否属于同一工作区、来源是否停用 |
| `lease_expired` / `task_claimed` | 采集端时钟偏差过大、心跳间隔过长或任务已被回收；查看服务端日志与任务 attempts |
| `item_quota_exceeded` | 超过 `max_items` 或事件总量；缩小任务配额或拆分任务 |
| `domain_not_allowed` | 条目 URL 不在 `allow_domains` 内；修正采集目标或任务约束 |
| agent 退出码非 0 | 查看 `.tmp/e2e-agent.log` 或运行日志中的 `error` 字段 |

## 七、当前限制

- 所有账户的任务板块覆盖派发、列表、详情、取消、重试与删除；采集端身份的创建、轮换与删除通过「Agent 接入」或工作区 API 完成。
- 普通账户只能使用自己运行的采集端；服务端托管运行时、模型密钥、一键部署和程序发布仅管理员可用。
- 只支持文本/Markdown 条目入库，不支持媒体上传（v2 计划）。
- 没有搜索工具、浏览器渲染和定时任务（定时可由外部 cron 调 `once` 实现）。
- 采集端单并发，`capacity` 预留 1-4。


## 内置 Agent：测试模型连接与应用配置

- 「测试连接」使用当前表单中的供应商、Base URL、模型名称和 API Key，向 OpenAI 兼容的 `/chat/completions` 发起一次极短请求（20 秒超时）；不是只查询模型列表。可能产生少量 Token 费用，但不保存配置、不执行采集任务。
- API Key 留空时可使用管理员已保存的模型密钥；勾选清除密钥时，测试不会使用已保存密钥。测试结果不返回上游原始响应或密钥，且不跟随 HTTP 重定向。
- 「保存并重启」提交 `PUT /api/v1/admin/agent/runtime` 并带 `restart: true`。配置先保存，再等待旧进程退出后启动新进程；未运行时直接启动。若重启失败，返回明确的“配置已保存，但重启失败”提示。
- 「重启内置 Agent」调用 `POST /api/v1/admin/agent/runtime/restart`，仅使用已保存配置，不会保存当前未提交的表单。
- 测试接口：`POST /api/v1/admin/agent/runtime/test-connection`，请求字段为 `provider`、`base_url`、`model`、`api_key`、`use_saved_key`、`clear_api_key`；成功返回 `connected`、`model`、`latency_ms`。
- 这些接口仅管理员可用。进程重启会中断进行中的采集任务，任务在租约到期后按既有规则回收重试；重要任务运行时应等待完成后再重启。


## 采集去重、媒体预算与失败详情

- 新采集任务默认 `constraints.duplicate_policy=skip`，只新增；按同来源的稳定外部 ID 查重，B站 BV 与规范视频 URL 还会在同工作区跨来源比对，并视为同一内容，已删除的记录也保持拦截。允许更新需显式选 `update`，不会恢复已删除条目。
- `check_existing` 工具在下载/上传前提交最多20个候选，服务端 `/api/v1/agent/tasks/{id}/duplicates` 只返回候选的存在布尔值，不赋予资料库查询或正文读取权限，并校验任务归属、attempt、租约和工作区。
- 入库事务会再次查重以防并发或模型漏查，重复返回 `skipped`，不消耗新增配额，也不覆盖原资料。已有的非规范历史 ID 不自动改写或合并；这里只按稳定 ID 和已知 B站 URL 表示匹配，不做标题相似度或媒体内容指纹判重。
- 未显式填写预算时，视频/音频默认1800秒（受服务端策略上限约束），其他类型仍300秒；显式预算、既有任务及模板中已保存的预算不变。
- 工具失败事件保存具体错误文本，中文按完整 UTF-8 字符截断；超时报告与取消区分，末批事件在有界收尾时间内尝试上报。部分入库结果不会因任务超时自动回滚。
