import { NAlert, NButton, NDataTable, NInput, NSelect, NSpace, NSpin, NTag, type DataTableColumns } from 'naive-ui'

import BaseLayout from '@components/BaseLayout'

import type { DNSRecord } from '@/types/dns'

import type { DNSRecordSortKey } from './useController'

import { useController } from './useController'

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
			{
				title: '状态',
				key: 'status',
				width: 100,
				sorter: true,
				render: (record) => <NTag type={record.status === 'ENABLE' ? 'success' : 'warning'}>{record.status}</NTag>,
			},
			{ title: '记录 ID', key: 'provider_record_id', width: 160, ellipsis: { tooltip: true }, sorter: true },
		]

		onMounted(controller.refreshCredentials)

		return () => (
			<div class="h-full flex flex-col">
				<div class="mx-auto max-w-[1600px] w-full p-6">
					<BaseLayout
						v-slots={{
							headerLeft: () => <h2 class="text-xl font-semibold">DNS 解析管理</h2>,
							headerRight: () => (
								<NSpace>
									<NSelect
										value={controller.credentialID.value}
										options={controller.credentialOptions.value}
										placeholder="选择阿里云授权"
										style={{ width: '220px' }}
										onUpdateValue={async (value) => {
											controller.credentialID.value = value
											await controller.refreshZones()
										}}
									/>
									<NSelect
										value={controller.zone.value}
										options={controller.zoneOptions.value}
										disabled={!controller.credentialID.value}
										placeholder="选择 Zone"
										style={{ width: '240px' }}
										onUpdateValue={(value) => (controller.zone.value = value)}
									/>
									<NButton type="primary" disabled={!controller.zone.value} loading={controller.loading.value} onClick={controller.refreshSnapshot}>
										读取记录
									</NButton>
									<NButton secondary loading={controller.loading.value} onClick={controller.refreshCredentials}>
										刷新授权
									</NButton>
								</NSpace>
							),
							content: () => (
								<NSpace vertical size="large">
									<NAlert type="info" title="只读模式">
										当前页面只读取并展示 DNS 解析记录，不会修改任何记录。DNS 写入将在 DNSControl 的 Preview/确认阶段另行开放。
									</NAlert>
									{controller.error.value ? <NAlert type="error">{controller.error.value}</NAlert> : null}
									{controller.snapshot.value ? (
										<NSpace vertical size="medium">
											<div class="text-sm text-gray-500">快照：{controller.snapshot.value.snapshot_hash}</div>
											<NSpace wrap>
												<NInput
													value={controller.recordFilters.value.name}
													placeholder="主机记录"
													style={{ width: '160px' }}
													onUpdateValue={(value) => (controller.recordFilters.value.name = value)}
												/>
												<NInput
													value={controller.recordFilters.value.value}
													placeholder="记录值"
													style={{ width: '220px' }}
													onUpdateValue={(value) => (controller.recordFilters.value.value = value)}
												/>
												<NSelect
													value={controller.recordFilters.value.type || null}
													clearable
													options={[...new Set(controller.snapshot.value.records.map((record) => record.type))].sort().map((value) => ({ label: value, value }))}
													placeholder="记录类型"
													style={{ width: '140px' }}
													onUpdateValue={(value) => (controller.recordFilters.value.type = value ?? '')}
												/>
												<NSelect
													value={controller.recordFilters.value.line || null}
													clearable
													options={[...new Set(controller.snapshot.value.records.map((record) => record.line))].sort().map((value) => ({ label: value, value }))}
													placeholder="解析线路"
													style={{ width: '150px' }}
													onUpdateValue={(value) => (controller.recordFilters.value.line = value ?? '')}
												/>
												<NSelect
													value={controller.recordFilters.value.status || null}
													clearable
													options={[
														{ label: '启用', value: 'ENABLE' },
														{ label: '停用', value: 'DISABLE' },
													]}
													placeholder="状态"
													style={{ width: '120px' }}
													onUpdateValue={(value) => (controller.recordFilters.value.status = value ?? '')}
												/>
												<NButton
													secondary
													onClick={() => (controller.recordFilters.value = { name: '', type: '', line: '', status: '', value: '' })}
												>
													重置筛选
												</NButton>
											</NSpace>
											{controller.snapshot.value.read_only_reasons.length ? (
												<NAlert type="warning">该 Zone 仅可读取：{controller.snapshot.value.read_only_reasons.join('、')}</NAlert>
											) : null}
											<NDataTable
												remote
												columns={columns}
												data={controller.visibleRecords.value}
												scrollX={1100}
												onUpdateSorter={(sorter: { columnKey?: string | number; order?: 'ascend' | 'descend' | false }) => {
													if (typeof sorter.columnKey === 'string' && sorter.order) {
														controller.setRecordSort(sorter.columnKey as DNSRecordSortKey, sorter.order)
													}
												}}
											/>
										</NSpace>
									) : (
										<NSpin show={controller.loading.value}>
											<div class="py-16 text-center text-gray-500">请选择授权和 Zone 后读取记录。</div>
										</NSpin>
									)}
								</NSpace>
							),
						}}
					/>
				</div>
			</div>
		)
	},
})
