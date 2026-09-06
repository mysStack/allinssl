# AllinSSL + DNSControl 第二阶段设计

日期：2026-09-06。状态：R1 已提供 AliDNS 凭据摘要、Zone 列表和完整记录快照的只读 API 与页面；真实写入仍受验收门槛约束。当前页面不会修改任何 DNS 记录，也不调用 DNSControl。评审依据见 [设计评审记录](dnscontrol-integration-design-review.md)。

基线：`upstream/1.1.3`，提交 `73cbcb8a213d959e772fb8ab3120abb9efa476c4`。开发分支：`feature/dnscontrol-adapter`。前置分析：[第一阶段分析](dnscontrol-integration-analysis.md)。保留分析文档中的上游同步规则。

## 1. 范围与已定决策

MVP 提供 AliDNS Zone 读取、完整快照导入、业务记录增改删、Preview/Apply、操作历史与恢复核对。DNSControl `v5.0.3` 是唯一业务 DNS 写执行器；AliDNS API 的 Go 客户端仅承担查询。ACME DNS-01 继续使用原有 lego 路径。

| 项目 | 决策 |
| --- | --- |
| 读取 | 复用仓库已有 `github.com/go-acme/alidns-20150109/v4 v4.7.0`，即 go-acme 维护的 AliDNS API 生成客户端分支；实施时确认所需查询方法，不引入未经评估的新版本 |
| 写入 | 固定二进制，参数数组执行 DNSControl；不得通过 SDK 补写或补删 |
| 纳管 | 写入前完整导入一个兼容 Zone 的全部业务记录并确认纳管；不做“只管理一条、默认删除其他”的配置 |
| 保护 | 固定 IGNORE 保护 ACME 名称及顶点 NS/SOA；不兼容 Zone 只读 |
| 确认 | 服务端不可变候选、完整计划、远端快照、凭据指纹和会话绑定；预览有效期 5 分钟 |
| 并发 | 每个规范化 Zone 一个持久化锁，覆盖跨凭据别名；全局至多 2 个执行任务 |
| 部署 | 单应用实例、SQLite；多实例写入不在 MVP 支持范围 |
| 验证 | 离线 fixture → 专用 AliDNS Zone → 人工评审开放写入；默认仅允许读取和预览 |

不包含 Zone 创建/删除、注册商或 Zone 顶点 NS 委派变更、解析线路/权重/停启用、跨 Zone 批处理、自动 Apply、工作流与证书联动、多用户 RBAC。子域 NS 作为候选业务类型仍需专用 Zone 验收，不能修改顶点 NS。上述能力不能通过客户端隐藏字段绕过限制。

## 2. 源码依据与兼容性边界

- `backend/route/route.go`：现有 `/v1/<module>/<action>` 路由和 POST 表单约定。
- `backend/server/server.go`、`backend/middleware/auth.go`、`backend/app/api/login.go`：内存 session，全局鉴权另支持 `api_token`；session 未保存用户 ID。
- `backend/internal/access/access.go`、`backend/app/api/access.go`：`GetAccess` 可内部读取配置，现有列表接口未经摘要裁剪，不能用于 DNS 凭据下拉框。
- `backend/migrations/init.go`、`backend/public/sqlite_migrate`：SQLite 初始化和表迁移辅助能力；DNS 表及事务由独立存储层管理。
- `frontend/apps/allin-ssl/src/config/route.tsx`、`src/api/access.ts`：菜单、API 封装和类型约定。
- 本地第一阶段留存源码 `/tmp/dnscontrol-v5.0.3-analysis/source/providers/alidns/aliDnsProvider.go`：读取过滤非 ENABLE 记录，补入顶点 NS，执行前调整 TTL。
- 同目录 `providers/alidns/convert.go`：统一记录转换未映射解析线路。官方读取结果必须先检查 Line、Status、权重等属性，不能依靠 DNSControl report 发现丢失字段。
- 同目录 `commands/previewPush.go`：report 含 domain、provider/registrar、corrections、correction_details；详情为文本，不是逐条成功回执。
- 同目录 `providers/alidns/api.go` 的 `updateRecordset`：修改使用先删除再创建；结合 `pkg/diff2/analyze.go` 的 `findTTLChanges`，TTL-only 修改也进入该路径。通常针对 ByRecord 差异中的旧/新记录，不能误写为每次重建整个 RRset。
- `backend/app/api/login.go` 的 `Sign`/`SignOut`：原登录不轮换 DNS 会话状态，原登出只删除 `login`；实现必须增加 DNS 登录代次轮换和登出失效。
- `frontend/apps/allin-ssl/src/api/index.ts`：开发模式的 `useApi` 自动追加 `api_token`/`timestamp`。DNS API 必须使用独立、仅 session 的调用封装。

