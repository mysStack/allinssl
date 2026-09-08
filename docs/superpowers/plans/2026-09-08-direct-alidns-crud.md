# 直连 AliDNS 记录管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 DNS 页面从 DNSControl 预览切换为使用现有 AK/SK 直接管理 AliDNS 业务记录。

**Architecture:** 删除 DNSControl 的路由、服务、SQLite 任务存储和前端 Preview 流程。保留现有完整分页 Reader，并在同一 `backend/internal/dns` 服务中调用已安装的 AliDNS 客户端完成单记录增改删和启停；每次成功调用后重新读取完整快照。首期只实现 AliDNS，不建立单实现的多厂商抽象。

**Tech Stack:** Go 1.24、Gin、SQLite、`github.com/go-acme/alidns-20150109/v4`、Vue 3、Naive UI、Vitest、Vite。

**Spec:** `docs/development/dns-provider-integration-design.md`

## Global Constraints

- 仅支持 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`SRV`、`CAA` 的业务记录。
- `NS`、`SOA`、`_acme-challenge` 始终只读；ACME lego 调用链不得改动。
- 复用已安装 AliDNS 客户端和现有 `access` 中的 AK/SK；浏览器不得提交或接收密钥。
- 不增加 DNSControl、任务状态机、确认令牌、批量操作、审计表或单实现 Provider 接口。
- 所有 DNS 写入保持现有登录会话、同源和 CSRF 保护；未知字段必须拒绝。
- 不删除运行中 SQLite 数据库内的历史 DNSControl 表，也不删除用户未跟踪的 `backend/internal/dnscontrol/testdata/bind/creds.json`、构建产物或本地二进制。
- 每个可独立验证阶段只提交一个详细中文 commit；不得修改或推送 `upstream`。

---

## 文件结构

| 路径 | 职责 |
| --- | --- |
| `backend/internal/dns/service.go` | 将只读 Reader 工厂升级为单个 AliDNS Zone 服务工厂，并暴露增改删和启停。 |
| `backend/internal/dns/reader_alidns.go` | 扩展受控 AliDNS 客户端调用并保留完整分页读取。 |
| `backend/internal/dns/record.go` | 记录输入归一化、保护校验、冲突差异校验和 AliDNS 请求转换。 |
| `backend/internal/dns/record_test.go` | 使用假的 AliDNS 客户端验证写入请求、保护规则、刷新和不确定结果。 |
| `backend/app/api/dns.go` | 替换 Preview/任务 API，严格解析四个记录变更表单。 |
| `backend/app/api/dns_test.go` | 验证 HTTP 表单、会话边界、快照返回和服务错误。 |
| `backend/middleware/dns_auth.go` | 将受保护写路径改为四个直连记录接口，保留读取接口规则。 |
| `backend/middleware/dns_auth_test.go` | 验证新写路径的同源、CSRF、字段和请求体限制。 |
| `backend/route/route.go` | 删除 DNSControl Preview 路由并注册直连记录路由。 |
| `backend/migrations/init.go` | 停止初始化旧 DNSControl SQLite 存储。 |
| `frontend/apps/allin-ssl/src/types/dns.d.ts` | 删除 DNSControl 类型，定义记录表单和变更请求类型。 |
| `frontend/apps/allin-ssl/src/api/dns.ts` | 调用新记录接口，并传递 CSRF Token。 |
| `frontend/apps/allin-ssl/src/api/dns.spec.ts` | 验证每种记录类型只序列化允许字段。 |
| `frontend/apps/allin-ssl/src/views/dns/useController.tsx` | 删除轮询与 Preview 状态，管理新增、编辑、删除、启停和刷新快照。 |
| `frontend/apps/allin-ssl/src/views/dns/useController.spec.ts` | 验证控制器表单、受保护记录和变更后刷新。 |
| `frontend/apps/allin-ssl/src/views/dns/index.tsx` | 显示阿里云风格的记录操作、编辑表单和删除确认。 |

以下已跟踪的旧实现会删除：`backend/internal/dnscontrol/`、`backend/internal/dns/adopt.go`、`backend/internal/dns/adopt_runtime.go`、`backend/internal/dns/adopt_types.go`、`backend/internal/dns/store.go`、`backend/internal/dns/change.go` 及对应测试。

## Task 1: 删除 DNSControl 常规流程

