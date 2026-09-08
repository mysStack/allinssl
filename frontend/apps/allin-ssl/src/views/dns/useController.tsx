import { computed, ref } from 'vue'

import type { DNSCredential, DNSRecord, DNSRecordInput, DNSSession, DNSSnapshot, DNSZone } from '@/types/dns'

export interface DNSRecordFilters {
	name: string
	type: string
	line: string
	status: string
	value: string
}

export type DNSRecordSortKey = 'name' | 'type' | 'value' | 'ttl' | 'line' | 'status' | 'provider_record_id'
export type DNSRecordSortOrder = 'ascend' | 'descend'

export interface DNSGateway {
	getCredentials: () => Promise<DNSCredential[]>
	getZones: (credentialID: number) => Promise<DNSZone[]>
	getSnapshot: (credentialID: number, zone: string) => Promise<DNSSnapshot>
	getSession: () => Promise<DNSSession>
	createRecord: (input: { credentialID: number; zone: string; record: DNSRecordInput; csrfToken: string }) => Promise<DNSSnapshot>
	updateRecord: (input: { credentialID: number; zone: string; recordID: string; record: DNSRecordInput; csrfToken: string }) => Promise<DNSSnapshot>
	deleteRecord: (input: { credentialID: number; zone: string; recordID: string; csrfToken: string }) => Promise<DNSSnapshot>
	setRecordStatus: (input: { credentialID: number; zone: string; recordID: string; status: 'ENABLE' | 'DISABLE'; csrfToken: string }) => Promise<DNSSnapshot>
}

const emptyRecordForm = (): DNSRecordInput => ({ name: '', type: 'A', ttl: 600, value: '', line: 'default' })

export const createDNSController = (gateway: DNSGateway) => {
	const credentials = ref<DNSCredential[]>([])
	const zones = ref<DNSZone[]>([])
	const snapshot = ref<DNSSnapshot | null>(null)
	const credentialID = ref<number | null>(null)
	const zone = ref<string | null>(null)
	const loading = ref(false)
	const mutationLoading = ref(false)
	const error = ref('')
	const csrfToken = ref('')
	const recordModalVisible = ref(false)
	const editingRecordID = ref<string | null>(null)
	const recordForm = ref<DNSRecordInput>(emptyRecordForm())
	const recordFilters = ref<DNSRecordFilters>({ name: '', type: '', line: '', status: '', value: '' })
	const recordSort = ref<{ key: DNSRecordSortKey; order: DNSRecordSortOrder }>({ key: 'provider_record_id', order: 'descend' })
	const credentialOptions = computed(() => credentials.value.map((item) => ({ label: item.name, value: item.id })))
	const zoneOptions = computed(() => zones.value.map((item) => ({ label: item.name, value: item.name })))
	const visibleRecords = computed(() => filterRecords(snapshot.value?.records ?? [], recordFilters.value, recordSort.value))

	const setRecordSort = (key: DNSRecordSortKey, order: DNSRecordSortOrder) => {
		recordSort.value = { key, order }
	}

	const refreshCredentials = async () => {
		loading.value = true
		error.value = ''
		try {
			credentials.value = await gateway.getCredentials()
			if (!credentials.value.some((item) => item.id === credentialID.value)) {
				credentialID.value = null
				zones.value = []
				zone.value = null
				snapshot.value = null
			}
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 授权读取失败'
		} finally {
			loading.value = false
		}
	}

	const refreshZones = async () => {
		zones.value = []
		zone.value = null
		snapshot.value = null
		error.value = ''
		if (!credentialID.value) return
		loading.value = true
		try {
			zones.value = await gateway.getZones(credentialID.value)
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS Zone 读取失败'
		} finally {
			loading.value = false
		}
	}

	const refreshSnapshot = async () => {
		snapshot.value = null
		error.value = ''
		if (!credentialID.value || !zone.value) return
		loading.value = true
		try {
			snapshot.value = await gateway.getSnapshot(credentialID.value, zone.value)
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录读取失败'
		} finally {
			loading.value = false
		}
	}

	const refreshSession = async () => {
		try {
			csrfToken.value = (await gateway.getSession()).csrf_token
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 会话读取失败'
		}
	}

	const setZone = (value: string | null) => {
		zone.value = value
		snapshot.value = null
	}

	const canManageRecord = (record: DNSRecord) => !record.protected && record.type !== 'NS' && record.type !== 'SOA'

	const openCreateRecordForm = () => {
		error.value = ''
		editingRecordID.value = null
		recordForm.value = emptyRecordForm()
		recordModalVisible.value = true
	}

	const openEditRecordForm = (record: DNSRecord) => {
		if (!canManageRecord(record)) return
		error.value = ''
		editingRecordID.value = record.provider_record_id
		recordForm.value = {
			name: record.name,
			type: record.type as DNSRecordInput['type'],
			ttl: record.ttl,
			value: record.value,
			line: record.line,
			priority: record.priority,
			weight: record.weight,
			port: record.port,
			caaFlags: record.caa_flags,
			caaTag: record.caa_tag,
		}
		recordModalVisible.value = true
	}

	const saveRecord = async () => {
		if (!credentialID.value || !zone.value || !validRecordForm(recordForm.value)) {
			error.value = '请完整填写 DNS 记录字段'
			return
		}
		if (!csrfToken.value) await refreshSession()
		if (!csrfToken.value) return
		mutationLoading.value = true
		error.value = ''
		try {
			const input = { credentialID: credentialID.value, zone: zone.value, record: recordForm.value, csrfToken: csrfToken.value }
			snapshot.value = editingRecordID.value ? await gateway.updateRecord({ ...input, recordID: editingRecordID.value }) : await gateway.createRecord(input)
			recordModalVisible.value = false
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录保存失败，请刷新记录确认实际状态'
		} finally {
			mutationLoading.value = false
		}
	}

	const deleteRecord = async (record: DNSRecord) => {
		if (!credentialID.value || !zone.value || !csrfToken.value || !canManageRecord(record)) return
		await applyMutation(() => gateway.deleteRecord({ credentialID: credentialID.value!, zone: zone.value!, recordID: record.provider_record_id, csrfToken: csrfToken.value }))
	}

	const toggleRecordStatus = async (record: DNSRecord) => {
		if (!credentialID.value || !zone.value || !csrfToken.value || !canManageRecord(record)) return
		const status = record.status === 'ENABLE' ? 'DISABLE' : 'ENABLE'
		await applyMutation(() => gateway.setRecordStatus({ credentialID: credentialID.value!, zone: zone.value!, recordID: record.provider_record_id, status, csrfToken: csrfToken.value }))
	}

	const applyMutation = async (mutate: () => Promise<DNSSnapshot>) => {
		mutationLoading.value = true
		error.value = ''
		try {
			snapshot.value = await mutate()
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录操作失败，请刷新记录确认实际状态'
		} finally {
			mutationLoading.value = false
		}
	}

	return { credentials, zones, snapshot, credentialID, zone, loading, mutationLoading, error, credentialOptions, zoneOptions, recordFilters, visibleRecords, recordModalVisible, editingRecordID, recordForm, setRecordSort, refreshCredentials, refreshZones, refreshSnapshot, refreshSession, setZone, canManageRecord, openCreateRecordForm, openEditRecordForm, saveRecord, deleteRecord, toggleRecordStatus }
}