源码 checkout 已核对为精确 tag `v5.0.3`、提交 `5196387f35f783af46b2c28bd37223a715ce583b`；该 checkout 已有一处第一阶段 BIND fixture 改动，本轮未修改。后续测试 fixture 必须从锁定 tag 固化到仓库，并记录来源、版本和校验值；`/tmp` 留存文件不能作为 CI 或生产依赖。本机二进制 `version` 为 `v5.0.3`。本轮执行了隔离 BIND 只读预览，未运行真实 Provider 操作。

## 3. 模块与接口契约

依赖方向：HTTP/UI → DNS service → Reader、Planner、Executor、Store。公共数据类型放在无 service/CLI 依赖的 `backend/internal/dnsmodel`；接口由 service 持有，`dnscontrol` 只依赖 dnsmodel，由应用启动层组装并注入。禁止 dns 与 dnscontrol 互相 import。HTTP handler 不直接生成 JavaScript、调用 CLI 或操作 Provider。

```text
backend/internal/dnsmodel/   公共 Record、Snapshot、Candidate、Plan 类型
backend/internal/dns/
  service.go             业务校验、纳管、预览确认和恢复
  types.go               service 接口、ChangeRequest、Job
  credentials.go         凭据解析、内部指纹、脱敏摘要
  reader_alidns.go        AliDNS API 只读适配器、分页与字段保真
  store.go               SQLite 事务、CAS、锁、审计
  auth.go                DNS session actor、CSRF、任务归属校验
backend/internal/dnscontrol/
  adapter.go             DNSControl 调用编排
  generator.go           从完整候选快照生成 DSL
  executor.go            受限进程、超时、权限、清理
  report.go              版本固定的严格 report 解析
  testdata/              无真实凭据的脱敏 fixtures
backend/app/api/dns.go
frontend/apps/allin-ssl/src/views/dns/
frontend/apps/allin-ssl/src/api/dns.ts
frontend/apps/allin-ssl/src/types/dns.d.ts
```

少量既有文件接入点还包括 `backend/server/server.go`（DNS 请求预处理顺序与服务组装）、`backend/app/api/login.go`（登录代次）、`backend/route/route.go`、`backend/migrations/init.go`、菜单配置及后续 Dockerfile。不得因未在原新增目录清单列出就跳过鉴权前置条件。

接口草案用于确定职责，实施时可调整 Go 命名但不能削弱契约：

```go
type ZoneReader interface {
    ListZones(context.Context, CredentialRef) ([]ZoneSummary, error)
    ReadZone(context.Context, CredentialRef, string) (Snapshot, error)
}
type Planner interface {
    Build(Snapshot, ChangeRequest, ProtectionPolicy) (Candidate, error)
}
type DNSExecutor interface {
    Health(context.Context) (EngineInfo, error)
    Check(context.Context, Candidate) error
    Preview(context.Context, ExecutionInput) (Plan, error)
    Push(context.Context, ExecutionInput) (ExecutionResult, error)
}
```

`CredentialRef` 仅含授权 ID；密钥由后台执行时解析，不进入业务 DTO。`ExecutionInput` 由 service 创建，携带不可变候选、凭据引用与 job ID；路径、二进制和 flags 由 executor 决定。`Plan` 含严格规范化的变更集合、可解析标记、脱敏报告及计划 hash。`ExecutionResult` 只报告进程事实，业务成功必须由 service 重读验证。

Store 至少提供 `CreateJobAndClaimZone`、`TransitionCAS`、`RecordPlan`、`FinishWithAudit`、`ListRecoverableJobs`。所有跨包错误使用内部枚举，由 handler 转为稳定错误码，不直接回传 SDK/CLI 原始错误。

## 4. Record 与 Snapshot

统一 Record 字段：`provider_record_id`（内部定位/只读显示）、`name`（Zone 相对名称，顶点为 @）、`type`、`ttl`、`value`，以及类型专用 `priority`、`weight`、`port`、`caa_flags`、`caa_tag`。保留只读 `line`、`status`、Provider 扩展属性及 `writable_reason`，不能丢弃后当成普通记录编辑。

| 类型 | value 语义 | 额外约束 |
| --- | --- | --- |
| A / AAAA | IP 地址 | 使用标准 IP 解析与规范化；地址族匹配 |
| CNAME / NS | 绝对目标域名 | 内部小写 ASCII、尾点统一；顶点 NS 只读，MVP 禁止顶点 CNAME |
| TXT | 原始文本 | 保留大小写、空格、引号、反斜杠与 UTF-8；需验证长 TXT 分段往返 |
| MX | 绝对邮件服务器名 | priority 为 0–65535；支持并验证 null MX 的特殊语义 |
| SRV | 绝对目标名或合法的 `.` | priority/weight/port 各为 0–65535；严格校验服务名称 |
| CAA | 属性文本值 | flags 为 0–255；tag 合法性和引号由生成器处理 |

