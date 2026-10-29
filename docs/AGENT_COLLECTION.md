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

采集模块**默认开启，无需任何配置**。启动服务后，管理后台「系统管理 → 采集任务」即可派发任务。

- 现有数据库需要应用迁移 `000009_agent_collection.sql`；`start-local.ps1` 启动时会自动执行，手动部署请运行 `go run ./cmd/migrate`。
- 如需关闭：设置 `AGENT_ENABLED=false` 并重启，`/api/v1/agent/*` 与 `/api/v1/admin/agent/*` 会整体消失（404）。
- 其余参数均有默认值，仅在需要调整租约、配额或事件大小时才需配置：

```powershell
$env:AGENT_LEASE_SECONDS = "300"
$env:AGENT_TASK_MAX_ITEMS = "100"
$env:AGENT_TASK_MAX_TIMEOUT_SECONDS = "1800"
$env:AGENT_EVENT_MAX_BYTES = "8192"
$env:AGENT_EVENT_TOTAL_BYTES = "262144"
```

## 二、在管理后台派发任务

管理员登录后，左侧「系统管理 → 采集任务」板块可以：

- 查看待领取、执行中、已完成、失败/过期计数；
- 点击「派发任务」，选择来源、填写采集目标、配额、轮次、工具调用上限、超时、优先级、允许域名，可指定某个采集端；
- 在任务列表查看入库进度、尝试次数、采集端与更新时间；
- 打开详情查看统计与逐轮事件（轮次、工具调用、工具结果、结束）；
- 对进行中的任务「取消」，对失败/取消/过期任务「重试」。

板块依赖的接口：`/api/v1/admin/agent/status`、`/tasks`、`/agents` 与 `/api/v1/workspace/sources`。

自动化场景也可以直接调用 API：

```bash
# 1) 创建采集端（access_key 只返回一次）
curl -X POST http://127.0.0.1:8091/api/v1/admin/agent/agents \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"slug":"home-collector","name":"家庭采集端","description":"采集公开技术文章"}'

# 2) 创建采集任务（source_id 必须是本工作区的来源）
curl -X POST http://127.0.0.1:8091/api/v1/admin/agent/tasks \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "source_id": "<source-uuid>",
    "goal": "采集 Go 官方博客最近三篇文章并整理成学习资料",
    "constraints": {"max_items": 3, "language": "zh-CN", "tags": ["Go"], "allow_domains": ["go.dev"]},
    "budget": {"max_rounds": 6, "max_tool_calls": 12, "timeout_seconds": 300}
  }'

# 3) 查看进度
curl http://127.0.0.1:8091/api/v1/admin/agent/tasks/<task-id> -H "Authorization: Bearer $ADMIN_API_KEY"
curl http://127.0.0.1:8091/api/v1/admin/agent/tasks/<task-id>/events -H "Authorization: Bearer $ADMIN_API_KEY"

# 4) 取消 / 重试
curl -X POST http://127.0.0.1:8091/api/v1/admin/agent/tasks/<task-id>/cancel \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" -d '{"reason":"不再需要"}'
curl -X POST http://127.0.0.1:8091/api/v1/admin/agent/tasks/<task-id>/retry \
  -H "Authorization: Bearer $ADMIN_API_KEY" -H "Content-Type: application/json" -d '{}'
```

## 三、构建与配置采集端

### 使用分发包（推荐）

`dist/` 下已提供：

```
dist/wzstudy-agent.exe                         采集端程序（Windows x64）
dist/wzstudy-agent-v0.1.0-windows-amd64.zip    完整分发包
dist/agent.env.example                         配置模板
dist/start-agent.ps1 / start-agent.cmd         启动脚本（默认 doctor 自检）
dist/skills/                                   随包技能
dist/AGENT_README.md                           使用说明
```

解压后编辑 `agent.env`，填写 `WZSTUDY_SERVER_URL`、`WZSTUDY_AGENT_KEY`、模型配置，运行 `start-agent.cmd`（自检）→ `start-agent.cmd run`（常驻）。

配置文件加载顺序：进程环境变量 > `AGENT_CONFIG` 指定的文件 > 当前目录 `agent.env`。

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

- 采集端密钥哈希存储，创建/轮换时明文只返回一次；停用立即失效。
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

- 管理后台的任务板块覆盖派发、列表、详情、取消与重试；采集端身份的创建与轮换仍通过管理 API 完成。
- 只支持文本/Markdown 条目入库，不支持媒体上传（v2 计划）。
- 没有搜索工具、浏览器渲染和定时任务（定时可由外部 cron 调 `once` 实现）。
- 采集端单并发，`capacity` 预留 1-4。