**Files:**
- Modify: `backend/route/route.go:99-109`
- Modify: `backend/app/api/dns.go:12-74`
- Modify: `backend/middleware/dns_auth.go:50-54`
- Modify: `backend/migrations/init.go:150-152`
- Modify: `frontend/apps/allin-ssl/src/types/dns.d.ts:31-111`
- Modify: `frontend/apps/allin-ssl/src/api/dns.ts:21-73`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.tsx:24-345`
- Modify: `frontend/apps/allin-ssl/src/views/dns/index.tsx:50-264`
- Delete: `backend/internal/dnscontrol/engine.go`
- Delete: `backend/internal/dnscontrol/generator.go`
- Delete: `backend/internal/dnscontrol/preview.go`
- Delete: `backend/internal/dnscontrol/report.go`
- Delete: `backend/internal/dnscontrol/types.go`
- Delete: `backend/internal/dnscontrol/workspace.go`
- Delete: `backend/internal/dnscontrol/*_test.go`
- Delete: `backend/internal/dns/adopt.go`
- Delete: `backend/internal/dns/adopt_runtime.go`
- Delete: `backend/internal/dns/adopt_types.go`
- Delete: `backend/internal/dns/adopt_test.go`
- Delete: `backend/internal/dns/adopt_runtime_test.go`
- Delete: `backend/internal/dns/store.go`
- Delete: `backend/internal/dns/store_test.go`
- Delete: `backend/internal/dns/change.go`
- Delete: `backend/internal/dns/change_test.go`
- Modify: `backend/app/api/dns_test.go`
- Modify: `frontend/apps/allin-ssl/src/api/dns.spec.ts`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.spec.ts`

**Interfaces:**
- Removes: `DNSAdoptService`, `AdoptService`, `GetHealth`, `BindZone`, `CreateRecordPreview`, `GetJob`, `/v1/dns/get_health`, `/v1/dns/bind_zone`, `/v1/dns/create_record_preview`, `/v1/dns/get_job`.
- Preserves: `DNSReaderService.ListCredentials`, `ListZones`, `ReadZone`; login session, `middleware.DNSRequestPreflight`, `middleware.DNSSessionRequired`, local filtering and sorting.

- [ ] **Step 1: 写入路由移除测试**

在 `backend/app/api/dns_test.go` 增加路由注册测试，断言旧路径返回 `404`，现有读取路径仍能通过 DNS 会话中间件到达处理器。

```go
for _, path := range []string{
	"/v1/dns/get_health", "/v1/dns/bind_zone",
	"/v1/dns/create_record_preview", "/v1/dns/get_job",
} {
	request := httptest.NewRequest(http.MethodPost, path, nil)
	router.ServeHTTP(httptest.NewRecorder(), request)
	if recorder.Code != http.StatusNotFound { t.Fatalf("%s = %d", path, recorder.Code) }
}
```

- [ ] **Step 2: 运行失败测试确认旧路由仍存在**

Run: `go test ./backend/app/api -run TestDNSRoutesDoNotExposeDNSControl -count=1`

Expected: FAIL，因为旧 DNSControl 路由仍已注册。

- [ ] **Step 3: 删除旧服务和页面依赖**

删除上述已跟踪的 DNSControl 源码、测试和任务 SQLite 初始化；从 `DefaultDNSHandler` 删除 `NewSQLAdoptService`、恢复任务和 `dnscontrol` 导入。路由只保留读取接口，前端删除 Health、Adopt、Job、Preview 类型、API、轮询和页面元素。保留记录表格、筛选、排序与选择授权/Zone 的读取逻辑。

不要删除未跟踪的 `backend/internal/dnscontrol/testdata/bind/creds.json`；删除已跟踪文件后该目录可保留为用户本地文件所在目录。

- [ ] **Step 4: 运行定向测试**

Run: `go test ./backend/app/api ./backend/middleware ./backend/internal/dns -count=1`

Expected: PASS，且无 `dnscontrol`、`AdoptService` 或 `dns_change_jobs` 的编译引用。

- [ ] **Step 5: 运行前端移除检查**

Run: `rg -n "dnscontrol|adopt|create_record_preview|bind_zone|get_job|get_health" frontend/apps/allin-ssl/src/{api,types,views/dns}`

Expected: exit `1`；已移除的普通 DNS 页面代码中没有这些标识符。

- [ ] **Step 6: 提交阶段**

```bash
git add backend frontend/apps/allin-ssl/src backend/migrations/init.go
git commit -m "重构：移除 DNSControl 预览与纳管流程"
```

## Task 2: 实现 AliDNS 单记录服务

**Files:**
- Modify: `backend/internal/dns/service.go:17-119`
- Modify: `backend/internal/dns/reader_alidns.go:20-352`
- Create: `backend/internal/dns/record.go`
- Create: `backend/internal/dns/record_test.go`
- Modify: `backend/internal/dns/service_test.go`
- Modify: `backend/internal/dns/reader_alidns_test.go`

**Interfaces:**
- Consumes: `CredentialStore.Resolve`, `AliDNSReader.ReadZone`, `dnsmodel.Record`, `dnsmodel.BuildSnapshot`。
- Produces:

```go
type RecordInput struct {
	Name, Type, Value, Line string
	TTL                     int64
	Priority, Weight, Port  *int64
	CAAFlags                *int64
	CAATag                  string
}

func (s Service) CreateRecord(context.Context, int64, string, RecordInput) (dnsmodel.Snapshot, error)
func (s Service) UpdateRecord(context.Context, int64, string, string, RecordInput) (dnsmodel.Snapshot, error)
func (s Service) DeleteRecord(context.Context, int64, string, string) (dnsmodel.Snapshot, error)
func (s Service) SetRecordStatus(context.Context, int64, string, string, string) (dnsmodel.Snapshot, error)
```

- [ ] **Step 1: 写入服务失败测试**

在 `record_test.go` 创建 `fakeAliDNSAPI`，实现读取与四个 AliDNS 写请求。先写表驱动测试，断言：

```go
snapshot, err := service.CreateRecord(ctx, 1, "example.com", RecordInput{
	Name: "api", Type: "A", TTL: 600, Value: "192.0.2.20", Line: "default",
})
if err != nil { t.Fatal(err) }
if got := fake.addRequests[0]; *got.RR != "api" || *got.Type != "A" || *got.TTL != 600 { t.Fatalf("request = %#v", got) }
if len(snapshot.Records) != 2 { t.Fatalf("records = %d", len(snapshot.Records)) }
```

覆盖 `MX` 的 `Priority`、`SRV` 的 `Priority` 与编码后的 `Value`、`CAA` 的 flags/tag/value 编码、更新请求的 `RecordId`、删除、`Enable`/`Disable` 状态请求和每种成功操作后的完整重新读取。

- [ ] **Step 2: 运行失败测试确认写能力尚不存在**

Run: `go test ./backend/internal/dns -run 'TestService(Create|Update|Delete|Set)Record' -count=1`

Expected: FAIL，因为 `Service` 还没有四个记录操作。

- [ ] **Step 3: 扩展受控 AliDNS 客户端调用**

在 `AliDNSAPI` 加入以下方法，并让 `aliDNSClient` 用现有 `WithContext` 客户端函数实现：

```go
AddRecord(context.Context, *alidns.AddDomainRecordRequest) (*alidns.AddDomainRecordResponse, error)
UpdateRecord(context.Context, *alidns.UpdateDomainRecordRequest) (*alidns.UpdateDomainRecordResponse, error)
DeleteRecord(context.Context, *alidns.DeleteDomainRecordRequest) (*alidns.DeleteDomainRecordResponse, error)
SetRecordStatus(context.Context, *alidns.SetDomainRecordStatusRequest) (*alidns.SetDomainRecordStatusResponse, error)
```

将工厂返回类型从只读 `zoneReader` 扩展为可读取和写入同一 Zone 的内部服务。不要创建 `DNSProvider`、工厂注册表或第二套 SDK。

- [ ] **Step 4: 实现最小记录归一化与保护校验**

在 `record.go` 实现：

1. 仅接受七种首期类型，`Line` 必须非空，`TTL` 必须在当前快照 `Limits` 和 `600..86400` 的交集内。
2. 通过把候选记录加入快照（更新时先移除原 `record_id`）调用 `dnsmodel.BuildSnapshot` 获得规范化名称、值和专属字段。
3. 仅拒绝候选操作新引入的 `DUPLICATE_RECORD`、`CNAME_CONFLICT`、`NULL_MX_CONFLICT`、`MIXED_RRSET_TTL`、无效名称、无效值或 TTL；已有的特殊线路、停用记录和元数据不能阻止无关业务记录操作。
4. 对新增候选和已存在的编辑/删除/启停目标，拒绝 `Record.Protected`，即顶点 `NS`/`SOA` 与所有 `_acme-challenge` 名称。
5. 将规范化记录转换为 AliDNS 请求：`MX` 使用 `Priority`；`SRV` 使用 `Priority` 和由 `Weight Port Target` 组成的 `Value`；`CAA` 使用 `Flags Tag Value` 组成的 `Value`；其余类型传递规范化 `Value`。
6. 写请求超时或返回错误时返回一个稳定的“结果不明/写入失败”错误，不自动重试；仅在 AliDNS 明确成功后刷新完整快照。

- [ ] **Step 5: 运行服务测试**

Run: `go test ./backend/internal/dns -count=1`

Expected: PASS。确认假客户端从不接收 AK/SK，且所有成功路径读取两次：写前验证一次、写后刷新一次。

- [ ] **Step 6: 提交阶段**

```bash
git add backend/internal/dns
git commit -m "功能：实现 AliDNS 单记录增改删与启停服务"
```

## Task 3: 发布受保护的直连记录 API

**Files:**
- Modify: `backend/app/api/dns.go:15-420`
- Modify: `backend/app/api/dns_test.go`
- Modify: `backend/route/route.go:99-109`
- Modify: `backend/middleware/dns_auth.go:50-54`
- Modify: `backend/middleware/dns_auth_test.go`

**Interfaces:**
- Consumes: Task 2 的 `Service.CreateRecord`、`UpdateRecord`、`DeleteRecord`、`SetRecordStatus`。
- Produces: `/v1/dns/create_record`、`/v1/dns/update_record`、`/v1/dns/delete_record`、`/v1/dns/set_record_status`；所有成功响应为完整 `dnsmodel.Snapshot`。

- [ ] **Step 1: 写入 API 失败测试**

替换 API fake service，使其实现四个方法并记录输入。测试合法新增请求必须获得完整快照：

```go
form := url.Values{
	"credential_id": {"1"}, "zone": {"example.com"}, "name": {"api"},
	"type": {"A"}, "ttl": {"600"}, "value": {"192.0.2.20"},
	"line": {"default"}, "csrf_token": {"csrf-a"},
}
request := httptest.NewRequest(http.MethodPost, "/v1/dns/create_record", strings.NewReader(form.Encode()))
request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
```

分别测试编辑、删除、启停；增加跨 Zone 记录 ID、保护记录、未知字段、无效 SRV/CAA 字段、缺少 CSRF、不同 Origin 和查询参数 `api_token` 的拒绝用例。

- [ ] **Step 2: 运行失败测试确认接口尚未注册**

Run: `go test ./backend/app/api ./backend/middleware -run 'TestDNS(Create|Update|Delete|Set)' -count=1`

Expected: FAIL，因为直连记录路由和表单处理器尚未实现。

- [ ] **Step 3: 实现严格表单处理器与路由**

为四个操作写独立处理器。共享记录表单解析器必须允许的公共字段正好为：`credential_id`、`zone`、`record_id`（更新需要）、`name`、`type`、`ttl`、`value`、`line`、`csrf_token`；再按 `MX`、`SRV`、`CAA` 精确加入专属字段。删除仅允许 `credential_id`、`zone`、`record_id`、`csrf_token`；状态仅额外允许 `status`，其值限定为 `ENABLE` 或 `DISABLE`。

在 `protectedDNSMutation` 列出四个新路径。新增轻量 `/v1/dns/get_session`，仅返回当前 DNS 会话的 `csrf_token`；它替代旧的 DNSControl Health 查询，并加入 `emptyDNSReadRequest` 允许列表。所有写处理器调用 Task 2 服务并直接返回刷新后的快照。

- [ ] **Step 4: 运行 API 与中间件测试**

Run: `go test ./backend/app/api ./backend/middleware -count=1`

Expected: PASS，四个写接口在没有有效同源 CSRF 时均返回 `403`，且成功响应不包含 AK/SK 或原始 AliDNS 错误对象。

- [ ] **Step 5: 提交阶段**

```bash
git add backend/app/api/dns.go backend/app/api/dns_test.go backend/route/route.go backend/middleware/dns_auth.go backend/middleware/dns_auth_test.go
git commit -m "接口：开放受保护的 AliDNS 记录管理 API"
```

## Task 4: 完成 DNS 管理页面交互

**Files:**
- Modify: `frontend/apps/allin-ssl/src/types/dns.d.ts`
- Modify: `frontend/apps/allin-ssl/src/api/dns.ts`
- Modify: `frontend/apps/allin-ssl/src/api/dns.spec.ts`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.tsx`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.spec.ts`
- Modify: `frontend/apps/allin-ssl/src/views/dns/index.tsx`

**Interfaces:**
- Consumes: Task 3 的 `get_session` 和四个写接口。
- Produces:

```ts
export interface DNSRecordInput {
	name: string
	type: DNSRecordType
	ttl: number
	value: string
	line: string
	priority?: number
	weight?: number
	port?: number
	caaFlags?: number
	caaTag?: string
}
```

- [ ] **Step 1: 写入前端失败测试**

在 `dns.spec.ts` 对 `createDNSRecord`、`updateDNSRecord`、`deleteDNSRecord`、`setDNSRecordStatus` 建立 mock 断言。新增控制器测试，验证：

```ts
await controller.createRecord()
expect(createRecord).toHaveBeenCalledWith(expect.objectContaining({
	credentialID: 1, zone: 'example.com', csrfToken: 'csrf-a',
}))
expect(controller.snapshot.value).toEqual(returnedSnapshot)
```

覆盖编辑时预填当前记录、删除只在确认回调后调用、受保护记录 `canManageRecord(record) === false`、MX/SRV/CAA 不完整时不请求后端，以及状态切换后以返回快照替换页面数据。

- [ ] **Step 2: 运行失败测试确认旧 Preview 控制器不满足新行为**

Run: `npm --prefix frontend/apps/allin-ssl test -- --run src/api/dns.spec.ts src/views/dns/useController.spec.ts`

Expected: FAIL，因为当前 API 和控制器只会创建 DNSControl Preview。

- [ ] **Step 3: 实现最小页面状态与 API 调用**

删除 DNSControl 相关类型和网关方法。初始化时通过 `getDNSSession` 保存 CSRF Token；保留授权、Zone、完整读取、筛选、排序。创建和编辑共用一个记录表单：编辑预填 `name/type/ttl/value/line` 及专属字段，记录类型在编辑时禁用以避免不必要的类型变换。新增成功、更新成功、删除成功、启停成功时统一调用返回的完整快照替换 `snapshot.value`。

`index.tsx` 只展示“添加记录”，不展示纳管 Preview 或 DNSControl 提示。表格增加“操作”列：非保护记录显示编辑、删除和启用/停用；保护记录显示“只读”。删除使用 `NPopconfirm`。表单展示线路选择：默认线路始终可选，已有记录的线路作为可选项，用户也可填写其他有效线路名称；新记录默认 `default` 与 `ENABLE`。

- [ ] **Step 4: 运行前端定向测试与构建**

Run: `npm --prefix frontend/apps/allin-ssl test -- --run src/api/dns.spec.ts src/views/dns/useController.spec.ts && npm --prefix frontend/apps/allin-ssl run build`

Expected: PASS，且构建中没有 DNSControl/Preview 类型错误。

- [ ] **Step 5: 提交阶段**

```bash
git add frontend/apps/allin-ssl/src/api/dns.ts frontend/apps/allin-ssl/src/api/dns.spec.ts frontend/apps/allin-ssl/src/types/dns.d.ts frontend/apps/allin-ssl/src/views/dns
git commit -m "界面：支持 AliDNS 记录新增编辑删除与启停"
```

## Task 5: 完整验证与文档收尾

**Files:**
- Modify: `docs/development/dns-provider-integration-design.md`

**Interfaces:**
- Consumes: Tasks 1 至 4 的已提交实现。
- Produces: 标记为“已实现、待专用测试 Zone 人工验收”的中文设计记录，不改变生产 DNS。

- [ ] **Step 1: 更新验收状态**

在设计文档记录已实现的接口、受保护记录范围、自动化验证命令，以及“真实写入仅限专用测试 Zone 和最小权限 AK/SK”的人工验收前提。不得把本地 fake 测试描述为真实 DNS 验收。

- [ ] **Step 2: 运行完整后端验证**

Run: `go test ./backend/... -count=1 && go vet ./backend/...`

Expected: PASS。

- [ ] **Step 3: 运行完整前端验证**

Run: `npm --prefix frontend/apps/allin-ssl test -- --run && npm --prefix frontend/apps/allin-ssl run build`

Expected: PASS。

- [ ] **Step 4: 检查改动范围与空白**

Run: `git diff --check && ! rg -n "dnscontrol|create_record_preview|bind_zone|DNSControl" backend frontend/apps/allin-ssl/src && ! test -e docs/development/dnscontrol-integration-design.md`

Expected: 所有命令 exit `0`；已跟踪的业务源码和前端不再包含旧方案标识，旧设计文档不存在。新的直连设计文档允许说明 DNSControl 的未来可选边界。

- [ ] **Step 5: 提交阶段**

```bash
git add docs/development/dns-provider-integration-design.md
git commit -m "文档：补充 AliDNS 直连管理验收记录"
```

## 覆盖自检

- 旧 DNSControl 常规流程删除：Task 1。
- AK/SK 服务端解析、七类记录、线路与启停：Task 2。
- 严格 HTTP 表单、会话、CSRF、同源和完整快照响应：Task 3。
- 阿里云风格新增、编辑、删除、启停、筛选排序和只读提示：Task 4。
- Go、Vet、Vitest、Vite、差异检查和专用测试 Zone 验收边界：Task 5。