Zone 使用小写 IDNA ASCII，无尾点；输入名称必须在该 Zone 内。只在名称和域名目标上做规范化，不改写 TXT/CAA 内容。支持合法 `_service` 标签，通配符只允许最左标签；必须区分 DNS 通配符与 IGNORE glob 转义。

候选 TTL 必须处于 Provider 版本允许区间。MVP 保守最低 600、最高 86400，若实际版本限制更严取交集；查不到能力时不开放写入。不允许 Provider 静默调整 TTL 后仍展示原预览。

一次 ChangeRequest 限一个 RRset 内的一条增改删，不支持改名或换类型；改名需独立删除和新增。包含 `zone_id`、`operation`、`base_snapshot_hash`、`before`（更新/删除必需）、`after`（新增/更新必需）。后端按完整旧记录验证，禁止按数组下标操作。RRset 按 name+type+line 分组，组内完整值集合参与比较；检查 CNAME 与同名其他记录冲突。

Snapshot 含全量分页结果、读取时间、规范化记录、只读 Provider 元数据、能力限制、保护策略版本与内容 hash。Reader 必须查询所有状态，不能复用 DNSControl 的 `Status=Enable` 筛选。分页失败、限流耗尽、格式未知、重复冲突或检测到读取期间漂移时，快照不可用于 Preview。连续两次完整读取的业务内容与业务记录身份一致才接受；最多完整读取 3 次，每次与紧邻前次比较。ACME 保护范围的动态变化不参与稳定性门槛。这降低分页竞态，但不构成云端事务快照。

缓存只用于展示，不能作为 Apply 依据。区分三种 hash：`snapshot_hash` 覆盖业务记录语义（值、TTL、线路、状态及影响写入的属性）和保护规则，排除 Provider record ID、查询时间和返回顺序；`business_identity_hash` 额外绑定业务记录的 Provider ID，用于执行前发现删后重建等身份漂移；`inventory_hash` 保存完整库存（含保护记录），仅审计。ACME 保护范围的动态值不进入前两者。Apply 后比较语义 hash，不要求被修改记录保留旧 Provider ID；新 ID 重读后进入缓存和审计。

## 5. 纳管、完整配置与记录保护

首次绑定先读取并显示整个 Zone，列出纳管记录与只读记录。管理员明确确认“纳管全部兼容业务记录”；随后生成完整配置，CLI 必须成功退出且完整报告为零业务变更才能标记兼容性状态 `writable`。该状态不等于开启全局写开关。导入操作不执行 push。未完成确认的 Zone 保持 `read_only`。

绑定/重新绑定通过 `kind=adopt` 的异步 job 处理，复用相同 provider+zone 锁、幂等和审计，避免 HTTP 50 秒超时以及绑定与 Apply 并发。零差异验证成功转 `adopted`，非零计划转 blocked，均不进入确认或 push。只有持有锁的 adopt job 可更新 Zone 凭据、纳管状态或保护版本；已有活动 change job 时返回 ZONE_BUSY，不从旁覆盖其上下文。

MVP 不支持任意混合所有权：除下面的固定保护范围外，全部业务记录必须可无损表示并接受统一纳管。出现未知类型、停用记录、非默认线路、权重/路由策略、无法保真的扩展属性或歧义重复记录时，整个 Zone 只读，展示原因；不猜测其转换方式，也不只检查本次编辑的记录。

每份配置固定包含以下规则，来源为上游 `documentation/language-reference/domain-modifiers/IGNORE.md`：

```javascript
IGNORE("_acme-challenge", "*"),
IGNORE("_acme-challenge.**", "*"),
IGNORE("@", "NS,SOA"),
```

以上是 AliDNS 候选规则，不能原样用于所有 Provider 的 fixture。BIND 会自动补 SOA，若同时 IGNORE SOA 会触发安全检查失败；本轮 BIND 试验将 SOA 参数显式匹配旧文件并只 IGNORE 顶点 NS，以验证共享 ACME glob 和无额外业务变化。此适配只用于 BIND 测试，不改变 AliDNS 候选；AliDNS 顶点行为仍须专用 Zone 验收。不能启用 DISABLE_IGNORE_SAFETY_CHECK 让失败测试变绿。

