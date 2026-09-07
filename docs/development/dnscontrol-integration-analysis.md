# AllinSSL + DNSControl 第一阶段分析

## 结论

本项目应将 DNSControl 作为业务 DNS 记录的唯一执行器；AllinSSL 只提供凭据复用、结构化变更请求、预览确认、审计和 UI。现有 ACME DNS-01 流程继续直接使用 AllinSSL 的 lego Provider，DNSControl 不参与 `_acme-challenge` 的临时 TXT 记录。

本阶段只完成基线修正与分析，未开始 DNSControl 功能编码，也没有访问或修改任何 AliDNS/生产 DNS 资源。

## 已确认的开发基线

| 项目 | 值 |
| --- | --- |
| 上游 remote | `upstream` → `https://github.com/allinssl/allinssl.git` |
| 上游实际源码分支 | `upstream/1.1.3` |
| 上游提交 | `73cbcb8a213d959e772fb8ab3120abb9efa476c4` |
| 本地 `main` | `73cbcb8a213d959e772fb8ab3120abb9efa476c4` |
| 本地 `custom` | `73cbcb8a213d959e772fb8ab3120abb9efa476c4` |
| 当前功能分支 | `feature/dnscontrol-adapter` |
| 当前功能分支提交 | `73cbcb8a213d959e772fb8ab3120abb9efa476c4` |

本地 remote-tracking ref `upstream/main`（`0ccc062`）包含 `LICENSE` 和 `README.md`，没有完整项目源码，不能用作源码基线。旧的 `main`、`custom` 和 `feature/dnscontrol-adapter` 均没有有效开发提交，已干净地重建到 `upstream/1.1.3`。当前三个分支指向同一基线已核实；未修改任何 `upstream` 分支是基线修正阶段的操作记录。本地 remote-tracking ref 不代表实时远端状态。

## 上游同步规则

不得假设官方长期使用 `main` 作为源码分支。每次同步前必须执行：

```bash
git fetch upstream --prune
git remote show upstream
git branch -r
git ls-remote --heads upstream
```

确认官方当前实际源码/发布分支后，才选择同步来源。当前 `1.1.3` 阶段唯一允许的开发基线是 `upstream/1.1.3`。

后续同步 `1.1.3` 基线的安全流程：

```bash
git fetch upstream --prune
git checkout main
git merge --ff-only upstream/1.1.3
git push origin main
```

本次从错误空基线重建 `main` 是一次性修复，才使用了受确认保护的强制更新。以后只有在确认上游已切换到新的源码发布分支并完成兼容性评估后，才允许变更上述同步来源。`custom` 和 `feature/*` 不直接承接未评估的上游更新；应先创建独立同步分支、测试后再合并。

## 当前 AllinSSL 架构

### 后端与路由

- Go/Gin 服务入口在 `backend/server/server.go`，路由集中注册于 `backend/route/route.go`。
- API 使用 `/v1/<module>/<action>` 形式，以表单参数接收请求，并统一返回 `{code,count,data,message,status}`。
- DNS 模块应新增独立的 `/v1/dns` 路由组，避免 `/v1/alidns/*` 等 Provider 专用 API。
- 当前路由没有分组级 RBAC。登录由内存 session 和安全入口保护；项目目前为单管理员模型。

### 凭据

- `data/data.db` 中 `access` 表保存 `id`、`name`、`type` 和 JSON 字符串 `config`。
- `backend/internal/access/access.go` 已提供 `GetAccess` 和按 `dns` 分类的 `GetAll`。
- Aliyun 授权在前端使用 `type=aliyun`，其 `config` 为 `access_key_id` 与 `access_key_secret`。
- 证书申请在 `backend/internal/cert/apply/apply.go` 读取该授权，传给 lego 的 `alidns` Provider。

DNS 模块应仅接受 `credential_id`，再调用 `access.GetAccess`，验证 `type == "aliyun"` 后将这两个字段映射为 DNSControl 临时 `creds.json`。API、UI、审计记录和普通日志绝不能返回或记录 AccessKey。

### 证书 DNS-01 路径

证书申请使用 `backend/internal/cert/apply/apply.go` 中的 `GetDNSProvider`，并由 lego 完成 DNS-01 TXT 的创建与清理。DNSControl 集成不得改动该调用链，也不得管理 `_acme-challenge`。业务解析和 ACME 临时记录必须保持职责分离。

### 前端

- Vue/TypeScript 应用位于 `frontend/apps/allin-ssl`。
- 菜单/页面排序在 `frontend/apps/allin-ssl/src/config/route.tsx`。
- 授权管理页面位于 `views/authApiManage`，已经安全地以密码输入控件编辑阿里云 AccessKey。
- 前端 API 模式是 `src/api/<module>.ts` 对应 `/v1/<module>/<action>`。

