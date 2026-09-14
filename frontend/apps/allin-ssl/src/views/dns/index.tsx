import { NAlert, NButton, NCard, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSpin, NTag, type DataTableColumns } from 'naive-ui'

import BaseLayout from '@components/BaseLayout'

import type { DNSRecord, DNSRecordInput } from '@/types/dns'

import type { DNSRecordSortKey } from './useController'

import { useController } from './useController'

const recordTypeOptions = ['A', 'AAAA', 'CNAME', 'TXT', 'MX', 'SRV', 'CAA'].map((value) => ({ label: value, value }))

export default defineComponent({
	name: 'DNS',
	setup() {
		const controller = useController()
		const columns: DataTableColumns<DNSRecord> = [
			{ title: '主机记录', key: 'name', width: 180, ellipsis: { tooltip: true }, sorter: true },
			{ title: '类型', key: 'type', width: 90, sorter: true },
			{ title: '记录值', key: 'value', minWidth: 260, ellipsis: { tooltip: true }, sorter: true },
			{ title: 'TTL', key: 'ttl', width: 90, sorter: true },
			{ title: '线路', key: 'line', width: 120, ellipsis: { tooltip: true }, sorter: true },
			{ title: '状态', key: 'status', width: 100, sorter: true, render: (record) => <NTag type={record.status === 'ENABLE' ? 'success' : 'warning'}>{record.status === 'ENABLE' ? '启用' : '停用'}</NTag> },
			{ title: '记录 ID', key: 'provider_record_id', width: 160, ellipsis: { tooltip: true }, sorter: true },
			{
				title: '操作', key: 'actions', width: 190, fixed: 'right', render: (record) => controller.canManageRecord(record) ? (
					<NSpace>
						<NButton text type="primary" disabled={controller.mutationLoading.value} onClick={() => controller.openEditRecordForm(record)}>编辑</NButton>
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
												<NSelect value={controller.recordFilters.value.status || null} clearable options={[{ label: '启用', value: 'ENABLE' }, { label: '停用', value: 'DISABLE' }]} placeholder="状态" style={{ width: '120px' }} onUpdateValue={(value) => (controller.recordFilters.value.status = value ?? '')} />
												<NButton secondary onClick={() => (controller.recordFilters.value = { name: '', type: '', line: '', status: '', value: '' })}>重置筛选</NButton>
											</NSpace>
											<NDataTable remote columns={columns} data={controller.visibleRecords.value} scrollX={1300} onUpdateSorter={(sorter: { columnKey?: string | number; order?: 'ascend' | 'descend' | false }) => { if (typeof sorter.columnKey === 'string' && sorter.order) controller.setRecordSort(sorter.columnKey as DNSRecordSortKey, sorter.order) }} />
										</NSpace>
									) : (
										<NSpin show={controller.loading.value}><div class="py-16 text-center text-gray-500">请选择授权和 Zone 后读取记录。</div></NSpin>
									)}
								</NSpace>
							),
						}}
					/>
					<NModal show={controller.recordModalVisible.value} onUpdateShow={(show) => (controller.recordModalVisible.value = show)}>
						<NCard title={controller.editingRecordID.value ? '编辑 DNS 记录' : '添加 DNS 记录'} closable style={{ width: '560px' }} onClose={() => (controller.recordModalVisible.value = false)}>
							<NForm labelPlacement="left" labelWidth={90}>
								<NFormItem label="主机记录"><NInput value={controller.recordForm.value.name} placeholder="例如 www" onUpdateValue={(value) => (controller.recordForm.value.name = value)} /></NFormItem>
								<NFormItem label="记录类型"><NSelect value={controller.recordForm.value.type} options={recordTypeOptions} onUpdateValue={controller.setRecordType} /></NFormItem>
								<NFormItem label="记录值"><NInput value={controller.recordForm.value.value} placeholder="请输入记录值" onUpdateValue={(value) => (controller.recordForm.value.value = value)} /></NFormItem>
								<NFormItem label="TTL"><NInputNumber value={controller.recordForm.value.ttl} min={600} max={86400} step={60} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.ttl = value ?? 600)} /></NFormItem>
								<NFormItem label="解析线路"><NInput value={controller.recordForm.value.line} placeholder="default" onUpdateValue={(value) => (controller.recordForm.value.line = value)} /></NFormItem>
								{controller.recordForm.value.type === 'MX' || controller.recordForm.value.type === 'SRV' ? <NFormItem label="优先级"><NInputNumber value={controller.recordForm.value.priority} min={0} max={65535} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.priority = value ?? 0)} /></NFormItem> : null}
								{controller.recordForm.value.type === 'SRV' ? <><NFormItem label="权重"><NInputNumber value={controller.recordForm.value.weight} min={0} max={65535} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.weight = value ?? 0)} /></NFormItem><NFormItem label="端口"><NInputNumber value={controller.recordForm.value.port} min={1} max={65535} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.port = value ?? 1)} /></NFormItem></> : null}
								{controller.recordForm.value.type === 'CAA' ? <><NFormItem label="Flags"><NInputNumber value={controller.recordForm.value.caaFlags} min={0} max={255} class="w-full" onUpdateValue={(value) => (controller.recordForm.value.caaFlags = value ?? 0)} /></NFormItem><NFormItem label="Tag"><NInput value={controller.recordForm.value.caaTag} placeholder="issue" onUpdateValue={(value) => (controller.recordForm.value.caaTag = value)} /></NFormItem></> : null}
								<div class="flex justify-end gap-3"><NButton onClick={() => (controller.recordModalVisible.value = false)}>取消</NButton><NButton type="primary" loading={controller.mutationLoading.value} onClick={controller.saveRecord}>保存</NButton></div>
							</NForm>
						</NCard>
					</NModal>
				</div>
			</div>
		)
	},
})