保护 ACME 名称的所有类型同时覆盖 TXT 与常见 CNAME 委派。不能把这些记录也加入候选 DSL；请求若触及保护名直接拒绝。委派到非标准名称的 ACME 写入目标无法由前缀自动识别，相关 Zone 在明确并验证额外固定保护规则之前保持只读。

生成流程：完整基线 → 验证单条请求 → 修改对应 RRset → 保留全部其余业务记录 → 排除保护记录 → 添加固定 IGNORE → 稳定排序与安全序列化。禁止前端传入 DSL、原始 JavaScript 或自定义 IGNORE。禁止 `DISABLE_IGNORE_SAFETY_CHECK`。DNSControl `check` 通过只是语法条件，不能证明不会误删。

`NO_PURGE` 不能作为通用 CRUD 方案：它会保留从候选中删除的记录，导致显式删除无法完成。MVP 采用完整快照加固定保护规则，删除必须是用户明确选中的旧记录；任何额外删改均阻止执行。

业务记录必须由该应用作为单一写入方管理；其他 ACME 写入方只能操作经过验证的保护范围。完整快照不是永久声明状态，每次新请求重新读取实际 Zone，不能把旧缓存恢复到云端。

## 6. Preview 与 Apply

### 6.1 创建与预览

1. 验证 DNS 会话、CSRF、Zone 纳管状态、请求格式与幂等键；解析 Aliyun 凭据并生成内部凭据指纹。
2. 事务内创建 job 并抢占规范化 Zone 锁。锁以 provider+zone 唯一，不以 credential_id 唯一，避免同 Zone 多授权并发。
3. 完整重读，验证客户端 `base_snapshot_hash` 与 `before`；不匹配返回 `REMOTE_DRIFT`。
4. 生成不可变候选并运行 `check`、`preview`。每个命令必须单独确认启动成功、未超时、退出码为 0；preview 还需确认本次新建 report 存在且完整。再解析每个 report 项，校验 Zone、Provider、注册商、行数和详情。非零退出但 report 显示 0 corrections 的情况必须失败，不能成为零差异纳管或 no_change。
5. 将 report 规范化为完整变化集合，与后端根据 before/after 推导的允许变化集合比较。Provider 可把更新表示为删除加新增，必须有固定 fixture 证明这种映射；不能只比较 correction 数量。
6. 出现未知行、跨 Zone、额外删除、保护记录变化、TTL 自动修正或不完整输出时转 `blocked`；保留脱敏诊断，禁止确认。
7. 无差异转 `no_change`；否则保存快照、候选与计划 hash，签发一次性随机确认 token，进入 `awaiting_confirmation`，5 分钟后失效。

数据库只保存确认 token 的 hash。响应返回完整 FQDN、类型、旧值/新值、TTL 和计划摘要；浏览器不持有候选文件路径或凭据。`corrections` 在此版本按报告文本行计数，可能包含保护说明，并非独立语义变更数量、API 调用数量或成功数量。解析器必须按 Provider/版本区分“已知说明”与“变更动作”；已知说明也需完整语法匹配，未知内容拒绝。不能将 BIND 的输出语法和数量假定直接用于 AliDNS。

AliDNS 更新的物理行为是 delete-then-create，包括 TTL-only。Plan 增加 `execution_strategy=delete_create`、`may_temporarily_disappear=true`、`provider_id_may_change=true`；确认页面明确提示短暂缺失及新增失败的可能性。不得展示为原子覆盖或无中断更新，不能忽略低 TTL/负缓存影响。接受该行为是开放更新能力的产品验收项；需要无中断更新的 Zone 保持只读，不能通过 SDK 偷补 UpdateDomainRecord 绕过唯一写执行器决策。

### 6.2 确认与执行

1. Apply 只接受 `job_id`、确认 token、期望 job version、幂等键；删除还需输入完整 FQDN。此接口不接受新记录内容。
2. 事务 CAS 将同一会话创建的有效 job 从 `awaiting_confirmation` 转到 `revalidating` 并消费 token；重复请求返回同一 job，不再次启动进程。
3. 重新解析授权，比较内部凭据指纹、Zone、保护策略、DNSControl 版本、候选 hash；授权删除或轮换使预览失效。指纹采用安装级随机密钥的 HMAC，仅内部存储，不返回 UI。HMAC 密钥以 `0600` 保存于受保护数据目录；密钥轮换或丢失使旧预览失效。
4. 重读并比较 `snapshot_hash`、`business_identity_hash`，再运行一次成功的 preview，比较语义计划 hash 与原计划。任一不同转 `stale`，释放锁并要求新建 Preview；不得自动接受新计划。按每次单独进程结果判断，不能用最后一个 shell 命令的退出码代替前面命令。
5. 事务记录 `applying` 和执行 attempt，提交成功后启动唯一一次 push。执行使用已确认的候选与同一份内存凭据副本，不重新接受客户端参数。
6. 进入 `verifying`，Reader 重读业务记录并比较候选；在 30 秒内有限重读处理 API 可见性延迟。只有进程正常结束、报告完整且其变化语义仍符合已确认计划、实际业务语义与候选一致才记为 `succeeded`。Provider ID 允许因更新改变；保护范围动态值不要求和旧快照一致。push report 计划变化即使最终状态吻合也进入核对并审计，不能事后静默接受。