DNS UI 应新增独立 `views/dns`、`api/dns.ts` 和类型文件；只传输结构化 DNS 记录与 ChangeRequest，绝不传输 `dnsconfig.js` 或 Provider 原始凭据。

### 数据库、权限与审计

- 数据库初始化和升级逻辑目前集中在 `backend/migrations/init.go`，并已有 `backend/public/sqlite_migrate` 表结构迁移辅助工具；DNS 模块尚无独立的版本化 migration 记录。
- 当前代码有 workflow 运行日志，但 HTTP 操作审计中间件只是空的注释骨架；不存在可复用的 DNS 审计表或 RBAC。
- 因此 DNS MVP 需要新增 `dns_zones`、`dns_records`、`dns_change_jobs` 和 `dns_audit_logs` 的幂等 `CREATE TABLE IF NOT EXISTS` 初始化，并在 DNS service 内显式写入审计。

现有部署为单进程 SQLite。第一版同 Zone 串行化应在 service 中实现；重启后仍需通过 `dns_change_jobs` 中的非终态记录阻止重复 Apply。若未来改为多实例，必须升级为数据库/分布式锁，不能仅依赖内存 mutex。

## DNSControl 评估

### 锁定版本与安装验证

计划锁定 DNSControl `v5.0.3`，不使用 `latest`。该版本是分析时官方发布的稳定 tag，已下载官方 Linux AMD64 release、用官方 `checksums.txt` 校验 SHA-256，并安装为：

```text
/home/bruce/.local/bin/dnscontrol-5.0.3
```

`dnscontrol-5.0.3 version` 返回 `v5.0.3`。生产镜像实现时应把同一精确版本的二进制复制到镜像内的 `/usr/local/bin/dnscontrol`，并在启动时进行版本健康检查；DNS 模块不可用不能阻止证书模块启动。

### AliDNS 凭据与配置格式

DNSControl `v5.0.3` 的 AliDNS Provider 名称为 `ALIDNS`，其凭据格式与 AllinSSL 已有字段直接匹配：

```json
{
  "alidns": {
    "TYPE": "ALIDNS",
    "access_key_id": "<AllinSSL credential config.access_key_id>",
    "access_key_secret": "<AllinSSL credential config.access_key_secret>",
    "region_id": "cn-hangzhou"
  }
}
```

其中 `region_id` 可省略，DNSControl 默认 `cn-hangzhou`。该 Provider 支持本 MVP 的 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`NS`、`SRV` 和 `CAA`；AliDNS 免费版 TTL 最低为 `600`，配置生成器必须在调用 CLI 前验证此限制。

业务记录的候选配置由后端独立生成，例如：

```javascript
var REG_NONE = NewRegistrar("none");
var DSP_ALIDNS = NewDnsProvider("alidns");

D("example.com", REG_NONE, DnsProvider(DSP_ALIDNS),
  A("test", "192.0.2.1", TTL(600))
);
```

已使用 `dnscontrol check` 验证上述 AliDNS DSL 语法，无需联网或凭据。配置生成器必须只接受内部统一 Record 模型，禁止 Controller 或前端拼接 JavaScript。

### CLI 调用方式

适配器应固定通过参数数组执行，推荐调用：

```text
dnscontrol --no-colors preview --config <job>/dnsconfig.js --creds <job>/creds.json --domains <zone> --cmode none --no-populate --report <job>/preview-report.json
dnscontrol --no-colors push    --config <job>/dnsconfig.js --creds <job>/creds.json --domains <zone> --cmode none --no-populate --report <job>/push-report.json
```

- 使用 `exec.CommandContext`，不可使用 shell、`sh -c` 或拼接后的命令字符串。
- `--domains` 和单 Zone job 缩小影响面。
- `--cmode none` 使第一期单 Zone 操作使用顺序采集，减少 Provider 并发风险；以后经集成测试再评估 `concurrent`。
- `--no-populate` 防止配置错误时创建 Zone；AliDNS 本身也不自动创建不存在的域名。
- 每次操作在 `data/dnscontrol/jobs/<job-id>` 建立 `0700` 目录；`dnsconfig.js`、`creds.json`、报告和 CLI 输出均设为 `0600`。由于 DNSControl 新建 `--report` 文件的默认权限并非 `0600`，适配器须预先以 `0600` 创建报告文件，或在命令返回后立即收紧权限。结束时必须删除 `creds.json`，失败路径也要清理。

