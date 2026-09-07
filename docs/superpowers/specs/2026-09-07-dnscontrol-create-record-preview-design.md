# DNSControl 创建解析记录预览设计

## 1. 目标与边界

本阶段在 DNS 解析管理页提供参考阿里云云解析 DNS 的“添加记录”操作。用户填写一条新记录后，AllinSSL 只创建 DNSControl Preview 任务并展示变化；不调用 `dnscontrol push`，不直接调用 AliDNS 写 API，也不修改云端 DNS。

业务 DNS 的唯一写执行器仍为固定版本 `dnscontrol v5.0.3`。AliDNS SDK 继续只用于凭据、Zone 和记录读取。ACME DNS-01 仍由既有 lego 路径处理。

本阶段不包含编辑、删除、确认令牌、Push、真实 Provider 写入、非默认线路、停用记录或 Provider 扩展元数据的创建。

## 2. 可创建条件

“添加记录”仅在以下条件同时满足时可用：

1. 当前已选择授权和 Zone，并成功读取完整 Snapshot。
2. Snapshot 为兼容状态；出现 `INACTIVE_RECORD`、`UNSUPPORTED_LINE`、`UNSUPPORTED_METADATA` 或任一其他只读原因时，整个 Zone 保持只读。
3. 该 Zone 已有成功的零差异纳管任务，且纳管时使用的授权与当前授权一致。
4. 本地 DNSControl 健康检查通过，版本精确为 `v5.0.3`。

未纳管 Zone 显示“请先完成纳管预览”；不兼容 Zone 显示只读原因；DNSControl 不可用时显示健康检查失败原因。前端禁用入口仅改善体验，后端必须重新检查全部前提。

## 3. 页面与表单

记录表格上方增加“添加记录”按钮。弹窗遵循阿里云常见的记录创建结构，包含：

| 字段 | 规则 |
| --- | --- |
| 主机记录 | Zone 相对名称；顶点使用 `@`；允许合法 `_service` 标签；通配符只允许最左标签；不得越出 Zone。 |
| 记录类型 | 仅 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`SRV`、`CAA`。 |
| 记录值 | 按记录类型验证并规范化；TXT/CAA 保持原始文本语义。 |
| TTL | 从当前 Snapshot 的 Provider 能力范围选择；本阶段有效交集为 `600` 至 `86400` 秒。 |
| 解析线路 | 固定显示“默认”，不可编辑。 |
| 状态 | 固定显示“启用”，不可编辑。 |

`MX` 额外要求优先级；`SRV` 额外要求优先级、权重和端口；`CAA` 额外要求标志位和标签。数值字段与现有 `dnsmodel` 的规范化规则一致：MX/SRV 数值为 `0` 至 `65535`，CAA 标志位为 `0` 至 `255`。

表单提交按钮命名为“创建预览”。提交成功只开始异步任务，不能在本地记录表中乐观添加一行；最终预览完成后仍保留当前只读快照，用户可手动刷新记录。

## 4. API 与任务模型

新增受保护表单接口：`POST /v1/dns/create_record_preview`。

请求仅接受以下字段：

- `credential_id`
- `zone`
- `base_snapshot_hash`
- `record` 的结构化字段
- `idempotency_key`
- `csrf_token`

接口必须拒绝重复字段、未知字段、JSON/DSL 片段、Provider 凭据、二进制路径、CLI 参数或自定义 `IGNORE` 规则。它沿用 DNS 专用会话、同源校验和 CSRF 校验。该接口与 `bind_zone` 使用同一 Zone 锁，避免纳管预览和记录预览并发。

任务新增 `kind=create_record_preview`。任务状态沿用 `queued`、`previewing`、`blocked`、`failed`，成功终态新增 `previewed`；所有终态都释放 Zone 锁。复用 `get_job`，但响应应包含脱敏的候选记录摘要、计划哈希、变化摘要和错误码。不得保存前端 DSL、云凭据或可执行文件路径。

同一会话下，`(actor_id, action=create_record_preview, idempotency_key)` 唯一：相同请求返回原任务，不同请求返回幂等冲突。

## 5. 后端处理流程