DNSControl push 会重新读取并计算变化；本设计不假设它能执行保存的 Preview，也不假设 AliDNS 提供原子 compare-and-swap。最后一次检查到 push 之间仍存在外部并发窗口。本地锁不能阻止云控制台、其他应用或凭据管理操作。开放写入的前提是业务记录的单一写入方约束；无法满足时保持只读，不宣称“配置 hash 相同即可保证完全一致”。

重读验证确认的是 Provider 管理平面状态，不是全球 DNS 缓存传播完成。UI 分别显示执行结果与 TTL 提示，不以本机解析结果决定写入成功。

## 7. Job 状态、幂等与恢复

| 状态 | 允许的下一状态 | Zone 锁 |
| --- | --- | --- |
| queued | previewing / cancelled / expired / failed | 持有 |
| previewing | awaiting_confirmation / no_change / adopted / blocked / failed | 持有；终态释放；adopted 仅 kind=adopt |
| awaiting_confirmation | revalidating / cancelled / expired | 持有，最长 5 分钟 |
| revalidating | applying / stale / failed | 持有；终态释放 |
| applying | verifying / needs_reconciliation | 持有 |
| verifying | succeeded / needs_reconciliation | 持有 |
| needs_reconciliation | reconciled_applied / reconciled_unchanged / reconciled_partial | 持有直至核对完成 |
| no_change / adopted / blocked / failed / cancelled / expired / stale / succeeded / reconciled_* | 无自动重试 | 释放 |

`failed` 仅用于确定尚未启动 push 的失败。push 已启动后，超时、非零退出、报告缺失、状态写入失败或进程崩溃均不能认定“没改动”，转人工核对。取消只适用于 queued/awaiting_confirmation，运行中不提供会让用户误以为可回滚的取消按钮。

DNS 后台每 15 秒用状态/version CAS 回收超过 expires_at 的待确认 job，写入审计并释放锁；不能依赖浏览器轮询触发过期。确认 token 丢失可由原会话取消后重新 Preview，或等待后台过期。此机制不回收 applying/verifying/needs_reconciliation 的锁。

服务启动先获取单实例运行锁，再恢复 DNS job；重启会使内存 session 和所有旧确认 token 失效。queued/previewing/revalidating 可安全终止为 failed，awaiting_confirmation 过期；applying/verifying 转 needs_reconciliation。后两者必须先证明旧子进程已经退出，未知时保留锁，不依据过期时间放行第二个写入。

Linux executor 建立独立进程组、超时终止整个组并 wait 回收，采用父进程退出终止机制；启动恢复应校验保存的 PID 和进程启动身份，不能只凭 PID 杀进程。容器退出/重启及父进程崩溃必须覆盖集成测试。

`reconcile_job` 只读取实际记录并展示与基线/候选的差异。实际等于候选记 reconciled_applied；等于基线记 reconciled_unchanged；混合或额外变化记 reconciled_partial，需管理员确认真实快照后释放锁。仍无法读取或存在活跃旧执行器时不释放。需要修复的变更必须从当前状态新建 Preview；不自动回滚或重推旧 job。

`(actor_id, action, idempotency_key)` 唯一，记录请求 hash；同键同内容返回原结果，同键不同内容返回 `IDEMPOTENCY_CONFLICT`。这提供应用内至多一次启动约束，不声称跨数据库与云 API 的 exactly-once 事务。

## 8. SQLite 数据设计

时间字段统一 UTC RFC3339，ID 由服务端生成，外部输入不能决定文件名或 SQL 标识符。下表为逻辑字段，实施迁移时补全类型、NOT NULL、枚举 CHECK 和索引。

