import { NAlert, NButton, NDataTable, NFormItem, NInput, NInputNumber, NModal, NPopconfirm, NSelect, NSpace, NSpin, NTag, type DataTableColumns } from 'naive-ui'

import BaseLayout from '@components/BaseLayout'

import type { DNSRecord, DNSRecordType } from '@/types/dns'

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

		onMounted(async () => {
			await controller.refreshCredentials()
			await controller.refreshHealth()
		})
		onUnmounted(controller.stopPolling)

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
										onUpdateValue={controller.setZone}
									/>
									<NButton type="primary" disabled={!controller.zone.value} loading={controller.loading.value} onClick={controller.refreshSnapshot}>
										读取记录
									</NButton>
									<NPopconfirm
										positiveText="开始预览"
										negativeText="取消"
										onPositiveClick={controller.startAdoptPreview}
										v-slots={{
											trigger: () => (
												<NButton type="primary" secondary disabled={!controller.canStartAdoptPreview.value} loading={controller.adoptLoading.value}>
													纳管预览
												</NButton>
											),
										}}
									>
										确认以当前完整快照创建 DNSControl 零差异纳管预览。此操作不会修改 DNS 记录，也不会执行 push。
									</NPopconfirm>
									<NButton type="primary" disabled={!controller.canCreateRecordPreview.value} onClick={controller.openCreateRecordForm}>
										添加记录
									</NButton>
									<NButton secondary loading={controller.loading.value} onClick={controller.refreshCredentials}>
										刷新授权
									</NButton>
								</NSpace>
							),
							content: () => (
								<NSpace vertical size="large">
									<NAlert type="info" title="读取与纳管预览模式">
										当前页面只读取并展示 DNS 解析记录。纳管预览仅验证 DNSControl 零差异接管，不会修改记录或执行 push。
									</NAlert>
									{controller.error.value ? <NAlert type="error">{controller.error.value}</NAlert> : null}
									{controller.health.value && !controller.health.value.preview_available ? (
										<NAlert type="warning" title="DNSControl 预览不可用">
											当前 DNSControl 状态不支持预览；仍可继续只读查看记录。
										</NAlert>
									) : null}
									{controller.snapshot.value ? (
										<NSpace vertical size="medium">
											<div class="text-sm text-gray-500">快照：{controller.snapshot.value.snapshot_hash}</div>
											{controller.snapshot.value.compatible ? (
												<NAlert type="info" title="可以创建纳管预览">
													{controller.health.value ? `DNSControl ${controller.health.value.version}` : '正在读取 DNSControl 状态'}。确认后只创建并轮询预览任务，不会执行 DNS 写入。
												</NAlert>
											) : null}
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
											{controller.adoptJob.value ? (
												<NAlert type={controller.adoptJob.value.state === 'adopted' ? 'success' : controller.adoptJob.value.state === 'blocked' || controller.adoptJob.value.state === 'failed' ? 'error' : 'info'} title="DNSControl 纳管预览任务">
													任务：{controller.adoptJob.value.id}；状态：{controller.adoptJob.value.state}；Zone：{controller.adoptJob.value.zone}
													{controller.adoptJob.value.error_code ? `；原因：${controller.adoptJob.value.error_code}` : ''}
												</NAlert>
											) : null}
											{controller.createPreviewJob.value ? (
												<NAlert
													type={controller.createPreviewJob.value.state === 'previewed' ? 'success' : controller.createPreviewJob.value.state === 'blocked' || controller.createPreviewJob.value.state === 'failed' ? 'error' : 'info'}
													title="DNS 记录创建预览任务"
												>
													任务：{controller.createPreviewJob.value.id}；状态：{controller.createPreviewJob.value.state}
													{controller.createPreviewJob.value.error_code ? `；原因：${controller.createPreviewJob.value.error_code}` : ''}
													{controller.createPreviewJob.value.state === 'previewed' ? '；预览已生成，未修改 DNS，Push 尚未开放' : ''}
												</NAlert>
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
				<NModal
					show={controller.createRecordModalVisible.value}
					preset="card"
					title="添加 DNS 记录（Preview）"
					style={{ width: '640px' }}
					onUpdateShow={(show) => (controller.createRecordModalVisible.value = show)}
				>
					<NSpace vertical size="medium">
						<NAlert type="info">仅生成 DNSControl 变更预览，不会修改 DNS，也不会执行 Push。</NAlert>
						<NFormItem label="记录类型" required>
							<NSelect
								value={controller.createRecordForm.value.type}
								options={['A', 'AAAA', 'CNAME', 'TXT', 'MX', 'SRV', 'CAA'].map((value) => ({ label: value, value }))}
								onUpdateValue={(value) => (controller.createRecordForm.value = { name: '', type: value as DNSRecordType, ttl: 600, value: '' })}
							/>
						</NFormItem>
						<NFormItem label="主机记录" required>
							<NInput value={controller.createRecordForm.value.name} placeholder="例如 api 或 _sip._tcp" onUpdateValue={(value) => (controller.createRecordForm.value.name = value)} />
						</NFormItem>
						<NFormItem label="记录值" required>
							<NInput value={controller.createRecordForm.value.value} placeholder="请输入记录值" onUpdateValue={(value) => (controller.createRecordForm.value.value = value)} />
						</NFormItem>
						<NFormItem label="TTL" required>
							<NInputNumber value={controller.createRecordForm.value.ttl} min={600} max={86400} precision={0} onUpdateValue={(value) => (controller.createRecordForm.value.ttl = value ?? 600)} />
						</NFormItem>
						{controller.createRecordForm.value.type === 'MX' || controller.createRecordForm.value.type === 'SRV' ? (
							<NFormItem label="优先级" required>
								<NInputNumber value={controller.createRecordForm.value.priority} min={0} max={65535} precision={0} onUpdateValue={(value) => (controller.createRecordForm.value.priority = value ?? undefined)} />
							</NFormItem>
						) : null}
						{controller.createRecordForm.value.type === 'SRV' ? (
							<NSpace>
								<NFormItem label="权重" required>
									<NInputNumber value={controller.createRecordForm.value.weight} min={0} max={65535} precision={0} onUpdateValue={(value) => (controller.createRecordForm.value.weight = value ?? undefined)} />
								</NFormItem>
								<NFormItem label="端口" required>
									<NInputNumber value={controller.createRecordForm.value.port} min={0} max={65535} precision={0} onUpdateValue={(value) => (controller.createRecordForm.value.port = value ?? undefined)} />
								</NFormItem>
							</NSpace>
						) : null}
						{controller.createRecordForm.value.type === 'CAA' ? (
							<NSpace>
								<NFormItem label="标志位" required>
									<NInputNumber value={controller.createRecordForm.value.caaFlags} min={0} max={255} precision={0} onUpdateValue={(value) => (controller.createRecordForm.value.caaFlags = value ?? undefined)} />
								</NFormItem>
								<NFormItem label="标签" required>
									<NInput value={controller.createRecordForm.value.caaTag} placeholder="例如 issue" onUpdateValue={(value) => (controller.createRecordForm.value.caaTag = value)} />
								</NFormItem>
							</NSpace>
						) : null}
						<NSpace>
							<NTag>线路：default（默认线路）</NTag>
							<NTag type="success">状态：ENABLE（启用）</NTag>
						</NSpace>
						<NSpace justify="end">
							<NButton onClick={() => (controller.createRecordModalVisible.value = false)}>取消</NButton>
							<NButton type="primary" loading={controller.createPreviewLoading.value} onClick={controller.startCreateRecordPreview}>
								创建预览
							</NButton>
						</NSpace>
					</NSpace>
				</NModal>
			</div>
		)
	},
})
