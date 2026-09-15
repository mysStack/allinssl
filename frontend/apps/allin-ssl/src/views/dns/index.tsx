import { NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSpin, NTag, type DataTableColumns } from 'naive-ui'

import BaseLayout from '@components/BaseLayout'

import type { DNSRecord, DNSRecordInput, DNSRecordSetValue } from '@/types/dns'

import type { DNSRecordSortKey } from './useController'

import { useController } from './useController'

const recordTypeOptions = [
	{ label: 'A - 将域名指向一个 IPv4 地址', value: 'A' },
	{ label: 'AAAA - 将域名指向一个 IPv6 地址', value: 'AAAA' },
	{ label: 'CNAME - 将域名指向另一个域名', value: 'CNAME' },
	{ label: 'TXT - 文本记录', value: 'TXT' },
	{ label: 'MX - 邮件服务器记录', value: 'MX' },
	{ label: 'SRV - 服务定位记录', value: 'SRV' },
	{ label: 'CAA - 证书颁发机构限制', value: 'CAA' },
]
const loadBalancingPolicyOptions = [{ label: '轮询', value: 'round_robin' }, { label: '权重', value: 'weight' }]
const statusOptions = [{ label: '启用', value: 'ENABLE' }, { label: '停用', value: 'DISABLE' }]

const recordValuePlaceholder = (type: DNSRecordInput['type']) => {
	switch (type) {
	case 'A': return '例如 203.0.113.10'
	case 'AAAA': return '例如 2001:db8::10'
	case 'CNAME': return '例如 target.example.com.'
	case 'MX': return '例如 mail.example.com.'
	case 'SRV': return '例如 service.example.com.'
	case 'CAA': return '例如 letsencrypt.org'
	default: return '请输入记录值'
	}
}

const recordPolicy = (record: DNSRecord) => record.load_balancing_policy === 'weight' ? '权重' : ['A', 'AAAA', 'CNAME'].includes(record.type) ? '轮询' : '-'
const recordWeight = (record: DNSRecord) => ['A', 'AAAA', 'CNAME'].includes(record.type) ? record.load_balancing_policy === 'weight' ? record.load_balancing_weight ?? '-' : '-' : record.type === 'SRV' ? record.weight ?? '-' : '-'
const supportsLoadBalancing = (type: string) => type === 'A' || type === 'AAAA'