| 表 | 主要字段与约束 |
| --- | --- |
| dns_zones | id、provider、name_ascii、credential_id、status、protection_policy_json、policy_version、snapshot_hash、snapshot_at、revision；UNIQUE(provider,name_ascii)，同 Zone 重绑定授权使未执行预览失效 |
| dns_records | id、zone_id、snapshot_revision、provider_record_id、name、type、ttl、typed_value_json、line、status、metadata_json、ownership；索引(zone_id,name,type)，不把 name+type 当成单记录唯一键 |
| dns_change_jobs | id、kind(adopt/change)、zone_id、credential_id、credential_fingerprint、actor_id、session_binding_hash、auth_epoch、state、version、request_json、request_hash、base_snapshot_json/hash、business_identity_hash、candidate_snapshot_json/hash、config_hash、plan_json/hash、engine_version、policy_version、confirm_token_hash、expires_at、attempt_id、process_identity、error_code、created_at/updated_at |
| dns_audit_logs | id、job_id、zone_id、actor_id、event、from_state、to_state、request_id、safe_detail_json、created_at；业务层只追加，索引(job_id,created_at) |
| dns_zone_locks | provider、zone_name 主键、job_id 唯一、acquired_at；与 jobs 事务性维护，非简单 TTL 锁 |
| dns_idempotency_keys | actor_id、action、key 联合唯一、request_hash、job_id、created_at；保存任务存续期间及终态后至少 7 天 |
| dns_schema_migrations | version 主键、applied_at；仅跟踪 DNS 表增量，不取代现有全局迁移机制 |

相较分析阶段的四张表，新增锁表、幂等表与 DNS 迁移版本表。job 持有历史快照，dns_records 是最近完整读取缓存，不作为长期期望状态。导入/刷新以完整事务替换当前 Zone 缓存，不能让 UI 看到半页数据。

使用参数化 SQL、短事务、SQLite busy timeout；抢锁、状态 CAS 和必要审计同事务提交。数据库事务期间不得等待 SDK/CLI。审计写入失败必须阻止新 push；push 后落库失败由非终态 job 在恢复时核对。锁释放和终态审计同事务完成。

幂等初始化独立于证书表，不改现有证书 schema。新增 DNS 表/列遵循现有迁移辅助能力并有显式 DNS schema version；每连接启用外键并测试。升级前备份 SQLite，失败只禁用 DNS 功能；无自动 DROP 回滚。数据库不可用时自然不能提供 DNS 写入。

## 9. API 与鉴权

全部路径位于 `/v1/dns`，MVP 使用 POST 表单和现有响应约定；DNS 专用前端封装不调用开发模式自动加 API token 的 useApi 分支。嵌套请求由 `request` 字段承载严格 JSON。总请求限制 64 KiB，禁止未知字段和重复冲突字段。返回既有 `{code,count,data,message,status}` 外壳，DNS 错误在 data 中提供稳定 `error_code`、request_id，不依赖解析 message。

在 server 注册全局 `SessionAuthMiddleware` 之前安装仅针对 `/v1/dns` 的请求预处理：限制 body 大小、允许的 Content-Type，并拒绝 query/body 携带的 api_token、timestamp。原因是全局 `checkApiKey` 已调用 c.Bind；若到 DNS 路由组才限大小，解析已发生。DNS 参数统一取表单 body，避免 query/body 覆盖歧义。其他模块沿用原有路径。

| 动作 | 请求要点 | 返回/行为 |
| --- | --- | --- |
| get_health | 无 | 引擎版本、read/write 可用性、原因、DNS CSRF token |
| get_credentials | 无 | 仅 id/name/type；不复用 get_all 的原始响应 |
| get_zones | credential_id | 云端 Zone 摘要及本地纳管状态 |
| get_records | zone_id 或读取范围内的 credential_id+zone | 记录、只读原因、snapshot_hash、读取时间 |
| bind_zone | credential_id、zone、snapshot_hash、明确纳管确认、idempotency_key | 异步 adopt job_id；持 Zone 锁进行零差异验证，成功终态 adopted；不 push |
| preview_change | zone_id、request、idempotency_key | 异步 job_id；轮询查看预览 |
| get_job | job_id | 状态、安全计划、有效期；确认 token 仅返回创建会话 |
| apply_change | job_id、confirm_token、expected_version、idempotency_key、删除确认 FQDN | 同一 job，进入后台 revalidating |
| cancel_job | job_id、expected_version | 仅取消尚未执行的待确认任务 |
| reconcile_job | job_id、expected_version、核对确认 hash | 只读核对；确认必须绑定最新重读快照 |
| get_jobs / get_audit | zone_id、分页参数 | 脱敏历史；最大每页 100 |

新增 DNS 组中间件显式校验 session `login == true`、`__login_key` 有效及超时；仅有全局 `api_token` 的请求不能调用 DNS MVP。不能假定通过全局中间件就有可归属用户。

