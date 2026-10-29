# 查询客户端扫码接入（v1）

## 管理端操作

进入 **数据查询 → 查询客户端**：

1. 新建或显式轮换成功后，在一次性 Key 弹窗中填写“扫码设备可访问的服务地址”。
2. 确认该地址属于当前服务、扫码设备可达，并知悉二维码包含完整 Key。
3. 点击“生成接入二维码”，在支持本协议的客户端内扫描。可按需下载 PNG。
4. 已有客户端可点击“扫码接入”，粘贴已保存的 Key 后生成；这不会修改或轮换 Key。若已遗失，只能明确确认重新生成，旧 Key 会立即失效。

二维码完全在浏览器本地生成，编码器随应用内嵌，不调用第三方二维码服务。生成前会在**当前服务**验证 Key 真实所属的查询客户端。管理员、全局只读、录入端及其他客户端 Key 都不能代替选中身份。

生成或下载不会把明文 Key、载荷或二维码存入服务端记录、URL、日志或浏览器持久存储。关闭/替换弹窗、修改地址或 Key 时清除旧二维码，并撤销临时图片对象 URL。用户主动下载或截图的副本不受弹窗清理控制。

**二维码就是凭据，不是公开图片，也不是一次性/短期配对码。** 它没有独立失效时间，可重复扫描；Key 轮换、客户端停用或撤销全部查询权限后不能继续接入。每台设备需要独立撤销时，应分别创建客户端。

## 地址与网络

`base_url` 是客户端能访问的 **HTTP(S) 服务根地址**，不含 `/admin`、`/api/v1`、账号密码、查询参数或片段。v1 不支持代理路径前缀。HTTPS 推荐用于正式接入。

- `127.0.0.1`、`localhost`、`[::1]` 指向扫码设备自身，通常不能让手机访问管理员电脑。
- 手机通常需要同一局域网地址或正确配置的 HTTPS 域名。
- **修改二维码地址不等于修改服务器监听。** 当前服务若只绑定回环地址，仍须显式配置可达的监听地址、端口及防火墙；不要把 `0.0.0.0` 或 `[::]` 监听通配地址直接放进二维码。
- 本地启动脚本默认仅监听 `127.0.0.1:8091`。需要手机访问时，可在可信网络显式使用 `./start-local.ps1 -Lan`，或 `./scripts/restart-local.ps1 -Lan` 重启已有服务；启动提示会列出候选 IPv4 地址。二维码填写手机实际能访问的 `http://局域网IP:端口`，不要填写 `0.0.0.0` 或 `127.0.0.1`。局域网监听会扩大访问范围，系统使用双栈套接字时也可能允许 IPv6 访问；脚本和二维码功能都不会修改防火墙，也不保证手机能连通。HTTP 会明文传输凭据，正式使用请配置 HTTPS。
- HTTP 不加密，只用于明确授权的可信本机/局域网调试。SDK 默认拒绝 HTTP，需显式 `allow_insecure_http=True`。
- 管理页面不会向填写的其他地址探测或发送 Key。管理员负责确认它指向同一服务；扫码端必须按下文对**实际目标地址**再次校验。

## 二维码载荷

UTF-8 JSON 直接作为二维码内容，带 UTF-8 ECI 26、四模块白色边界和黑白图像，不将 Key 拼入 HTTP URL 或自定义 scheme 的查询参数。最大 **1200 UTF-8 字节**。

```json
{
  "type": "wzstudy.query_client",
  "version": 1,
  "base_url": "https://study.example.com",
  "client_id": "11111111-2222-4333-8444-555555555555",
  "name": "手机学习端",
  "access_key": "wzck_example_placeholder_not_a_real_key_1234567890"
}
```

以上仅是无效示例，不能用于接入。客户端应严格要求六个字段，不猜测其他版本/格式；拒绝重复 JSON 字段、非标准数字、错误字段类型、控制字符、非法 URL、未知字段及超长输入。

- `type` 固定为 `wzstudy.query_client`，`version` 必须是整数 1，不接受布尔值或字符串。
- `base_url` 规范化为服务 origin；同一服务的不同 origin 分别管理，不能仅按名称或 ID 跨域合并。
- `client_id` 是 UUID，用于和服务端权威身份比较，不是单独的授权凭证。
- `name` 仅用于展示，最多 200 UTF-8 字节，不含控制字符。校验成功后可使用服务端当前名称。
- `access_key` 为 32–256 个 ASCII 字母/数字/下划线/连字符。不能仅根据 `wzck_` 前缀判断权限：显式迁移的旧查询 Key 可保留旧前缀。
- 载荷不包含权限声明；实际权限只信服务端。扫描不能提升或补齐 read/sync/feedback；反馈能力需管理员显式授予。

机器 schema 见 OpenAPI 的 `ClientConnectionConfig`。

## 扫码客户端导入流程

1. 客户端扫码器获取原始文本，按 UTF-8 解析并校验上述协议。
2. 显示目标服务地址和客户端名称，提醒用户确认可信来源；不凭二维码名称判断服务可信。
3. 用户确认目标后，通过 HTTPS 向固定端点发送一次只读验证：

```http
GET /api/v1/client/connection
Authorization: Bearer <二维码里的查询 Key>
Accept: application/json
```

成功响应：

```json
{
  "data": {
    "type": "wzstudy.query_client",
    "version": 1,
    "client_id": "11111111-2222-4333-8444-555555555555",
    "name": "手机学习端",
    "permissions": ["read", "sync"]
  }
}
```

