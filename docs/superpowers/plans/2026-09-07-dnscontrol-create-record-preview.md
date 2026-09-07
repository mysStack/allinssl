# DNSControl 创建解析记录预览 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为已完成零差异纳管且兼容的 AliDNS Zone 提供“添加记录 → DNSControl Preview”能力，全程不写入 DNS。

**Architecture:** 前端只提交受限的结构化记录表单。后端持有 Zone 锁后重读完整快照、校验纳管状态和快照哈希、合入候选记录，再用固定 `dnscontrol v5.0.3` 执行 Preview。成功任务仅保存脱敏变更摘要和计划哈希，永不调用 `push` 或 AliDNS 写 API。

**Tech Stack:** Go、Gin、SQLite、`dnsmodel`、本地 DNSControl `v5.0.3`、Vue 3、TypeScript、Naive UI、Vitest。

**Spec:** `docs/superpowers/specs/2026-09-07-dnscontrol-create-record-preview-design.md`

## Global Constraints

- DNSControl 版本精确固定为 `v5.0.3`；AliDNS SDK 只读，业务 DNS 写执行器只能是 DNSControl。
- 本阶段只产生 `preview`，禁止新增 `push` 调用、AliDNS CRUD 写调用、确认令牌或真实 DNS 写入路径。
- 仅兼容且已经零差异纳管的 Zone 可创建记录；含停用、非默认线路或 Provider 元数据的 Zone 保持只读。
- 可创建类型仅为 `A`、`AAAA`、`CNAME`、`TXT`、`MX`、`SRV`、`CAA`；线路固定 `default`，状态固定 `ENABLE`，TTL 为 `600` 至 `86400` 且处于 Provider 限制内。
- 必须保护 `_acme-challenge`、其子名称及顶点 `NS`/`SOA`；前端不能提交 DSL、CLI 参数、凭据或自定义 `IGNORE`。
- 每一个可验证阶段完成后运行对应测试并用详细中文提交；不提交凭据、数据库、证书、日志、构建缓存或运行时产物，不自动推送。

---

### Task 1: 候选新增记录模型与完整快照合并

**Files:**
- Create: `backend/internal/dns/change.go`
- Create: `backend/internal/dns/change_test.go`
- Modify: `backend/internal/dns/adopt_types.go`

**Interfaces:**
- Consumes: `dnsmodel.Record`、`dnsmodel.Snapshot`、`dnsmodel.BuildSnapshot`。
- Produces: `CreateRecordInput`、`BuildCreateRecordCandidate(snapshot dnsmodel.Snapshot, input CreateRecordInput) (dnsmodel.Snapshot, dnsmodel.Record, error)`、`ErrInvalidChange`、`ErrProtectedRecord`、`ErrRecordConflict`。

- [ ] **Step 1: 写入候选构建失败测试**

在 `change_test.go` 以兼容 Snapshot fixture 覆盖 A 记录规范化成功、ACME 和顶点 NS 拒绝、重复记录、CNAME 冲突、Null MX 混用、TTL 越界、异常 SRV 名称和 CAA 标志越界。

```go
candidate, record, err := BuildCreateRecordCandidate(snapshot, CreateRecordInput{
    Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10",
})
if err != nil || record.Line != "default" || record.Status != "ENABLE" {
    t.Fatalf("candidate = %#v, record = %#v, err = %v", candidate, record, err)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./backend/internal/dns -run TestBuildCreateRecordCandidate -count=1`

Expected: FAIL，因为候选构建 API 尚不存在。

- [ ] **Step 3: 实现最小候选构建器**

在 `change.go` 定义结构化输入，固定创建 `dnsmodel.Record{Line: "default", Status: "ENABLE"}`。拒绝保护名称；将记录附加到完整 Snapshot 并调用 `dnsmodel.BuildSnapshot`，将候选不兼容、记录数不增加一条和未找到规范化记录映射为稳定错误。

```go
func BuildCreateRecordCandidate(snapshot dnsmodel.Snapshot, input CreateRecordInput) (dnsmodel.Snapshot, dnsmodel.Record, error) {
    record := dnsmodel.Record{Name: input.Name, Type: input.Type, TTL: input.TTL, Value: input.Value, Line: "default", Status: "ENABLE"}
    if protectedCreateRecord(record) { return dnsmodel.Snapshot{}, dnsmodel.Record{}, ErrProtectedRecord }
    records := append(append([]dnsmodel.Record(nil), snapshot.Records...), record)
    return buildValidatedCandidate(snapshot, records, record)
}
```

- [ ] **Step 4: 运行候选构建测试**