MVP actor 为服务端固定的单管理员主体 `local-admin`，不是客户端传来的 user_id；另在已验证的 session 中保存随机 DNS 会话标识及登录代次 auth_epoch，job 绑定二者。必须在 Sign 成功时轮换 auth_epoch、DNS CSRF token 和 DNS 会话标识，清除旧确认 token；SignOut 同步失效这些值。仅删除 login 不足以保证同一浏览器重新登录后旧确认不可复用。旧待确认 job 按过期机制结束；已开始的 push 不因登出被假定取消。

待确认 job 只能由创建会话和同一登录代次 Apply。幂等键命中也必须先检查任务归属及登录代次，不能向其他会话返回确认 token。重登或重启后重新 Preview；已执行任务由当前管理员核对。未来多用户版本需显式替换 actor 和授权策略，不沿用固定身份。

DNS 组生成并检查独立 CSRF token，状态变更要求同源校验和 token；UI 同源部署，不开放通配 CORS。token 和确认 token 不写日志。get_job 若返回确认 token，应从会话关联的安全临时存储读取；数据库只保留 hash，丢失时重新 Preview。

典型错误码：`DNS_UNAVAILABLE`、`ZONE_READ_ONLY`、`PROTECTED_RECORD`、`UNSUPPORTED_RECORD`、`ZONE_BUSY`、`REMOTE_DRIFT`、`PREVIEW_STALE`、`PLAN_UNPARSEABLE`、`PLAN_OUT_OF_SCOPE`、`CREDENTIAL_CHANGED`、`CONFIRMATION_INVALID`、`IDEMPOTENCY_CONFLICT`、`NEEDS_RECONCILIATION`。限流/网络错误可以重试只读操作，不能自动重试 push。

## 10. 执行器、凭据与部署

生产默认 `/usr/local/bin/dnscontrol`，要求版本严格为 `v5.0.3`；开发允许由服务端配置绝对路径 `/home/bruce/.local/bin/dnscontrol-5.0.3`。版本不符禁用写入；配置路径不能来自 API。固定使用分析文档中的 `--domains`、`--cmode none`、`--no-populate`、`--no-colors`、`--report`，只允许 check/version/preview/push 的已审查组合。

默认 check 10 秒、preview 60 秒、push 120 秒，进程组终止宽限 5 秒。stdout/stderr 各限 1 MiB、report 限 4 MiB；超限时阻止继续确认，若 push 已启动则进入核对。stdin 关闭、环境变量白名单、禁止继承任意云凭据和调试参数。

每次 CLI 调用使用独立 job/attempt 子目录 `0700`，文件 `0600`；报告在执行前创建并拒绝符号链接。不允许“执行完成后才收紧 report 权限”的暴露窗口。不要在并发 Go 请求中调用进程全局 syscall.Umask；所需权限由显式创建参数保障，若实施采用 umask 0077，必须在独立 DNS 子进程入口设置并测试，不能改变证书服务的全局文件创建行为。临时 creds 只包含当前凭据，绝不进入 argv 或日志；每次调用后 defer 清理，启动时清理已确认不再运行的残留 creds。

不能让全局 HTTP logger 记录 DNS 请求正文、token 或 Provider 错误全文。SDK/CLI 输出先按密钥与已知敏感字段脱敏再持久化，UI 用纯文本渲染；日志脱敏不意味着允许公开 TXT 内容。默认终态诊断文件保留 7 天、审计与 job 摘要 90 天；非终态任务不自动清理。数据库和备份含 DNS 清单，应按敏感运维数据限制访问。

Docker 构建锁定版本和各架构官方 SHA-256，验证后复制二进制；禁止运行时下载 latest。第一阶段只验证了 Linux AMD64，ARM64 镜像须有独立资产校验及执行测试，不能复制 AMD64 冒充多架构支持。镜像验证 version、运行权限、CA 根证书和 DNS job 目录权限。功能开关 `DNSCONTROL_ENABLED=false` 为默认；另以 `DNSCONTROL_WRITE_ENABLED=false` 控制写入，通过测试和评审后显式开启。CLI 不可用不能让证书服务启动失败。

## 11. UI 流程

菜单新增“DNS 管理”。选择授权摘要 → 获取 Zone → 查看记录与只读原因 → 确认完整纳管 → 新增/编辑/删除 → 展示完整预览 → 用户确认 → 查看执行与核对结果。记录编辑器按类型呈现字段；不暴露 DSL、文件路径或密钥。

预览展示新增/修改/删除集合、FQDN、完整旧值/新值、TTL、到期倒计时和保护记录说明。大 TXT 可折叠，但确认前必须可展开完整内容。删除需输入完整 FQDN；服务端仍验证该值。预览过期、远端漂移或解析失败时禁用确认，并解释重新预览原因。

