# DNS 解析直连厂商集成设计

日期：2026-09-08。状态：已实施，待专用测试 Zone 人工验收。

## 目标

在 AllinSSL 内提供类似阿里云云解析 DNS 的记录管理页面。用户继续使用 AllinSSL 已保存的阿里云 AK/SK 授权，页面可读取 Zone 和记录，并直接完成业务记录的新增、编辑、删除、启用和停用。

本设计以 `upstream/1.1.3`、提交 `73cbcb8a213d959e772fb8ab3120abb9efa476c4` 为上游基线；当前开发分支为 `feature/dnscontrol-adapter`。上游同步前必须先执行：

```bash
git remote show upstream
git branch -r
git ls-remote --heads upstream
```

确认官方实际源码或发布分支后再决定同步来源；当前阶段唯一基线为 `upstream/1.1.3`。

## 决策

- 常规 DNS 管理页面直接调用厂商 API，不调用 DNSControl。
- 首期仅支持 AliDNS，并复用仓库已有 `github.com/go-acme/alidns-20150109/v4` 客户端，不引入新的 DNS SDK。
- 首期可操作 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`SRV`、`CAA`；支持 TTL、解析线路和启用/停用。
- `NS`、`SOA` 与 `_acme-challenge` 记录始终只读，不能在业务 DNS 页面删除、编辑或切换状态。
- ACME DNS-01 仍由既有 lego 调用链管理，与业务 DNS 页面隔离。
- 不做批量操作、DNSControl Preview/Push、任务状态机、确认令牌、审计表或预先抽象的单实现 Provider 接口。

DNSControl 可在未来作为独立、显式启用的 GitOps 功能评估；同一 Zone 不能由其与直连厂商 API 同时写入。

## 现有能力与迁移

当前 `backend/internal/dns` 已能使用 AliDNS 完整分页读取授权、Zone 和快照，并通过同一客户端封装完成单记录增改删和启停；`backend/app/api/dns.go` 与 `frontend/apps/allin-ssl/src/views/dns/` 已具备受保护的 DNS 会话、CSRF、筛选、排序和记录表单。

实施的第一步删除普通页面中的 DNSControl 健康检查、纳管 Preview、创建 Preview、任务轮询及相关路由，不保留双入口。页面保留授权选择、Zone 选择、完整读取、筛选和排序，并改为直接执行记录操作。

## 后端设计

`backend/internal/dns` 扩展现有 AliDNS 客户端封装，使其在同一后端边界内支持：

| 业务操作 | AliDNS 请求 |
| --- | --- |
| 新增 | `AddDomainRecord` |
| 编辑 | `UpdateDomainRecord` |
| 删除 | `DeleteDomainRecord` |
| 启用或停用 | `SetDomainRecordStatus` |

服务层按 `credential_id` 解析已保存的 AK/SK；浏览器永远不提交、读取或回显密钥。写操作使用当前完整快照验证 Zone、记录 ID 和保护规则，然后调用 AliDNS。成功后重新执行完整分页读取并返回新快照，前端不得自行乐观改写记录。

新增以下受 DNS 会话、同源和 CSRF 保护的表单接口：

| 路径 | 必填字段 | 行为 |
| --- | --- | --- |
| `/v1/dns/create_record` | `credential_id`、`zone`、记录字段、`csrf_token` | 新增记录并返回刷新后的快照 |
| `/v1/dns/update_record` | `credential_id`、`zone`、`record_id`、记录字段、`csrf_token` | 更新指定记录并返回刷新后的快照 |
| `/v1/dns/delete_record` | `credential_id`、`zone`、`record_id`、`csrf_token` | 删除指定记录并返回刷新后的快照 |
| `/v1/dns/set_record_status` | `credential_id`、`zone`、`record_id`、`status`、`csrf_token` | 启用或停用指定记录并返回刷新后的快照 |

所有请求使用现有 `application/x-www-form-urlencoded` 边界，严格拒绝未知字段、查询参数中的认证字段和跨站请求。`record_id` 必须在当前授权与 Zone 的完整快照中存在；客户端提供的名称、类型或线路不能替代该归属校验。

记录字段按类型验证：

- 所有可写类型必须有主机记录、记录值、`600` 至 `86400` 的整数 TTL 和有效线路。
- `MX` 额外要求优先级；`SRV` 额外要求优先级、权重和端口；`CAA` 额外要求标志位和标签。
- 编辑不可把记录转换成超出首期范围的类型；`NS`、`SOA`、`_acme-challenge` 始终拒绝。
- 写入前重新读取记录以避免过期记录 ID；AliDNS 返回失败时不改变本地页面状态。
- 网络超时或结果不明确时不自动重试，返回“请刷新记录确认实际状态”，避免重复创建或误删。

## 前端设计

页面沿用现有阿里云风格表格、完整读取、筛选和本地排序。移除“纳管预览”“DNSControl 状态”和 Preview 任务提示，新增如下操作：

- 顶部“添加记录”打开记录表单。
- 表格每行提供“编辑”“删除”“启用/停用”。删除必须通过二次确认。
- 表单按记录类型动态显示 MX、SRV、CAA 的专属字段，并允许输入 AliDNS 解析线路，默认值为 `default`。
- `NS`、`SOA`、`_acme-challenge` 行显示只读原因且不显示写操作；没有绕过入口。
- 操作成功后使用接口返回的完整快照替换页面数据；操作失败保留当前快照并显示错误。

## 多厂商演进

首期不创建只有 AliDNS 一个实现的通用接口。第二家厂商确定后，先比较已验证的读取、增改删和状态能力，再从 AliDNS 服务提取实际共同的最小操作模型；厂商不支持的字段在页面明确标记，不伪造统一能力。

## 测试与验收

- Go 单元测试：请求字段、七类记录、MX/SRV/CAA 专属字段、保护记录、跨 Zone/跨授权记录 ID、线路、启停、AliDNS 失败和结果不明。
- API 测试：DNS 登录会话、CSRF、同源、未知字段拒绝、表单限制和成功后完整快照返回。
- 前端测试：新增、编辑、删除确认、启停、动态字段、只读按钮、成功刷新和失败提示。
- 验证命令：相关 Go 测试、`go vet`、Vitest、Vite 构建和 `git diff --check`。
- 不使用真实生产 DNS 进行自动测试；完成后仅用专用测试 Zone 与最小权限 AK/SK 人工验证一次。

## 本阶段验证记录

- `go test ./backend/app/api ./backend/internal/dns ./backend/internal/dnsmodel ./backend/middleware ./backend/route ./backend/server -count=1`：通过。
- `go vet ./backend/app/api ./backend/internal/dns ./backend/internal/dnsmodel ./backend/middleware ./backend/route ./backend/server`：通过。
- `npm --prefix frontend/apps/allin-ssl test -- --run src/api/dns.spec.ts src/views/dns/useController.spec.ts`：通过。
- `npm --prefix frontend/apps/allin-ssl run build`：通过。
- `npm --prefix frontend/apps/allin-ssl run tsc` 在当前环境中因 Node 堆内存不足退出；已将 Vite 构建作为前端编译验证，未发现本阶段代码构建错误。
- `git diff --check`：通过；已确认旧 DNSControl 普通页面路由和文档已移除。

## 提交规则

每个可独立验证的阶段完成后，提交一个详细中文 commit。不得修改或推送 `upstream`；不提交现有工作区中的用户生成物、构建产物或测试凭据。