const validRecordForm = (record: DNSRecordInput) => {
	if (!record.name.trim() || !record.value.trim() || !record.line.trim() || record.ttl < 600 || record.ttl > 86400) return false
	if (record.type === 'MX') return record.priority !== undefined
	if (record.type === 'SRV') return record.priority !== undefined && record.weight !== undefined && record.port !== undefined
	if (record.type === 'CAA') return record.caaFlags !== undefined && Boolean(record.caaTag?.trim())
	return true
}

const filterRecords = (records: DNSRecord[], filters: DNSRecordFilters, sort: { key: DNSRecordSortKey; order: DNSRecordSortOrder }): DNSRecord[] => {
	const includes = (value: string, filter: string) => value.toLowerCase().includes(filter.trim().toLowerCase())
	return records
		.filter((record) => includes(record.name, filters.name) && includes(record.value, filters.value) && (!filters.type || record.type === filters.type) && (!filters.line || record.line === filters.line) && (!filters.status || record.status === filters.status))
		.slice()
		.sort((left, right) => {
			const leftValue = left[sort.key]
			const rightValue = right[sort.key]
			const result = typeof leftValue === 'number' && typeof rightValue === 'number' ? leftValue - rightValue : String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true })
			return sort.order === 'ascend' ? result : -result
		})
}

export const useController = () =>
	createDNSController({
		getCredentials: async () => (await import('@api/dns')).getDNSCredentials(),
		getZones: async (credentialID) => (await import('@api/dns')).getDNSZones(credentialID),
		getSnapshot: async (credentialID, zone) => (await import('@api/dns')).getDNSSnapshot(credentialID, zone),
		getSession: async () => (await import('@api/dns')).getDNSSession(),
		createRecord: async (input) => (await import('@api/dns')).createDNSRecord(input),
		updateRecord: async (input) => (await import('@api/dns')).updateDNSRecord(input),
		deleteRecord: async (input) => (await import('@api/dns')).deleteDNSRecord(input),
		setRecordStatus: async (input) => (await import('@api/dns')).setDNSRecordStatus(input),
	})
