# Slack 通知集成

## 范围

本项目通过 Slack Incoming Webhook 发送证书、工作流和监控通知。通知渠道类型固定为 `slack`，不需要 OAuth、Bot Token 或额外数据库迁移；渠道配置继续保存在现有 `report` 表的 `config` JSON 中。

## 配置

1. 在 Slack App 设置中启用 Incoming Webhooks。
2. 为目标频道创建 Webhook，复制其 URL。
3. 在 AllinSSL 的“设置 / 告警通知”中添加 Slack 渠道，填写名称和 Webhook 地址。
4. 保存后使用“测试”按钮验证发送，再在工作流或监控中选择该渠道。

Webhook 地址必须是 HTTPS 的 Slack 官方地址，格式为：

```text
https://hooks.slack.com/services/<workspace>/<app>/<secret>
```

渠道配置保存形式如下；`enabled` 使用现有通知渠道约定的字符串值：

```json
{"enabled":"1","webhook":"https://hooks.slack.com/services/<workspace>/<app>/<secret>"}
```

后端拒绝非 HTTPS、非 `hooks.slack.com` 或路径格式不正确的地址；发送时不跟随重定向，避免将 Webhook 密钥提交给其他主机。

## 消息格式

请求为 `POST`，使用 `Content-Type: application/json`，载荷为：

```json
{"text":"<subject> : <body>"}
```

仅 HTTP `2xx` 响应视为发送成功。测试通知和业务通知共用同一发送实现，失败错误不会回显 Webhook 地址或其中的密钥。

## 安全与运维

- Webhook URL 是敏感凭据；不要写入 Git、日志、工单截图或前端静态配置。
- Slack 撤销或更换 Webhook 后，直接在 AllinSSL 编辑渠道并更新地址。
- 本集成不访问真实 Slack 工作区做自动化测试；后端测试使用本地 HTTP transport 验证请求方法、消息载荷、状态码处理和凭据脱敏。
- Slack 官方 Webhook 配置与消息格式以 [Slack Incoming Webhooks 文档](https://api.slack.com/messaging/webhooks) 为准。