1. 校验 DNS 会话、同源、CSRF、表单白名单、授权、Zone、幂等键和记录字段。
2. 事务创建任务并取得按 Zone 唯一的锁；锁冲突返回 `ZONE_BUSY`。
3. 重新读取完整 Zone Snapshot，并验证 Zone、授权、纳管状态、兼容性及 `base_snapshot_hash`。任一不匹配转 `blocked`，错误码为 `DNS_REMOTE_DRIFT`、`DNS_NOT_ADOPTED` 或 `DNS_INCOMPATIBLE_SNAPSHOT`。
4. 将请求记录转换为 `dnsmodel.Record`，固定 `line=default`、`status=ENABLE`，并与完整 Snapshot 合并。禁止触及 `_acme-challenge`、其子名称和顶点 `NS`/`SOA`。
5. 复用 `dnsmodel.BuildSnapshot` 验证候选：拒绝重复记录、同名 CNAME 冲突、Null MX 混用、TTL 不支持、无效 SRV、无效 CAA 或其他不可表达记录。
6. 以完整候选 Snapshot 生成确定性 DNSControl DSL，保留现有固定保护规则：ACME 名称及顶点 `NS`/`SOA` 使用固定 `IGNORE`。
7. 分别确认 `check` 与 `preview` 都正常启动、未超时、退出码为零且生成完整报告；解析报告时只接受目标 Zone、`ALIDNS` Provider 与预期的新增语义。额外删除、保护记录变化、未知输出、TTL 自动调整、跨 Zone 项或不完整报告一律转 `blocked`。
8. 保存脱敏计划摘要和计划哈希，任务转 `previewed`。该状态不签发确认 token，不产生 Push 路径。

## 6. 错误展示与审计

前端将 `previewed` 展示为“预览已生成”，并显示“未修改 DNS、Push 尚未开放”。`blocked` 展示稳定错误码及安全中文说明；`failed` 仅表示预览基础设施或读取失败，不能被解释为“未发生 DNS 写入”之外的任何执行结论。

审计记录任务创建、开始预览、阻断、失败和预览完成。日志仅保存 Zone、记录类型、相对主机名、计划哈希、状态和脱敏错误码；不得记录 AccessKey、Secret、完整敏感 TXT 值、CSRF、会话绑定、幂等键或临时文件路径。

## 7. 测试与验收

后端测试覆盖：

1. 各支持类型的规范化和表单校验，以及 MX、SRV、CAA 的专有字段。
2. 保护名称、重复记录、CNAME 冲突、Null MX 冲突、TTL 越界和不兼容 Snapshot 的阻断。
3. 已纳管校验、Snapshot 漂移、同 Zone 锁、幂等命中/冲突、会话与 CSRF 拒绝。
4. DNSControl Preview 的预期新增差异、额外删除、未知报告和非零退出码的处理。
5. 任务状态转换、锁释放、审计写入与重启恢复。

前端测试覆盖入口禁用条件、动态字段、表单错误、请求序列化、任务轮询和预览结果展示。验证只使用 fixture、mock 或 BIND 本地测试；不得读取真实云凭据或写入真实 DNS。

## 8. 非目标与后续阶段

本阶段完成后仍不具备 DNS 写能力。后续若要开放 Push，必须单独设计并验收确认令牌、执行前重读/重新 Preview、严格计划校验、执行后核对、失败人工核对和显式全局写开关。编辑、删除、非默认线路、停用记录和扩展元数据同样作为独立设计项，不能通过 AliDNS SDK 绕开 DNSControl。

## 9. 阶段提交要求

本功能按可验证的开发阶段交付：每完成一个阶段，在运行与该阶段风险相称的测试、构建和静态检查后提交一次代码。提交信息必须为详细中文，说明本阶段的功能范围、DNSControl 不写入约束、已运行验证及未覆盖的边界。提交不得包含真实凭据、数据库、证书、日志、构建缓存或其他运行时产物；提交完成不代表授权推送、创建 PR 或发布。

## 10. 实施与验收记录

2026-09-07 已按本设计完成后端候选构建、任务持久化/锁、受保护 API、真实服务器中间件链及前端动态表单。HTTP 实现采用严格的扁平表单字段：公共字段为 `credential_id`、`zone`、`base_snapshot_hash`、`name`、`type`、`ttl`、`value`、`idempotency_key`、`csrf_token`；MX/SRV/CAA 仅追加各自专用字段。未知、重复、query 注入、跨类型字段、错误 content type 和超过 64 KiB 的请求均拒绝。

成功登录会轮换 DNS session identity、auth epoch、CSRF 和独立 Secure 绑定 Cookie，防止登录前固定会话；真实 `/v1/dns` 路由先安装请求 preflight，再安装 Session 鉴权。`bind_zone` 与 `create_record_preview` 均要求当前应用登录、同源 Origin 和 CSRF，显式 API token 不具备 DNS 授权能力。

前端仅在当前兼容、健康且同授权同 Snapshot 已纳管时启用“添加记录”；A/AAAA/CNAME/TXT 使用公共字段，MX/SRV/CAA 显示专用字段，线路和状态不可编辑。成功轮询到 `previewed` 后显示“预览已生成，未修改 DNS，Push 尚未开放”，并保持原 Snapshot 不变。

验收使用 Go 单元/集成测试、真实 Gin 注册链、Vitest、Vite 构建、静态检查和脱敏 fixture；未使用真实凭据、真实数据或 AliDNS 网络。生产路径没有 `Push` 方法、确认 token 或 Provider 写调用。未来开放写入仍需专用 AliDNS Zone 验收、执行前重读与重新 Preview、一次性确认、默认关闭写开关和执行后核对，不能由本阶段结果推定安全。