Run: `go test ./backend/internal/dns -run TestBuildCreateRecordCandidate -count=1`

Expected: PASS，合法记录得到完整兼容候选，所有保护/冲突/格式用例被拒绝。

- [ ] **Step 5: 提交候选模型阶段**

Run: `go test ./backend/internal/dns ./backend/internal/dnsmodel -count=1 && git diff --check`

```bash
git add backend/internal/dns/change.go backend/internal/dns/change_test.go backend/internal/dns/adopt_types.go
git commit -m "新增 DNS 记录预览候选模型并保护完整 Zone 快照"
```

提交正文写明：仅构建候选 Snapshot；固定默认线路和启用状态；拒绝保护范围及冲突；没有 DNS 写入路径和实际验证命令。

### Task 2: 预览任务持久化、纳管校验与服务编排

**Files:**
- Modify: `backend/internal/dns/store.go`
- Modify: `backend/internal/dns/store_test.go`
- Modify: `backend/internal/dns/adopt.go`
- Modify: `backend/internal/dns/adopt_test.go`
- Modify: `backend/internal/dns/adopt_types.go`

**Interfaces:**
- Consumes: Task 1 的 `CreateRecordInput`、完整候选 Snapshot、既有 `Previewer`。
- Produces: `CreateRecordPreviewInput`、`AdoptService.StartCreateRecordPreview(context.Context, CreateRecordPreviewInput) (AdoptJob, error)`、`JobPreviewed`、`Store.CreateChangePreview`、`Store.IsAdopted`。

- [ ] **Step 1: 写入服务和持久化失败测试**

测试未纳管或授权不一致、快照漂移、成功 `previewed`、同键同请求、同键不同请求、同 Zone 锁和终态释放锁。

```go
job, err := service.StartCreateRecordPreview(context.Background(), CreateRecordPreviewInput{
    Identity: testIdentity(), CredentialID: 1, Zone: "example.com", BaseSnapshotHash: snapshot.SnapshotHash,
    Record: CreateRecordInput{Name: "api", Type: "A", TTL: 600, Value: "192.0.2.10"}, IdempotencyKey: "record-a",
})
if err != nil || job.State != JobPreviewed { t.Fatalf("job = %#v, err = %v", job, err) }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./backend/internal/dns -run 'Test(CreateRecordPreview|Store.*ChangePreview)' -count=1`

Expected: FAIL，因为变更任务、纳管查询与 `previewed` 状态尚未实现。

- [ ] **Step 3: 实现任务模型和 worker**

以可重复安全方式扩展 SQLite：job 增加 `kind`、候选记录摘要与变更摘要，旧行默认为 `adopt`；不得删除历史任务或 Zone 数据。幂等 action 固定 `create_record_preview`，与纳管预览复用 Zone 锁。纳管查询必须同时比对 Zone 和 `credential_id`。

worker 重读 Snapshot，验证哈希、兼容性与纳管状态，调用 Task 1、`GenerateArtifacts` 和 `Preview`。拒绝 Zone/Provider 不匹配、非正 corrections 或空 details；仅预期新增通过时转 `previewed`。不新增 `Push` 方法。

```go
const JobPreviewed JobState = "previewed"

func (service *AdoptService) StartCreateRecordPreview(ctx context.Context, input CreateRecordPreviewInput) (AdoptJob, error) {
    // Validate, create an idempotent locked job, then start the preview worker.
}
```

- [ ] **Step 4: 运行服务回归**

Run: `go test ./backend/internal/dns ./backend/internal/dnscontrol ./backend/internal/dnsmodel -count=1`

Expected: PASS，纳管 Preview 不回归，记录 Preview 不产生 Push 调用。

- [ ] **Step 5: 提交后端任务阶段**

Run: `go test ./backend/internal/dns ./backend/internal/dnscontrol ./backend/internal/dnsmodel -count=1 && go vet ./backend/internal/dns ./backend/internal/dnscontrol && git diff --check`

```bash
git add backend/internal/dns/store.go backend/internal/dns/store_test.go backend/internal/dns/adopt.go backend/internal/dns/adopt_test.go backend/internal/dns/adopt_types.go
git commit -m "新增 DNSControl 创建记录预览任务与纳管安全校验"
```

提交正文写明：仅 Preview、Zone 锁和幂等、重读防漂移、同授权已纳管校验、未开放 Push 与验证命令。

### Task 3: 受保护 API、路由和会话校验

**Files:**
- Modify: `backend/app/api/dns.go`
- Modify: `backend/app/api/dns_test.go`
- Modify: `backend/middleware/dns_auth.go`
- Modify: `backend/middleware/dns_auth_test.go`
- Modify: `backend/route/route.go`