任务执行轮询采用 1/2/5 秒退避，页面离开不等于取消后台任务。重复点击 Apply 返回原任务。`needs_reconciliation` 显示“结果待核对，可能已有部分变更”，提供读取核对入口，不提供盲目重试。显示 AliDNS 管理平面成功与 DNS 缓存传播的区别。

## 12. 测试与实施顺序

| 测试层 | 必须覆盖的断言 |
| --- | --- |
| 纯单元 | IDNA/越界域名/通配符/注入；各类型往返、TXT 分段、MX/SRV/CAA、TTL；记录顺序变化不改 hash，值/线路/状态改变必改 hash |
| Reader mock | 全分页、限流/中断、重复页、读取间漂移、停用与线路识别；不返回半份快照 |
| Planner | 完整快照增改删只改变请求目标；零差异纳管；未知类型/元数据拒写；ACME/顶点 NS/SOA 永远不进入候选 |
| CLI fixture | 固定版本 check/preview/BIND 临时 push；ACME 根与多级子名、动态插入 TXT、CNAME 委派、顶点 NS、多值 RRset、TXT 特殊字符；绝不使用生产凭据 |
| Report | 单次更新的多行表达、未知行、计数不符、额外 Provider/registrar、额外删除、缺失/损坏/超限报告均不能进入可确认态 |
| Store/状态机 | 并发两个授权操作同 Zone 只有一个获锁；token 单次消费、同键异内容冲突；审计失败不启动 push；各状态崩溃重启、孤儿子进程与 PID 重用 |
| Executor | 文件创建瞬间权限、符号链接、环境污染、超时子进程、非零退出、密钥脱敏、磁盘满、凭据清理；push 结果不明进入核对 |
| API/UI | 无登录/仅 api_token/跨会话/CSRF 拒绝；接口无 config/密钥；只读状态、过期确认、双击、断网轮询、删除确认与核对流程 |
| 专用 AliDNS Zone | 每种开放类型的真实增改删、线路/停用阻断、TTL 边界、完整纳管零差异、受保护 ACME 并发变化、部分失败与重读；最终无多余业务变更 |
| 回归/镜像 | 原证书 DNS-01、证书调度、授权页面、菜单；引擎缺失/版本错误时证书仍启动；AMD64 与声明支持的其他架构分别测试 |

R1 增补回归：preview 退出非零但 JSON corrections=0；BIND 保护说明导致报告 7 行但只有 1 项业务修改；TTL-only 的 delete/create 与新 Provider ID；SignOut→Sign 后旧确认失效；开发前端不附带 api_token；body 限额在全局 c.Bind 前生效；adopt 与 change 抢同一锁；浏览器离开后待确认任务自动过期；进程权限设置不影响证书文件。

实施分为四个可评审增量：①类型、只读 Reader、凭据摘要与纳管检查；②生成器、严格 report 解析、离线 fixtures；③事务 job、确认、锁、执行与恢复，写开关仍关闭；④UI、镜像和专用 Zone 联调。每个增量独立提交，避免与上游同步混合。第一增量的公共模型、Reader 和凭据部分已开始实施；其余能力仍按此顺序推进。

正式写入前必须完成：锁定来源与校验记录、保护性 fixtures、完整计划解析、真实 AliDNS 类型/元数据验收、恢复演练和单一业务写入方确认。未验证的类型可继续只读，不以“设计已列出”视为已支持。

## 13. 本阶段交付与剩余边界

本轮已实施第一增量中的离线公共模型、AliDNS 只读 Reader 和 Aliyun 凭据摘要/解析：Reader 以完整分页读取所有状态、线路和类型，保留停止记录、特殊线路和扩展属性，并要求相邻两次业务语义与 Provider Record ID 一致。分页异常、超限、字段缺失或远端漂移均不返回部分 Snapshot；ACME 保护范围的动态记录不影响稳定性门槛。凭据列表仅返回 `id`、`name`、`type`，解析出的 AccessKey 不可 JSON 序列化或格式化输出。

本增量没有修改 DNSControl 写入执行器、数据库 schema、HTTP/API/UI、镜像或开关；没有运行真实 AliDNS 查询、preview 或 push。离线 mock/隔离测试不等同于真实 Provider 验收，完整包测试仍依赖可用的 Go 模块下载环境。

本轮评审已修正读取与执行结果判定、生命周期和接入顺序方面的缺口，可进入第一批只读能力及离线组件实施。全 Zone 纳管、单一业务写入方和 delete-then-create 是写入开放前必须向使用者明确的限制。DNSControl 原始 CLI 缺少原子计划执行能力是明确保留的技术边界，如未来必须支持业务多写入方，需要重新设计执行契约，不能通过缩短预览有效期宣称消除竞态。