### Preview、Push 与机器可读输出

DNSControl `v5.0.3` 的 `preview` 和 `push` 都提供 `--report <file>`。报告是稳定的 JSON 数组，包含 `domain`、`provider`、`corrections` 和文本化的 `correction_details`。因此 UI 应以报告 JSON 作为主要 Preview 数据源，而不是解析终端 stdout。

报告详情仍是人类可读的文本，不含独立的旧值/新值 JSON 字段。适配器应：

1. 存储原始报告 JSON 和 CLI stdout/stderr（脱敏后）。
2. 将 JSON 的 `corrections` 用于交叉校验，不能仅凭数量放行 Apply；还必须确认完整变更语义与用户请求一致。
3. 将详情按受控的 DNSControl `v5.0.3` 文本格式解析为 UI DTO；无法识别的行只允许以脱敏后的通用变更项展示和审计，并阻止 Apply。
4. 为该解析器添加版本固定的 fixtures 测试；升级 DNSControl 时必须重测。

本机已使用 DNSControl 官方 BIND 测试 fixture 验证：`preview` 生成 JSON report，`push` 在临时目录内成功执行并生成 JSON report。该测试只改写了 `/tmp/dnscontrol-v5.0.3-analysis` 下的 fixture 文件，未连接真实 DNS Provider。由于没有专用 AliDNS 测试 Zone 和凭据，未运行 `check-creds`、AliDNS preview 或 AliDNS push。

### Zone 与记录读取

DNSControl `get-zones <credkey> <zone|all>` 可读取一个 Zone 或所有 Zone，并可输出 `js`、`djs`、`zone`、`tsv` 或 `nameonly`。它适合首次验证和导入，但其输出是配置/文本格式，不是稳定的统一 Record API。

因此 MVP 需要在 Adapter 内封装读取路径，并在设计阶段决定以下二选一：

- 固定使用 `get-zones --format=tsv`，在 Adapter 内做版本锁定的解析；或
- 仅将 DNSControl 用于变更执行，读取另选官方可维护接口。

不能让前端直接消费 `get-zones` 的 CLI 文本。该决策需要在第二阶段设计文档中定稿。

### R1 只读页面的筛选与排序

阿里云 `DescribeDomainRecords` API 支持按主机记录、记录值、类型、线路、状态筛选，并支持分页和排序；默认按新增时间倒序。R1 页面采用与之相近的展示体验，但保持读取边界：

- Reader 必须始终完整分页读取 Zone 快照，不能将页面筛选参数传给 AliDNS，避免因筛选条件造成停用记录、特殊线路或其他业务记录漏读。
- 前端在已经校验完成的快照上本地筛选：主机记录和记录值为不区分大小写的模糊匹配；类型、线路和状态为精确匹配；各条件可组合。
- 默认以 AliDNS Provider Record ID 的自然数值倒序展示，作为新增记录优先的稳定近似；用户可点击任一表头在本地切换升序或降序。
- 筛选、排序和重置均不触发 DNS API 调用，也不修改快照内容；重新读取记录才会访问 AliDNS。

## 建议的边界和修改点

后续实现应以新增为主：

```text
backend/internal/dnscontrol/  # executor、adapter、config generator、parser、types
backend/internal/dns/         # DNS service、job、锁和审计
backend/app/api/dns.go        # HTTP handlers
backend/route/route.go        # 注册 /v1/dns
backend/migrations/init.go    # 幂等创建 DNS 表
frontend/apps/allin-ssl/src/views/dns/
frontend/apps/allin-ssl/src/api/dns.ts
frontend/apps/allin-ssl/src/types/dns.d.ts
frontend/apps/allin-ssl/src/config/route.tsx
Dockerfile                    # 固定 DNSControl 二进制
```

不修改 ACME core、证书 scheduler 或 workflow core，直到第二阶段的“DNS 记录申请证书”联动明确需要接入为止。

ChangeRequest 至少绑定：`credential_id`、`zone`、`operation`、内部 Record、申请用户和候选配置 SHA-256。Apply 时必须检查 job 状态、提交用户、credential、zone 和 config hash 与 Preview 完全一致；不一致时拒绝并要求重新 Preview。删除操作还需要前端展示完整 FQDN、类型和值并二次确认。

## 风险与控制