**Interfaces:**
- Consumes: Task 2 的 `StartCreateRecordPreview` 与扩展 `AdoptJob`。
- Produces: `POST /v1/dns/create_record_preview`，响应 `{job_id,state}`，`get_job` 返回脱敏 kind、候选记录和变化摘要。

- [ ] **Step 1: 写入 API 安全边界失败测试**

发送合法表单并断言只返回 job ID/state；测试未知/重复字段、缺失 CSRF、跨源、未登录、以及 `line`、`status`、`metadata`、`config`、`credentials` 均拒绝。响应不得出现 secret、config、credentials、path、token、DSL 或 CLI 参数。

```go
form := url.Values{"credential_id": {"1"}, "zone": {"example.com"}, "base_snapshot_hash": {"hash-a"},
    "name": {"api"}, "type": {"A"}, "ttl": {"600"}, "value": {"192.0.2.10"},
    "idempotency_key": {"key-a"}, "csrf_token": {"csrf-a"}}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./backend/app/api ./backend/middleware ./backend/route -run Test.*CreateRecordPreview -count=1`

Expected: FAIL，因为路由、表单解析与处理器尚不存在。

- [ ] **Step 3: 实现严格表单接口**

为 `DNSAdoptService` 添加记录预览方法。单独实现 `createRecordPreviewForm`，白名单只接受指定字段；MX/SRV/CAA 使用严格十进制解析专用字段。将同源与 CSRF 保护扩展到新路由，并只返回脱敏响应。

```go
dns.POST("/create_record_preview", handler.CreateRecordPreview)
job, err := h.adopt.StartCreateRecordPreview(c.Request.Context(), dns.CreateRecordPreviewInput{
    Identity: identity, CredentialID: form.credentialID, Zone: form.zone,
    BaseSnapshotHash: form.snapshotHash, Record: form.record, IdempotencyKey: form.idempotencyKey,
})
```

- [ ] **Step 4: 运行 API 和中间件回归**

Run: `go test ./backend/app/api ./backend/middleware ./backend/route -count=1`

Expected: PASS，只有会话内、同源、CSRF 正确且字段严格的表单能创建 Preview 任务。

- [ ] **Step 5: 提交 API 阶段**

Run: `go test ./backend/app/api ./backend/middleware ./backend/route -count=1 && go vet ./backend/app/api ./backend/middleware ./backend/route && git diff --check`

```bash
git add backend/app/api/dns.go backend/app/api/dns_test.go backend/middleware/dns_auth.go backend/middleware/dns_auth_test.go backend/route/route.go
git commit -m "新增受保护的 DNS 创建记录预览接口"
```

提交正文写明：表单白名单、会话/同源/CSRF、响应脱敏、无 Push/无 AliDNS 写请求和验证命令。

### Task 4: 阿里云式前端表单与预览结果

**Files:**
- Modify: `frontend/apps/allin-ssl/src/types/dns.d.ts`
- Modify: `frontend/apps/allin-ssl/src/api/dns.ts`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.tsx`
- Modify: `frontend/apps/allin-ssl/src/views/dns/useController.spec.ts`
- Modify: `frontend/apps/allin-ssl/src/views/dns/index.tsx`

**Interfaces:**
- Consumes: Task 3 的 `create_record_preview` 请求/响应和 `get_job` 扩展字段。
- Produces: `DNSCreateRecordPreviewInput`、动态字段添加记录弹窗、`startCreateRecordPreview()`、`canCreateRecordPreview`。

- [ ] **Step 1: 写入前端控制器失败测试**

测试不兼容、未纳管或 DNSControl 不可用时入口禁用；A 表单正确序列化；MX/SRV/CAA 有专有字段；收到 `previewed` 后保留只读快照且不调用任何写接口。

```ts
await controller.startCreateRecordPreview()
expect(createRecordPreview).toHaveBeenCalledWith(expect.objectContaining({
  credentialID: 1, zone: 'example.com', baseSnapshotHash: 'snapshot-a',
  record: expect.objectContaining({ name: 'api', type: 'A', ttl: 600, value: '192.0.2.10' }),
}))
expect(controller.snapshot.value?.records).toHaveLength(0)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `pnpm --dir frontend/apps/allin-ssl vitest run src/views/dns/useController.spec.ts`

Expected: FAIL，因为记录 Preview 类型和控制器动作尚不存在。

- [ ] **Step 3: 实现类型、网关和控制器**

增加记录专用字段、Preview 输入、`previewed` 状态、job kind 与脱敏摘要。新增网关调用并复用 1.5 秒轮询；切换授权/Zone 或刷新快照时清空表单和记录 Preview 状态。