该端点只接受独立查询客户端 Key，要求启用且至少有 read 或 sync；**不回退使用管理员 Cookie、管理员 Key、旧全局只读 Key 或 Source Key**。响应不包含明文 Key、哈希、管理员备注或其他身份记录，且禁止缓存。只读/仅同步均可接入，空权限或停用为 403，无效或已轮换旧 Key 为 401。

4. 核对 type/version、返回 client_id 与载荷相同、权限只含 read/sync/feedback，且至少有 read 或 sync。404 表示服务不支持本协议，不应改用管理接口、其他身份或静默轮换。
5. 校验成功后，以 **规范化 base_url + client_id** 添加连接，并安全保存 Key。实际 App 应使用操作系统安全凭据存储，不使用日志、公开文件、URL 或浏览器 localStorage。
6. 同一连接相同 Key 可幂等导入；不同 Key 必须用户明确确认替换且验证成功后再更新。不同 origin 不自动合并。缓存、来源筛选和游标继续遵守 `subscribe.py` 的隔离规则。

所有带 Key 的验证/查询请求都应拒绝跨地址重定向，不转发认证头；保持正常 TLS 校验，不用“关闭证书验证”代替正确部署。

## 可运行的解析/导入参考

本仓库没有实际学习 App 的摄像头或连接设置界面，因此交付的是**服务端二维码 + 接入协议 + 可复用解析/注册模块**，不能宣称已替外部 App 实现相机扫码和持久保存。

`examples/client_connection.py` 只依赖标准库，提供：

- `parse_connection(text)`：离线校验并返回配置，Key 不出现在默认 repr。
- `verify_connection(config)`：拒绝重定向，校验实际服务的查询客户端身份。
- `ConnectionRegistry.add_from_qr(text)`：真实验证后直接添加内存连接，无需手输 URL/Key；不同 Key 替换需明确参数。

```python
from client_connection import ConnectionRegistry, parse_connection

connections = ConnectionRegistry()
# decoded_text 来自实际客户端的扫码库，不是服务器返回的身份信息。
preview = parse_connection(decoded_text).summary()  # 安全摘要，不含 Key
# 此处由 App 显示 preview 并让用户确认目标。
result = connections.add_from_qr(decoded_text)      # HTTPS，验证后自动加入
config = connections.get(result["base_url"], result["client_id"])
# 后续 API 使用 config.headers()；请勿打印该请求头。
# 实际 App 在此接入其安全凭据存储；示例不自动写 Key 到磁盘。
```

CLI 可从扫码器输出的文本文件或标准输入读取。文件若包含真实 Key，必须由用户安全管理，不进入版本库：

```powershell
# 仅校验格式并打印不含 Key 的目标摘要，不联网、不声称已连接。
python examples/client_connection.py --input <私有扫码结果.json>
# 用户确认目标后，校验并在本次进程的内存注册表中添加。
python examples/client_connection.py --input <私有扫码结果.json> --verify
# 仅可信本机/局域网开发需要时显式允许 HTTP。
python examples/client_connection.py --input <私有扫码结果.json> --verify --allow-insecure-http
```

普通系统相机扫到 JSON 可能仅显示文本；“扫码直接添加”需要实际客户端接入本协议。此实现没有修改其他项目或手机 App。

## 验证

```powershell
python -m unittest discover -s examples -v
go test ./internal/httpapi -run 'TestQueryClientConnection|TestQRCodeLocalStaticBundle'
go build -o .artifacts/wzstudy.exe ./cmd/api
python scripts/verify_client_qr.py
```

扫码综合测试使用临时服务、合成资料和随机凭据，独立 OpenCV 图像解码后调用实际校验端点并导入注册表，还验证真实查询、重复导入、错误身份、撤权、轮换、PNG 下载、清理及手机尺寸布局。不是对所有手机相机/扫码 SDK 的实机认证；OpenCV 对 ECI 有兼容警告，但测试要求恢复的中文/emoji 和 Key 字节完全正确。

报告与合成测试截图在 `.artifacts/client-qr-verification.json`、`client-qr-desktop.png`、`client-qr-mobile.png`。测试截图中的二维码仅属于已销毁的隔离服务，不是真实接入配置。


## 本地编码器来源

- 库：Nayuki QR-Code-generator v1.8.0（ES6 编译版，MIT）。
- 发布源：`https://github.com/nayuki/QR-Code-generator/releases/tag/v1.8.0`。
- 随包文件：`internal/adminui/qrcodegen.js`，保留完整上游许可和版权声明，未修改库代码。
- SHA-256：`6a1116192ed1dd67fa1bf31e77f5817103d71c23bbac24c382e698b7668bdd01`。
- 构建时内嵌、本地提供静态脚本；运行时不请求上游网站或二维码生成 API。


## 客户端反馈与去重

客户端可显式申请 feedback 权限，上报推荐/曝光、已读、喜欢、学习记录和收藏，按事件 ID 幂等保存。`POST /api/v1/client/feedback` 仅写本身份；`GET /api/v1/items?exclude=recommended,read` 或 `unseen=true` 可过滤旧候选，支持时间窗口和本页状态。管理员只读查看反馈或按身份模拟查询，不替用户上报。默认 read/sync 及所有旧 Key 不自动扩权。详见 `docs/CLIENT_FEEDBACK.md`（文档目录内为 `CLIENT_FEEDBACK.md`），示例为 `examples/feedback_client.py`。