| 风险 | 控制措施 |
| --- | --- |
| AccessKey 泄漏 | 临时凭据文件 `0600`、完成即删、禁写日志/API/audit、错误信息脱敏。 |
| 命令注入 | 固定二进制路径、`exec.CommandContext` 参数数组、严格校验 zone/record/TTL，禁用 shell。 |
| Preview 与 Apply 不一致 | 保存候选配置 SHA-256，Apply 比较 job、用户、凭据、Zone 和 hash。 |
| 并发覆盖同 Zone | 单 Zone change job 锁；非终态 job 阻止新的 Apply。 |
| DNSControl 输出升级破坏 UI | 固定 `v5.0.3`、使用 `--report` JSON、为文本细节解析保留 fixtures。 |
| DNS 模块影响证书 | 完全隔离 DNSControl 与 lego DNS-01，DNS 健康失败仅禁用 DNS 页面。 |
| 上游冲突 | 新目录优先，最小化路由、菜单、migration 和 Dockerfile 改动；同步前先识别实际源码分支。 |
| 当前无 RBAC/审计 | MVP 首先实现 DNS 专用审计；多用户权限模型另立改造任务，不能假定现有 RBAC 存在。 |

## 下一步

下一步仅输出 `docs/development/dnscontrol-integration-design.md`：确定 Adapter 接口、执行器契约、统一 Record 模型、数据表、Change Job 状态机、API、UI 流程和测试策略。该阶段仍不开始 DNS 功能编码。

## 第二阶段复核补充（2026-09-06）

第二阶段方案见 [DNSControl 集成设计](dnscontrol-integration-design.md)，已完成本轮源码与离线设计评审，尚未实现。读取路径选用仓库已依赖的 go-acme AliDNS API 生成客户端，只读调用与 DNSControl 写执行分离。候选配置必须来自已完整读取、确认纳管的 Zone 快照；仅生成本次编辑的一条记录可能触发其他记录被删除。使用固定 `IGNORE` 规则保护 ACME 名称及 Zone 顶点 NS/SOA，正式写入前须通过保护性 fixtures 和专用 AliDNS Zone 验证。

本地 DNSControl 源码的 AliDNS `GetZoneRecords` 会过滤停用记录，`nativeToRecord` 未映射解析线路。MVP 对无法无损表达的 Zone 保持只读。`config_hash` 无法检测远端漂移，Apply 前还需重读快照、重新 Preview 并比较完整计划；CLI 无原子条件写入契约，最后检查与 push 之间的外部并发风险仍然存在。MVP 写入仅适用于业务记录由单一写入方管理的 Zone。

AllinSSL 现有 `GetAllAccess` 直接返回包含 `config` 的授权数据，DNS 页面必须使用只返回授权摘要的新接口。登录 session 当前只保存登录标记和登录校验信息，没有用户 ID；DNS job 的发起身份及确认会话需由新增 DNS 鉴权逻辑建立，不能直接假定可复用多用户身份模型。

本轮 [设计评审](dnscontrol-integration-design-review.md) 还确认：AliDNS 修改（含 TTL-only）采用先删后建，可能短暂缺失并改变 Provider ID；CLI preview 失败也可能输出 corrections=0 的 JSON，必须先验证进程状态；BIND report 可包含保护说明，文本行数不等于业务变更数。DNS 专用会话需在成功登录/登出时显式轮换/失效，前端开发请求不能沿用自动追加 api_token 的通用分支。以上已纳入 R1 设计及测试门槛。

## 2026-09-07 实施范围复核

当前分支已从分析阶段推进到只读 Snapshot、零差异纳管 Preview 和单条创建记录 Preview。创建入口仅支持 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`SRV`、`CAA`，固定默认线路与启用状态；后端基于完整远端 Snapshot 构造候选，并再次校验快照哈希、兼容性、同授权纳管状态、保护名称和 DNSControl 报告语义。浏览器只提交结构化表单，不传 DSL、命令参数、路径或 Provider 凭据。

安全链已在真实 Gin 注册路径验证：成功登录创建并轮换 DNS 身份、CSRF 和 `/v1/dns` 范围的 Secure/HttpOnly/SameSite=Strict 绑定 Cookie；旧绑定失效；DNS 状态变更路由要求显式同源和 CSRF；JSON、超限表单及 query/form `api_token` 在业务处理器前被拒绝。测试使用临时 SQLite、fixture 和 fake 依赖，没有读取真实数据或访问 AliDNS。

本阶段仅生成并展示 DNSControl Preview，成功状态为 `previewed`。没有确认令牌、Apply、AliDNS SDK 写调用或 `dnscontrol push`，也没有乐观修改前端 Snapshot。真实 Provider 的外部并发、AliDNS 对 Preview 详情的实际格式、delete-then-create 可用性和执行后传播仍未验收；任何未来 Push 都必须在专用 Zone、专用最小权限凭据和默认关闭写开关下单独设计、评审与批准。
