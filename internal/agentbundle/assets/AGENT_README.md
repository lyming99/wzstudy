# WzStudy 采集端（Windows x64）

独立采集进程：从 WzStudy 录入系统领取收集任务，用 agent loop + function calling 抓取、整理并推送资料。

## 目录内容

| 文件 | 说明 |
|---|---|
| `wzstudy-agent.exe` | 采集端程序 |
| `agent.env.example` | 配置模板 |
| `start-agent.ps1` / `start-agent.cmd` | 启动脚本（默认执行 doctor 自检） |
| `skills/` | 随包分发的技能，例如 `web-article` |

## 快速开始

1. 把整个目录解压到本地（不要只复制 exe，`agent.env` 与 `skills` 需要同目录）。
2. 首次运行 `start-agent.cmd`（或 `start-agent.ps1`），会自动把模板复制为 `agent.env`。
3. 用记事本编辑 `agent.env`：

```ini
WZSTUDY_SERVER_URL=http://127.0.0.1:8091
WZSTUDY_AGENT_KEY=wzag_你的采集端密钥
AGENT_MODEL_BASE_URL=https://api.deepseek.com/v1
AGENT_MODEL_API_KEY=你的模型密钥
AGENT_MODEL=deepseek-chat
```

4. 再次运行 `start-agent.cmd`（默认 `doctor`）：校验配置、录入系统连通性与采集端权限，不写入数据。
5. 自检通过后运行：

```powershell
.\start-agent.ps1 run        # 常驻，循环领取任务
.\start-agent.ps1 once       # 只执行一个任务后退出
```

也可以直接命令行运行（环境变量优先于 `agent.env`）：

```powershell
.\wzstudy-agent.exe doctor
.\wzstudy-agent.exe run
```

## 采集端密钥

采集端身份由录入系统管理端创建，密钥只在创建时显示一次（`wzag_` 开头）。如果还没有密钥，请先在录入系统的管理后台/管理 API 创建一个采集端，再把密钥填入 `agent.env`。

## 退出与排查

- `Ctrl+C` 停止常驻进程；正在执行的任务会归还租约，下次重新领取。
- 自检失败时查看终端输出：401 表示密钥错误或已轮换；404 表示录入系统未启用采集模块；连接失败检查 `WZSTUDY_SERVER_URL`。
- 日志为 JSON，字段 `error` 给出原因；不会记录密钥。

## 安全

- 采集端只能写入任务绑定的来源，不能查询资料库。
- 默认禁止抓取内网/回环地址；仅在本地测试时可用 `AGENT_ALLOW_PRIVATE_NETWORK=true`。
- 模型密钥只保存在本机 `agent.env`，请勿提交到版本库或发给他人。
