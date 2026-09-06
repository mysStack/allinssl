# DNSControl 集成设计评审记录

日期：2026-09-06。对象：[第二阶段设计](dnscontrol-integration-design.md)，修订 R1。

## 结论

完成源码与本地离线评审，已直接修正设计缺口。可进入只读能力和离线组件实施；尚不具备开放真实 AliDNS 写入的验收证据。没有修改功能源码、运行真实 AliDNS 查询或执行 push，也未提交或推送 Git。

AllinSSL 基线为 `73cbcb8a213d959e772fb8ab3120abb9efa476c4`。本地 DNSControl 源码为精确 tag `v5.0.3`、提交 `5196387f35f783af46b2c28bd37223a715ce583b`。其工作区已有第一阶段修改的 `commands/test_data/simple.com.zone`，本轮未使用或修改该文件；下面引用的源码文件无本地改动。

## 发现与处理

| 编号 | 级别 | 证据与影响 | R1 处理 |
| --- | --- | --- | --- |
| R01 | P1 | AliDNS `api.go:updateRecordset` 先 delete 再 create，`analyze.go:findTTLChanges` 也生成 CHANGE；TTL 修改可能短暂删除记录，创建失败则停留在部分完成状态 | 明确非原子更新、UI 风险字段、失败核对；语义 hash 与 Provider ID 分离，成功重读接受新 ID |
| R02 | P1 | 实际 BIND preview 退出码 1，却仍生成 corrections=0 的 report | check/preview 各自先验证进程成功，再判断报告；零数字不能放行纳管 |
| R03 | P2 | BIND 自动补 SOA 与 IGNORE SOA 冲突；一个业务修改的报告可有 7 条文本行 | 区分 Provider fixture；保留失败样例，已知说明与实际变更分开解析，不放宽未知内容处理 |
| R04 | P1 | `login.go:SignOut` 只删除 login；重登未轮换 DNS 上下文 | Sign/SignOut 显式轮换/撤销 auth_epoch、CSRF、会话绑定；跨登录代次不能复用确认 |
| R05 | P2 | `src/api/index.ts:useApi` 在开发模式加 api_token；全局 `checkApiKey` 在路由组前 c.Bind | DNS 独立 session 请求封装；限额、Content-Type 和 token 拒绝前移至全局解析之前 |
| R06 | P2 | 原 bind_zone 未定义锁/异步过程，可与 Apply 争用；待确认锁未规定后台过期执行 | kind=adopt 复用 Zone 锁及 job，15 秒后台 CAS 回收待确认任务 |
| R07 | P2 | Record 类型放 service 包而 CLI adapter 依赖它可能形成 import cycle；请求中修改 umask 会影响整个进程 | 公共 dnsmodel 与启动层注入；显式文件权限，禁止请求内修改全局 umask |

级别表示未修正文档时对后续实现的影响，不表示已存在或利用了生产系统漏洞。R01 的物理行为和 CLI 最后检查到 push 的并发窗口仍是能力边界，不能靠文档修正消除。

## 离线验证

使用已安装 `/home/bruce/.local/bin/dnscontrol-5.0.3`，隔离目录 `/tmp/dnscontrol-design-review-CdNhcu`。creds 只含本地 BIND 目录配置，没有真实密钥；Zone 名为 `review.example`，地址使用文档测试网段。所有执行均为 check 或 preview。

初始库存：顶点 SOA/NS，ns1、www、mail 三条 A，以及 `_acme-challenge`、`_acme-challenge.www`、`_acme-challenge.a.b` 的 TXT 和 `_acme-challenge.delegated` 的 CNAME。

| 场景 | 命令结果 | report / 判定 |
| --- | --- | --- |
| 原样使用设计 IGNORE 规则做 check | 退出 0，No errors | 仅证明 DSL 语法通过 |
| 相同配置用 BIND preview | 退出 1，自动 SOA 与 IGNORE 冲突 | JSON 仍有 bind corrections=0；必须判为失败 |
| BIND 专用配置保留固定 SOA 参数，只 IGNORE 顶点 NS 与 ACME 模式 | 退出 0 | bind corrections=0，零业务变更 |
| 同配置只将 www 从 192.0.2.10 改为 192.0.2.11 | 退出 0，终端 1 correction | JSON corrections=7，其中 6 行为保护说明/清单，1 行 MODIFY；没有额外业务变化 |

关键成功预览动作：

```text
± MODIFY www.review.example A (192.0.2.10 ttl=600) -> (192.0.2.11 ttl=600)
```

报告中的保护清单包含顶点 NS、根 ACME TXT、多级 ACME TXT 与委派 CNAME。BIND 的 SOA 参数通过 default_soa 与旧文件显式匹配，未使用 DISABLE_IGNORE_SAFETY_CHECK；这是 BIND 测试适配，不是把 AliDNS 保护规则改成忽略更少记录。

成功样例配置的关键部分（BIND credentials 中 directory 指向隔离 zones 目录）：

```javascript
var reg = NewRegistrar("none");
var dns = NewDnsProvider("bind", "BIND", {default_soa: {
  master: "ns1.review.example.", mbox: "hostmaster.review.example.",
  serial: 2026090601, refresh: 3600, retry: 600,
  expire: 604800, minttl: 600, ttl: 600
}});
D("review.example", reg, DnsProvider(dns), DefaultTTL(600),
  IGNORE("_acme-challenge", "*"),
  IGNORE("_acme-challenge.**", "*"),
  IGNORE("@", "NS"),
  A("ns1", "192.0.2.53"), A("www", "192.0.2.11"), A("mail", "192.0.2.25")
);
```

执行参数为 `--no-colors preview --config <config> --creds creds.json --domains review.example --cmode none --no-populate --report <new-report>`。结果分别保存在隔离目录的 `failed-baseline-report.json`、`bind-baseline-report.json`、`bind-update-report.json`，没有覆盖第一阶段 fixture。临时文件只作为本轮调试留存；正式测试必须在仓库内固化独立 fixture。

## 验证边界与后续实施门槛

- 以上证明共享 IGNORE glob 在 BIND 上保护列出的 ACME 名称，也复现了失败报告与说明行计数问题；不证明 AliDNS 写入、顶点处理或停用/线路行为已通过真实测试。
- 本轮验证不包含 push、故障注入、执行器、API、UI、SQLite 或真实 DNS 缓存传播。这些内容目前仍是设计要求。
- 下一实施增量为公共模型、只读 Reader、凭据摘要和纳管兼容性检查，随后才做离线生成器/report fixtures。新增能力保持 DNS 写开关关闭。
- 开放写入仍需真实专用 Zone、逐类型/线路/停用验收、恢复演练，并接受单一业务写入方及先删后建限制；未满足时保持只读。
