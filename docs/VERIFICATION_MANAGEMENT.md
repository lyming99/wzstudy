# v0.2.0 验证记录

## 已通过

- `go test -count=1 -json wzstudy/...`：45 个通过的测试结果（36 个顶层测试）。9 个需要专用 PostgreSQL 测试库的条件用例跳过，不计为通过。
- `go vet wzstudy/...`：通过。
- 在本机已安装 GCC 的前提下，仅为测试进程设置 `CGO_ENABLED=1` 执行 `go test -race wzstudy/...`：通过；未改变系统/用户环境设置。
- Windows/amd64 可执行文件构建：通过，发行文件保持 `CGO_ENABLED=0`，前端资源与接口文档内嵌。
- 23 组真实服务浏览器验证：通过，使用隔离文件存储和随机测试密钥，而非前端假数据。
- Windows 便携包解压启动验证：通过，校验全部清单哈希、无源码启动、自动随机密钥、私有凭据文件 ACL、真实文件存储与内嵌文档；测试结束关闭其自有进程树。

## 浏览器覆盖范围

- 登录、错误密钥、HttpOnly 会话、安全退出、反向代理非 JSON 401 的重新登录处理。
- 初始空工作空间，客户端登记、一次性密钥、密钥轮换与旧密钥失效。
- 文章保存、草稿/审核/发布、独立公开快照、XSS 安全文本预览。
- 实际 WAV 音频与 WebM 视频上传、浏览器解码和播放。
- 多图片落盘、图片解码与轮播切换。
- 服务端搜索、筛选、分页、批量操作、CSV 导出。
- 摘要编辑保留原作者、原标签、来源和未在页面缓存中的自定义分类。
- 合集创建、编辑和按分类归集。
- 创建计划、暂停/恢复、手动真实投递、后台到期投递与不重复执行。
- 来源专属收件箱及接收范围认证；不把「已写入收件箱」伪装成客户端已读。
- 来源启停、RSS 错误反馈、对内网地址的抓取保护。
- 显示名称/紧凑模式保存、刷新与进程重启恢复。
- 7 个页面、390/768/1024/1280/1440/1600px 屏宽、手机导航、键盘焦点约束。
- 延迟返回的旧详情不会在切换页面后重开。
- 没有资料 `localStorage` 缓存，没有外部网页请求，没有捕获到未处理 JavaScript 异常。

## 明确未验证 / 不包含

- 本轮使用隔离文件工作区；Go 验证工具默认不连接 PostgreSQL。PostgreSQL 实库、迁移实际执行、PG 并发存储以及容器构建/运行均未验证；Compose 只完成无插值语法校验。
- 新增 `TestPostgresManagementWorkspace` 已就绪；配置专用测试数据库后会验证迁移、资产注册、筛选、设置、合集、公开快照和并发调度。原有 PostgreSQL 契约用例也会一起执行。
- 未执行公网部署，没有发布到版本服务；便携包未签名，不是安装器。
- 没有终端 App、APNs/FCM、已读回执、媒体转码、对象存储或多用户 RBAC。
- 此验证不是渗透测试、负载测试或生产可用性认证。

## 产物

- `.artifacts/go-tests.jsonl`、`.artifacts/go-verification.json`。
- `.artifacts/live-verification.json`、`.artifacts/package-verification.json`。
- `.artifacts/management-desktop.png`、`.artifacts/management-mobile.png`。
- `dist/release.json`、`dist/SHA256SUMS.txt`、Windows 可执行文件与 ZIP 便携包。

截图使用真正入库的隔离测试资料，首次正常启动仍为空工作空间。测试凭据不写入发行包，也不在验证输出中展示。

复现方式见同目录 `RUNNING.md`、`scripts/verify_live.py`、`scripts/verify_package.py`。PostgreSQL 测试只应指向专用测试数据库，夹具会创建和清理独立测试 schema。

## 目录整理后复验

验证入口归入 `scripts`，输出统一位于根 `.artifacts` 与 `dist`。结构/启动器诊断覆盖根入口、旧入口、路径含空格、旧工作区识别、歧义拒绝、缺少密钥的数据保护和 Docker 配置静态检查。实时结论见 `.artifacts/structure-verification.json`、`go-verification.json`、`live-verification.json` 和 `package-verification.json`；历史 API 基线 `docs/VERIFICATION.md` 未覆盖。
