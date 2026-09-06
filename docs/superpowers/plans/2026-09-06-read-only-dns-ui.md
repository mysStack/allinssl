# 只读 DNS 管理页面实施计划

> **供自动化开发执行者使用：** 必须使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 逐项执行。步骤使用复选框追踪。

**目标：** 增加可见、受 Session 鉴权保护的 AliDNS 只读管理页面。管理员可选择已有阿里云授权、加载其 Zone，并查看稳定的完整记录快照。

**架构：** 新增的 `/v1/dns` Handler 根据授权 ID 组装现有的只读 AliDNS Reader，仅返回授权摘要、Zone 摘要与 `dnsmodel.Snapshot`。新的 `/dns` 前端页面调用这些 API，不提供任何修改控件，也不会调用 DNSControl 或 AliDNS 写操作。

**技术栈：** Go、Gin、SQLite、AliDNS 生成客户端 v4.7.0、Vue 3 TSX、Naive UI、pnpm/Vite。

**设计依据：** `docs/development/dnscontrol-integration-design.md`

## 全局约束

- 仅接受既有的 `access.type = "aliyun"` 授权；Access Key 不得出现在响应、日志、浏览器状态或审计 DTO 中。
- 此增量保持 DNS 写入关闭：不新增记录增删改接口、不调用 DNSControl、不增加迁移、任务、Preview 或 Apply。
- AliDNS 读取复用 `backend/internal/dns/reader_alidns.go` 中已有的分页与连续两次快照一致性校验。
- DNS 请求沿用现有 Session 鉴权与 API 响应信封。
- Reader 失败、不支持的记录、不完整响应或远端漂移必须作为读取失败返回，不能降级为部分快照。

---

### 任务 1：组装只读 DNS Service

**文件：**
- 新建：`backend/internal/dns/service.go`
- 新建：`backend/internal/dns/service_test.go`
- 修改：`backend/internal/dns/credentials.go`

**接口：**
- 使用：`SQLCredentialStore.List`、`SQLCredentialStore.Resolve`、`NewAliDNSReader`、`AliDNSAPI`。
- 提供：`Service.ListCredentials(context.Context) ([]CredentialSummary, error)`、`Service.ListZones(context.Context, int64) ([]ZoneSummary, error)`、`Service.ReadZone(context.Context, int64, string) (dnsmodel.Snapshot, error)`。

- [ ] **步骤 1：编写失败的 Service 测试。** 验证授权摘要不含密钥、未知授权会在创建 Reader 前被拒绝、Zone 与快照读取只把已解析的授权交给新 Reader。
- [ ] **步骤 2：运行 `go test ./backend/internal/dns -run 'TestService' -count=1`，确认因 Service 尚不存在而编译失败。**
- [ ] **步骤 3：实现私有授权存储接口、Reader 工厂、AliDNS SDK 适配器以及最小 `Service` 方法。**
- [ ] **步骤 4：运行 `go test ./backend/internal/dns -count=1`，确认通过。**

### 任务 2：提供仅 Session 可访问的 DNS 读取 API

**文件：**
- 新建：`backend/app/api/dns.go`
- 新建：`backend/app/api/dns_test.go`
- 修改：`backend/route/route.go`

**接口：**
- 使用：任务 1 的 `dns.Service` 与 `public.SuccessData` / `public.FailMsg`。
- 提供：`POST /v1/dns/get_credentials`、`POST /v1/dns/get_zones`、`POST /v1/dns/get_snapshot`。

- [ ] **步骤 1：编写失败的 Handler 测试。** 覆盖正常路径、缺少 `credential_id`、缺少 `zone` 与 Reader 错误。
- [ ] **步骤 2：运行 `go test ./backend/app/api -run 'TestDNS' -count=1`，确认因 Handler 尚不存在而编译失败。**
- [ ] **步骤 3：实现表单校验与 Handler。** 对正整数授权 ID 和非空 Zone 进行校验；内部错误映射为稳定的读取失败消息，绝不返回 SDK 原始错误；仅在 `v1.Group("/dns")` 下注册 POST 路由。
- [ ] **步骤 4：运行 `go test ./backend/app/api -run 'TestDNS' -count=1`，确认通过。**

### 任务 3：新增 DNS 管理页面

**文件：**
- 新建：`frontend/apps/allin-ssl/src/api/dns.ts`
- 新建：`frontend/apps/allin-ssl/src/types/dns.d.ts`
- 新建：`frontend/apps/allin-ssl/src/views/dns/index.tsx`
- 新建：`frontend/apps/allin-ssl/src/views/dns/useController.tsx`
- 新建：`frontend/apps/allin-ssl/src/views/dns/useController.test.ts`
- 修改：`frontend/apps/allin-ssl/src/api/index.ts`
- 修改：`frontend/apps/allin-ssl/src/config/route.tsx`

**接口：**
- 使用：任务 2 的 API 响应信封，以及现有 `useApi`、`BaseLayout`、`NSelect`、`NDataTable`、`NButton` 与路由自动导入约定。
- 提供：侧栏 `dns` 路由，以及加载授权摘要、Zone 和选中 Zone 快照的页面。

- [ ] **步骤 1：编写失败的 Controller 测试。** 断言在授权和 Zone 都已选择前不会发起快照请求。
- [ ] **步骤 2：运行 `corepack pnpm --filter allin-ssl exec vitest run src/views/dns/useController.test.ts`，确认因 Controller 不存在而失败。**
- [ ] **步骤 3：实现类型化 API Client、Controller 与 TSX 页面。** 展示授权名称、Zone、快照 hash/读取时间、Provider 记录 ID、名称、类型、值、TTL、线路和状态；明确提示只读，且不渲染任何修改控件。
- [ ] **步骤 4：运行目标 Vitest 命令与 `corepack pnpm --filter allin-ssl exec vue-tsc --noEmit -p tsconfig.app.json`，二者均应通过。**

### 任务 4：构建并验证页面

**文件：**
- 修改：`static/build/index.html`
- 修改：`static/build/static/js/*`
- 修改：`docs/development/dnscontrol-integration-design.md`

**接口：**
- 使用：任务 3 的前端页面和 `static` 的 Go 嵌入资源。
- 提供：包含 DNS 路由的 `static/build` 产物，以及标注 R1 UI 可用状态的设计文档。

- [ ] **步骤 1：构建所需 workspace 包，接着运行 `corepack pnpm --filter allin-ssl build`，并将 `dist/` 复制到 `static/build/`。**
- [ ] **步骤 2：恢复受跟踪的静态图片资源，将 `./cmd/main.go` 构建到 `/tmp/allinssl-dns-check`，再运行 `go test ./backend/internal/dns ./backend/app/api -count=1` 与 `git diff --check`。**
- [ ] **步骤 3：确认构建后的路由可访问，且本增量不包含写接口或 DNSControl 调用。**
- [ ] **步骤 4：仅提交完成的源码、文档与静态构建产物，提交说明为 `feat: add read-only DNS management page`。**