export default defineComponent({
	name: 'DNS',
	setup() {
		const controller = useController()
		const columns: DataTableColumns<DNSRecord> = [
			{ title: '主机记录', key: 'name', width: 160, ellipsis: { tooltip: true }, sorter: true },
			{ title: '类型', key: 'type', width: 90, sorter: true },
			{ title: '解析请求来源', key: 'line', width: 130, ellipsis: { tooltip: true }, sorter: true },
			{ title: '记录值', key: 'value', minWidth: 240, ellipsis: { tooltip: true }, sorter: true },
			{ title: '负载策略', key: 'load_balancing_policy', width: 110, render: recordPolicy },
			{ title: '权重', key: 'load_balancing_weight', width: 80, render: recordWeight },
			{ title: 'TTL', key: 'ttl', width: 90, sorter: true },
			{ title: '状态', key: 'status', width: 90, sorter: true, render: (record) => <NTag type={record.status === 'ENABLE' ? 'success' : 'warning'}>{record.status === 'ENABLE' ? '启用' : '停用'}</NTag> },
			{ title: '备注', key: 'remark', width: 140, ellipsis: { tooltip: true }, render: (record) => record.remark || '-' },
			{ title: '记录 ID', key: 'provider_record_id', width: 150, ellipsis: { tooltip: true }, sorter: true },
			{
				title: '操作', key: 'actions', width: 190, fixed: 'right', render: (record) => controller.canManageRecord(record) ? (
					<NSpace>
						<NButton text type="primary" disabled={controller.mutationLoading.value} onClick={() => controller.openEditRecordForm(record)}>编辑记录集</NButton>
						<NButton text disabled={controller.mutationLoading.value} onClick={() => controller.toggleRecordStatus(record)}>{record.status === 'ENABLE' ? '停用' : '启用'}</NButton>
						<NPopconfirm onPositiveClick={() => controller.deleteRecord(record)}>
							{{ trigger: () => <NButton text type="error" disabled={controller.mutationLoading.value}>删除</NButton>, default: () => '确认删除这条 DNS 记录吗？' }}
						</NPopconfirm>
					</NSpace>
				) : <NTag>只读</NTag>,
			},
		]

		onMounted(async () => {
			await controller.refreshCredentials()
			await controller.refreshSession()
		})

		const updateValue = (value: DNSRecordSetValue, field: keyof DNSRecordSetValue, next: string | number | null) => {
			;(value as Record<string, unknown>)[field] = next ?? undefined
		}

		return () => (
			<div class="h-full flex flex-col">
				<div class="mx-auto max-w-[1600px] w-full p-6">
					<BaseLayout
						v-slots={{
							headerLeft: () => <h2 class="text-xl font-semibold">DNS 解析管理</h2>,
							headerRight: () => (
								<NSpace>
									<NSelect value={controller.credentialID.value} options={controller.credentialOptions.value} placeholder="选择阿里云授权" style={{ width: '220px' }} onUpdateValue={async (value) => { controller.credentialID.value = value; await controller.refreshZones() }} />
									<NSelect value={controller.zone.value} options={controller.zoneOptions.value} disabled={!controller.credentialID.value} placeholder="选择 Zone" style={{ width: '240px' }} onUpdateValue={controller.setZone} />
									<NButton type="primary" disabled={!controller.zone.value} loading={controller.loading.value} onClick={controller.refreshSnapshot}>读取记录</NButton>
									<NButton type="primary" secondary disabled={!controller.snapshot.value || controller.mutationLoading.value} onClick={controller.openCreateRecordForm}>添加记录</NButton>
									<NButton secondary loading={controller.loading.value} onClick={controller.refreshCredentials}>刷新授权</NButton>
								</NSpace>
							),
							content: () => (
								<NSpace vertical size="large">
									{controller.error.value ? <NAlert type="error">{controller.error.value}</NAlert> : null}
									{controller.snapshot.value ? (
										<NSpace vertical size="medium">
											<div class="text-sm text-gray-500">快照：{controller.snapshot.value.snapshot_hash}</div>
											<NSpace wrap>
												<NInput value={controller.recordFilters.value.name} placeholder="主机记录" style={{ width: '160px' }} onUpdateValue={(value) => (controller.recordFilters.value.name = value)} />
												<NInput value={controller.recordFilters.value.value} placeholder="记录值" style={{ width: '220px' }} onUpdateValue={(value) => (controller.recordFilters.value.value = value)} />
												<NSelect value={controller.recordFilters.value.type || null} clearable options={[...new Set(controller.snapshot.value.records.map((record) => record.type))].sort().map((value) => ({ label: value, value }))} placeholder="记录类型" style={{ width: '140px' }} onUpdateValue={(value) => (controller.recordFilters.value.type = value ?? '')} />
												<NSelect value={controller.recordFilters.value.line || null} clearable options={[...new Set(controller.snapshot.value.records.map((record) => record.line))].sort().map((value) => ({ label: value, value }))} placeholder="解析线路" style={{ width: '150px' }} onUpdateValue={(value) => (controller.recordFilters.value.line = value ?? '')} />
												<NSelect value={controller.recordFilters.value.status || null} clearable options={statusOptions} placeholder="状态" style={{ width: '120px' }} onUpdateValue={(value) => (controller.recordFilters.value.status = value ?? '')} />
												<NButton secondary onClick={() => (controller.recordFilters.value = { name: '', type: '', line: '', status: '', value: '' })}>重置筛选</NButton>
											</NSpace>
											<NDataTable remote columns={columns} data={controller.visibleRecords.value} scrollX={1600} onUpdateSorter={(sorter: { columnKey?: string | number; order?: 'ascend' | 'descend' | false }) => { if (typeof sorter.columnKey === 'string' && sorter.order) controller.setRecordSort(sorter.columnKey as DNSRecordSortKey, sorter.order) }} />
										</NSpace>
									) : (
										<NSpin show={controller.loading.value}><div class="py-16 text-center text-gray-500">请选择授权和 Zone 后读取记录。</div></NSpin>
									)}
								</NSpace>
							),
						}}
					/>
					<NModal show={controller.recordModalVisible.value} onUpdateShow={(show) => (controller.recordModalVisible.value = show)}>
						<NCard title={controller.editingRecordID.value ? '编辑记录集' : '添加记录'} closable style={{ width: '920px', maxWidth: 'calc(100vw - 32px)' }} onClose={() => (controller.recordModalVisible.value = false)}>
							<NForm labelPlacement="top">
								{controller.editingRecordID.value && controller.recordForm.value.values.length > 1 ? <NAlert type="info" showIcon class="mb-4">当前记录集包含多个记录值。为避免 CNAME 冲突，记录类型不能直接切换；请先将记录集缩减为一条记录。</NAlert> : null}
								<NFormItem label="记录类型"><NSelect value={controller.recordForm.value.type} options={recordTypeOptions} disabled={Boolean(controller.editingRecordID.value && controller.recordForm.value.values.length > 1)} onUpdateValue={controller.setRecordType} /></NFormItem>
								<NFormItem label="主机记录"><NInput value={controller.recordForm.value.name} placeholder={controller.zone.value ? `例如 www（${controller.zone.value}）` : '例如 www'} onUpdateValue={(value) => (controller.recordForm.value.name = value)} /></NFormItem>
								<NFormItem label="解析请求来源"><NSelect value={controller.recordForm.value.line} options={[{ label: '默认 / 默认', value: 'default' }]} onUpdateValue={(value) => (controller.recordForm.value.line = value)} /></NFormItem>
								<NSpace class="w-full" size="large">
									<NFormItem label="TTL 时间" class="w-48"><NInputNumber value={controller.recordForm.value.ttl} min={600} max={86400} step={60} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.ttl = value ?? 600)} /></NFormItem>
									{supportsLoadBalancing(controller.recordForm.value.type) ? <NFormItem label="记录值负载策略" class="w-48"><NSelect value={controller.recordForm.value.loadBalancingPolicy} options={loadBalancingPolicyOptions} onUpdateValue={(value) => (controller.recordForm.value.loadBalancingPolicy = value as DNSRecordInput['loadBalancingPolicy'])} /></NFormItem> : null}
								</NSpace>
								{controller.recordForm.value.type === 'CNAME' ? <NAlert type="warning" showIcon class="mb-4">AliDNS 返回的 CNAME 负载状态会展示在列表中；当前 SDK 只支持 A/AAAA 修改负载策略。</NAlert> : null}
								<NCard title="记录值集合" size="small" embedded>
									<div class="grid grid-cols-[minmax(0,2fr)_110px_100px_180px_64px] gap-2 mb-2 text-sm text-gray-500"><div>记录值</div><div>权重</div><div>状态</div><div>备注</div><div>操作</div></div>
									{controller.recordForm.value.values.map((value, index) => <div key={value.recordID ?? `new-${index}`} class="mb-3 rounded border border-gray-200 p-3">
										<div class="grid grid-cols-[minmax(0,2fr)_110px_100px_180px_64px] gap-2 items-center">
											<NInput value={value.value} placeholder={recordValuePlaceholder(controller.recordForm.value.type)} onUpdateValue={(next) => updateValue(value, 'value', next)} />
											{supportsLoadBalancing(controller.recordForm.value.type) && controller.recordForm.value.loadBalancingPolicy === 'weight' ? <NInputNumber value={value.loadBalancingWeight} min={1} max={100} onUpdateValue={(next) => updateValue(value, 'loadBalancingWeight', next)} /> : <div class="text-center text-gray-400">-</div>}
											<NSelect value={value.status} options={statusOptions} onUpdateValue={(next) => updateValue(value, 'status', next)} />
											<NInput value={value.remark} maxlength={50} showCount placeholder="备注（可选）" onUpdateValue={(next) => updateValue(value, 'remark', next)} />
											<NButton text type="error" disabled={controller.recordForm.value.values.length === 1} onClick={() => controller.removeRecordValue(index)}>删除</NButton>
										</div>
										{controller.recordForm.value.type === 'MX' ? <NSpace class="mt-3"><span class="text-sm text-gray-500">优先级</span><NInputNumber value={value.priority} min={0} max={65535} onUpdateValue={(next) => updateValue(value, 'priority', next)} /></NSpace> : null}
										{controller.recordForm.value.type === 'SRV' ? <NSpace class="mt-3"><span class="text-sm text-gray-500">优先级</span><NInputNumber value={value.priority} min={0} max={65535} onUpdateValue={(next) => updateValue(value, 'priority', next)} /><span class="text-sm text-gray-500">SRV 权重</span><NInputNumber value={value.weight} min={0} max={65535} onUpdateValue={(next) => updateValue(value, 'weight', next)} /><span class="text-sm text-gray-500">端口</span><NInputNumber value={value.port} min={1} max={65535} onUpdateValue={(next) => updateValue(value, 'port', next)} /></NSpace> : null}
										{controller.recordForm.value.type === 'CAA' ? <NSpace class="mt-3"><span class="text-sm text-gray-500">Flags</span><NInputNumber value={value.caaFlags} min={0} max={255} onUpdateValue={(next) => updateValue(value, 'caaFlags', next)} /><span class="text-sm text-gray-500">Tag</span><NInput value={value.caaTag} placeholder="issue" style={{ width: '180px' }} onUpdateValue={(next) => updateValue(value, 'caaTag', next)} /></NSpace> : null}
									</div>)}
									<NButton text type="primary" onClick={controller.addRecordValue}>+ 添加条目</NButton>
								</NCard>
								<NAlert type="info" showIcon class="mt-4">记录集保存会复用 AliDNS 的单记录 API 逐条执行。若其中一条失败，页面会自动重新读取该 Zone 并提示确认实际状态。</NAlert>
								<div class="mt-5 flex justify-end gap-3"><NButton onClick={() => (controller.recordModalVisible.value = false)}>取消</NButton><NButton type="primary" loading={controller.mutationLoading.value} onClick={controller.saveRecord}>保存记录集</NButton></div>
							</NForm>
						</NCard>
					</NModal>
				</div>
			</div>
		)
	},
})
