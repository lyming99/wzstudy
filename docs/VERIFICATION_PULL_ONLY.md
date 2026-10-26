# 移除数据推送模块：实现与验证记录

> 历史阶段记录：当前两链路身份与前端已继续改造，以 `VERIFICATION_TWO_CHAINS.md`、`CLIENT_ACCESS.md` 和最新 OpenAPI 为准。以下保留当时的测试证据，不代表当前全部实现。

## 当前架构

服务端负责资料入库、审核发布、来源与媒体管理，以及已发布资料查询；学习客户端主动查询候选资料、拉取公开内容变更，自行维护学习记录并计算推荐。本次未新增服务端个性化推荐算法。

### 已移除

- 管理端推送导航、推送日程、计划页面、资料详情与批量操作中的推送按钮、计划表单和投递通知。
- `POST /api/v1/admin/plans`、`POST /api/v1/admin/plans/{id}/action`。
- `GET /api/v1/clients/{id}/inbox` 与 `GET /api/v1/stream`。
- 推送模型、计划执行、投递与收件箱领域逻辑，以及主进程的定时推送协程。
- `inbox` 可配置权限、SSE 连接限制配置及其启动/Docker 配置。

上述旧业务路由返回 404；工作空间响应和 OpenAPI 不再包含计划/投递模型。

### 继续保留

- 手动录入、RSS/Atom 采集、AI Skill JSON/文件入库、去重及审核流程。`wzstudy-push` 中的 `push` 仍表示资料入库，不表示向学习客户端分发。
- `GET /api/v1/items` 的类型、分类、标签、关键词、来源筛选和分页；资料详情、媒体及公开来源查询。
- `GET /api/v1/changes` 的客户端主动拉取、全局提交顺序、过滤空页游标推进及删除墓碑。
- 独立客户端 Key、阅读/拉取/入库/上传权限、启停、轮换及审核快照隔离。
- 示例 `examples/subscribe.py` 的旧文件名与 SQLite 缓存：实现已改成 HTTP 轮询，资料和游标在同一事务中提交。

## 旧数据兼容与权限边界

未修改已应用的数据库迁移，未执行真实工作空间数据清理。通用记录中的历史计划和投递可以保留，但没有业务入口再读取或执行它们；资料、附件、Key 和历史变更游标不重置。

旧的显式权限数组中的 `inbox` 被忽略，并从来源管理/工作空间响应中滤除；仅含该值的旧策略成为有效空权限，不转换成旧默认策略。省略权限的旧来源继续只具有入库/上传能力，不自动获得阅读或增量拉取。新建和更新策略拒绝 `inbox`。

## 已执行验证

所有服务与浏览器验证使用临时隔离工作区、随机测试 Key 和合成资料；没有读取真实账号密码、来源 Key 或用户工作区正文，没有对真实资料执行入库/删除。

| 验证 | 实际结果 |
| --- | --- |
| `python scripts/verify_go.py` | 86 个用例通过，53 个顶层测试通过；10 个 PostgreSQL 相关测试按配置跳过 |
| `go vet ./...`、`go build ./...` | 通过 |
| `python -m unittest discover -s examples -v` | 6 个离线缓存用例通过：增删、空页推进、无效窗口、乱序回滚、资料/游标原子性及服务器/来源缓存隔离 |
| `python scripts/verify_client_access.py` | 8 组通过：独立 Key、读写权限隔离、旧接口移除、撤权/启停、轮换、持久恢复与桌面/手机布局 |
| `python scripts/verify_live.py` | 23 组通过：真实上传播放、审核隔离、资料管理、旧推送接口 404、客户端查询/拉取、重启持久化与响应式界面；无未捕获 JS 异常或外站请求 |
| `python scripts/verify_intake.py` | 10 组通过：三个录入入口、手动文件、RSS 配置及最小权限 AI Skill 真实入库与去重 |
| `python scripts/verify_push_skill.py` | 8 组隔离接口验证及 12 个 Skill 单测通过 |
| `python scripts/verify_structure.py` | 15 组通过：启动脚本语法、隔离路径与保护、Docker 静态配置 |
| OpenAPI 业务路由一致性及 schema 引用 | 通过；已删除路由与模型没有残余引用 |

新增回归测试位于 `internal/httpapi/pull_only_test.go`、`internal/study/pull_only_test.go` 和 `examples/test_subscribe.py`。其中覆盖旧到期计划/投递记录重启后保持惰性、旧权限不扩权、查询不改写文件、未审核内容不泄漏及下架候选可被拉取删除。

机器报告及桌面/手机截图位于 `.artifacts/`，例如 `go-verification.json`、`live-verification.json`、`management-desktop.png` 和 `management-mobile.png`。

## 未执行与启用方式

- 未执行 PostgreSQL 实库、容器运行或多实例实测；Docker 仅做静态配置检查。
- 未重启已有用户服务、未覆盖 `dist` 的既有发行包、未发布新版本。本轮构建验证使用 `.artifacts/wzstudy.exe`。
- 本地正在运行的旧进程需要从更新后的源码重新构建并重启才会生效。可使用 `scripts/restart-local.ps1`，不要加 `-SkipBuild`（该开关会使用未更新的 `dist` 便携版）；自定义端口、资料目录及工作区需传入原有相同参数。
