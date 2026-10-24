# 专用 AI 来源配置

## 推荐：管理员页面创建

1. 打开本地系统 `/admin/`，用管理员邮箱密码登录。
2. 在「客户端管理」创建独立来源，建议 slug `ai-collector`、名称「AI 资料采集」，保持启用、`auto_publish=false`。
3. 来源密钥只显示一次，安全保存。不要用管理员密码/API key 作为 Skill 凭据。
4. 在私有 JSON 配置里保存 `base_url`、`source_id`、`source_key`，或使用 `WZSTUDY_SOURCE_ID/KEY` 环境变量。

配置结构（占位值，不能直接使用）：

```json
{"base_url":"http://127.0.0.1:8091","source_id":"<客户端 UUID>","source_key":"<一次性来源密钥>"}
```

不要把真实配置放入本 Skill 或版本库。配置文件须仅允许当前用户访问。环境变量和配置只二选一使用一整对 ID/密钥，不混配。

## 本项目的一次性初始化工具

项目管理工具 `scripts/setup_ai_push.py` 可以在用户明确授权时创建专用来源并写受保护配置；它不是日常 Skill 的提权入口。

```powershell
python scripts/setup_ai_push.py --admin-key-file .local/access-keys.json --config .local/ai-push-client.json
```

该管理命令只在显式指定时读取本机管理 API key，内部调用管理 API 创建专用来源，**不回显、不复制、不保存管理员密钥或密码**。只将新来源密钥写入独立配置，Windows 限制 ACL、POSIX 模式 0600；现有配置拒绝覆盖，同 slug 已存在也不会自动轮换密钥。没有授权时，AI 应让用户通过页面完成此步骤。

然后日常只运行：

```powershell
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json doctor
python skills/wzstudy-push/scripts/wzstudy_push.py --config .local/ai-push-client.json push --input .artifacts/collected-items.json
```

## 可移植性与安全

- Script 只依赖 Python 3.10+ 标准库；从任意当前目录运行时使用其绝对路径。
- 没有隐式读取应用管理员配置的行为。私有来源配置必须通过 `--config` 或 `WZSTUDY_CONFIG` 明确指定。
- 来源密钥绑定配置中的服务地址；不允许用 `--base-url` 将现有配置改发另一地址。
- 远程必须 HTTPS；本机 HTTP 仅接受 localhost/loopback。不能关闭 TLS 校验。
- 不自动读取代理环境或跟随 HTTP 重定向，避免来源凭据意外转发。
- 密钥遗失时由管理员明确轮换，然后更新私有配置；不要自动创建一堆新来源或删除旧来源。