- [ ] **Step 4: 实现页面弹窗和结果提示**

增加“添加记录”按钮和 Naive UI 弹窗；按记录类型显示 MX/SRV/CAA 字段，固定显示“默认线路”和“启用”。按钮固定为“创建预览”；成功文案为“预览已生成，未修改 DNS，Push 尚未开放”。不添加保存、立即生效、确认写入或 Push 控件。

```tsx
<NButton type="primary" disabled={!controller.canCreateRecordPreview.value} onClick={controller.openCreateRecordForm}>
  添加记录
</NButton>
<NButton type="primary" loading={controller.createPreviewLoading.value} onClick={controller.startCreateRecordPreview}>
  创建预览
</NButton>
```

- [ ] **Step 5: 运行前端测试和构建**

Run: `pnpm --dir frontend/apps/allin-ssl vitest run src/views/dns/useController.spec.ts && pnpm --dir frontend/apps/allin-ssl build`

Expected: PASS，控制器测试通过且 Vite 生成静态资源。若全量 `vue-tsc` 仍被既有别名/依赖问题阻断，仅记录原始错误，不修复无关问题。

- [ ] **Step 6: 提交前端阶段**

Run: `pnpm --dir frontend/apps/allin-ssl vitest run src/views/dns/useController.spec.ts && pnpm --dir frontend/apps/allin-ssl build && git diff --check`

```bash
git add frontend/apps/allin-ssl/src/types/dns.d.ts frontend/apps/allin-ssl/src/api/dns.ts frontend/apps/allin-ssl/src/views/dns/useController.tsx frontend/apps/allin-ssl/src/views/dns/useController.spec.ts frontend/apps/allin-ssl/src/views/dns/index.tsx static/build
git commit -m "新增阿里云式 DNS 添加记录预览页面"
```

提交正文写明：仅兼容且已纳管 Zone 可用、动态字段、只创建 DNSControl Preview、没有 Push/真实写入及验证命令。提交前检查 `static/build` 不含数据库、日志或临时文件。

### Task 5: 文档同步、端到端回归与阶段验收

**Files:**
- Modify: `docs/development/dnscontrol-integration-design.md`
- Modify: `docs/development/dnscontrol-integration-analysis.md`
- Modify: `docs/superpowers/specs/2026-09-07-dnscontrol-create-record-preview-design.md`

**Interfaces:**
- Consumes: Tasks 1–4 的服务、接口和界面。
- Produces: 中文实施状态、已验证范围与未开放 Push 的明确记录。

- [ ] **Step 1: 写入中文实施状态**

在设计文档新增“创建记录 Preview”小节，记录入口条件、支持类型、默认线路/启用、完整 Snapshot 合并、保护规则、API 名称、状态和“未调用 Push”。在分析文档标记本阶段已实现范围与未来 Push 仍需专用 Zone 验收的边界。

- [ ] **Step 2: 运行后端集成回归**

Run: `go test ./backend/internal/dns ./backend/internal/dnscontrol ./backend/internal/dnsmodel ./backend/app/api ./backend/middleware ./backend/route -count=1`

Expected: PASS，记录 Preview、原纳管 Preview、会话与路由测试全部通过。

- [ ] **Step 3: 运行静态与产物检查**

Run: `go vet ./backend/internal/dns ./backend/internal/dnscontrol ./backend/app/api ./backend/middleware ./backend/route && git diff --check && rg -n 'dnscontrol.*push|\\.Push\\(' backend/internal/dns backend/app/api backend/route`

Expected: vet 和 diff 通过；搜索结果不得显示本阶段新增的 Push 执行路径。

- [ ] **Step 4: 审核阶段文件**

Run: `git status --short && git diff --name-only`

Expected: 仅提交源代码、测试、中文文档和必要前端产物；不提交 `data/`、凭据、证书、日志、`node_modules/`、`*.tsbuildinfo`、本地二进制或 DNSControl 工作目录。

- [ ] **Step 5: 提交文档与验收阶段**

Run: `git diff --check`

```bash
git add docs/development/dnscontrol-integration-design.md docs/development/dnscontrol-integration-analysis.md docs/superpowers/specs/2026-09-07-dnscontrol-create-record-preview-design.md docs/superpowers/plans/2026-09-07-dnscontrol-create-record-preview.md AGENTS.md
git commit -m "完善 DNSControl 创建解析记录预览中文设计与验收记录"
```

提交正文写明：Preview 范围、未开放 DNS 写入、实际通过的测试/构建/检查、未验证的真实 Provider 边界；不推送。
