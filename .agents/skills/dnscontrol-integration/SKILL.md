---
name: dnscontrol-integration
description: "设计、实现或评审 AllinSSL 的 DNSControl 业务 DNS 集成，涵盖记录读取、保护、预览确认和执行恢复；仅用于此集成，不接管 ACME DNS-01。"
---

# DNSControl 集成

执行前读取仓库 docs/development/dnscontrol-integration-design.md 和 dnscontrol-integration-design-review.md；分析问题再查 dnscontrol-integration-analysis.md。这些文档是约束和验证记录，不代表所有功能已实现。

保持业务 DNS 与 lego ACME DNS-01 分离。DNSControl 为业务写执行器，AliDNS API 客户端只读；不通过 SDK 偷补写入绕过执行契约。

使用完整兼容 Zone 快照和保护规则生成候选，不能只生成本次编辑记录。检查停用记录、解析线路、未知类型/元数据；不能无损表示的 Zone 保持只读。保护 ACME 名称及顶点 NS/SOA，并区分 BIND fixture 与真实 AliDNS 行为。

check/preview/push 分别验证进程事实与报告；非零退出且 corrections=0 仍是失败，文本行数不是实际变更数。未知计划内容、额外变化、快照/身份漂移和过期确认都不能放行 Apply。

AliDNS 修改含 TTL-only 可能先删后建并产生新 Record ID。区分语义与记录身份，结果不明进入核对，不自动回滚或重试 push。本地锁和计划 hash 不提供云端原子写入保证。

鉴权、凭据摘要、会话代次、CSRF、body 限额顺序、持久化锁和幂等按设计实现；不要复用返回完整 config 的授权列表给 DNS 页面。密钥不得进入日志、报告、源码或测试 fixture。

当前后续增量优先只读 Reader/公共模型，再进行离线生成器与解析器测试。真实 AliDNS 查询、写入和生产操作必须属于当前用户明确授权的测试环境范围；设计或安装 Skills 的请求不构成这类授权。

固定并校验 DNSControl v5.0.3；版本升级需独立评估与 fixture 回归。测试结论明确离线/BIND、AliDNS mock、专用 AliDNS 和生产之间的界限。写开关保持关闭，直到相关验收条件满足且用户授权开启。
