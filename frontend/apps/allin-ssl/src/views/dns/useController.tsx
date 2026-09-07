import { computed, ref } from 'vue'

import type {
	DNSAdoptJob,
	DNSBindZoneInput,
	DNSCreateRecord,
	DNSCreateRecordPreviewInput,
	DNSCredential,
	DNSHealth,
	DNSRecord,
	DNSSnapshot,
	DNSZone,
} from '@/types/dns'

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
	getHealth?: () => Promise<DNSHealth>
	bindZone?: (input: DNSBindZoneInput) => Promise<{ job_id: string; state: DNSAdoptJob['state'] }>
	createRecordPreview?: (input: DNSCreateRecordPreviewInput) => Promise<{ job_id: string; state: DNSAdoptJob['state'] }>
	getJob?: (jobID: string) => Promise<DNSAdoptJob>
}

const emptyCreateRecord = (): DNSCreateRecord => ({ name: '', type: 'A', ttl: 600, value: '' })

export const createDNSController = (gateway: DNSGateway) => {
	const credentials = ref<DNSCredential[]>([])
	const zones = ref<DNSZone[]>([])
	const snapshot = ref<DNSSnapshot | null>(null)
	const credentialID = ref<number | null>(null)
	const zone = ref<string | null>(null)
	const loading = ref(false)
	const adoptLoading = ref(false)
	const createPreviewLoading = ref(false)
	const error = ref('')
	const health = ref<DNSHealth | null>(null)
	const adoptJob = ref<DNSAdoptJob | null>(null)
	const createRecordModalVisible = ref(false)
	const createRecordForm = ref<DNSCreateRecord>(emptyCreateRecord())
	const createPreviewJob = ref<DNSAdoptJob | null>(null)
	const recordFilters = ref<DNSRecordFilters>({ name: '', type: '', line: '', status: '', value: '' })
	const recordSort = ref<{ key: DNSRecordSortKey; order: DNSRecordSortOrder }>({ key: 'provider_record_id', order: 'descend' })
	const credentialOptions = computed(() => credentials.value.map((item) => ({ label: item.name, value: item.id })))
	const zoneOptions = computed(() => zones.value.map((item) => ({ label: item.name, value: item.name })))
	const visibleRecords = computed(() => filterRecords(snapshot.value?.records ?? [], recordFilters.value, recordSort.value))
	const canStartAdoptPreview = computed(() => Boolean(credentialID.value && zone.value && snapshot.value?.compatible && health.value?.preview_available && !adoptLoading.value))
	const canCreateRecordPreview = computed(
		() =>
			Boolean(credentialID.value && zone.value && snapshot.value?.compatible && health.value?.preview_available && health.value.csrf_token && !createPreviewLoading.value) &&
			adoptJob.value?.kind === 'adopt' &&
			adoptJob.value.state === 'adopted' &&
			adoptJob.value.credential_id === credentialID.value &&
			adoptJob.value.zone === zone.value &&
			adoptJob.value.snapshot_hash === snapshot.value?.snapshot_hash,
	)
	let pollTimer: ReturnType<typeof setTimeout> | undefined
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
				resetCreateRecordPreview()
			}
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 授权读取失败'
		} finally {
			loading.value = false
		}
	}

	const refreshZones = async () => {
		stopPolling()
		zones.value = []
		zone.value = null
		snapshot.value = null
		adoptJob.value = null
		resetCreateRecordPreview()
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
		stopPolling()
		snapshot.value = null
		adoptJob.value = null
		resetCreateRecordPreview()
		error.value = ''
		if (!credentialID.value || !zone.value) return
		loading.value = true
		try {
			snapshot.value = await gateway.getSnapshot(credentialID.value, zone.value)
			await refreshHealth()
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录读取失败'
		} finally {
			loading.value = false
		}
	}

	const refreshHealth = async () => {
		if (!gateway.getHealth) return
		try {
			health.value = await gateway.getHealth()
		} catch (requestError) {
			health.value = null
			error.value = requestError instanceof Error ? requestError.message : 'DNSControl 状态读取失败'
		}
	}

	const startAdoptPreview = async () => {
		if (!gateway.bindZone || !credentialID.value || !zone.value || !snapshot.value?.compatible) return
		if (!health.value) await refreshHealth()
		if (!health.value?.preview_available || !health.value.csrf_token) {
			error.value = 'DNSControl Preview 当前不可用'
			return
		}
		adoptLoading.value = true
		error.value = ''
		try {
			const created = await gateway.bindZone({
				credentialID: credentialID.value,
				zone: zone.value,
				snapshotHash: snapshot.value.snapshot_hash,
				adoptAll: true,
				idempotencyKey: createIdempotencyKey(),
				csrfToken: health.value.csrf_token,
			})
			adoptJob.value = { id: created.job_id, state: created.state, zone: zone.value }
			await pollAdoptJob(created.job_id)
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 纳管预览创建失败'
		} finally {
			adoptLoading.value = false
		}
	}

	const pollAdoptJob = async (jobID: string) => {
		if (!gateway.getJob) return
		clearTimeout(pollTimer)
		try {
			const job = await gateway.getJob(jobID)
			adoptJob.value = job
			if (job.state === 'queued' || job.state === 'previewing') {
				pollTimer = setTimeout(() => void pollAdoptJob(jobID), 1500)
			}
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 纳管任务读取失败'
		}
	}

	const setZone = (value: string | null) => {
		stopPolling()
		zone.value = value
		snapshot.value = null
		adoptJob.value = null
		resetCreateRecordPreview()
	}

	const openCreateRecordForm = () => {
		if (!canCreateRecordPreview.value) return
		createRecordForm.value = emptyCreateRecord()
		createPreviewJob.value = null
		createRecordModalVisible.value = true
		error.value = ''
	}

	const startCreateRecordPreview = async () => {
		if (!gateway.createRecordPreview || !canCreateRecordPreview.value || !credentialID.value || !zone.value || !snapshot.value || !health.value?.csrf_token) return
		const record = normalizedCreateRecord(createRecordForm.value)
		const validationError = validateCreateRecord(record)
		if (validationError) {
			error.value = validationError
			return
		}
		createPreviewLoading.value = true
		error.value = ''
		try {
			const created = await gateway.createRecordPreview({
				credentialID: credentialID.value,
				zone: zone.value,
				baseSnapshotHash: snapshot.value.snapshot_hash,
				record,
				idempotencyKey: createIdempotencyKey(),
				csrfToken: health.value.csrf_token,
			})
			createPreviewJob.value = { id: created.job_id, kind: 'create_record_preview', state: created.state, zone: zone.value }
			await pollCreatePreviewJob(created.job_id)
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录预览创建失败'
		} finally {
			createPreviewLoading.value = false
		}
	}

	const pollCreatePreviewJob = async (jobID: string) => {
		if (!gateway.getJob) return
		clearTimeout(pollTimer)
		try {
			const job = await gateway.getJob(jobID)
			createPreviewJob.value = job
			if (job.state === 'queued' || job.state === 'previewing') {
				pollTimer = setTimeout(() => void pollCreatePreviewJob(jobID), 1500)
			}
		} catch (requestError) {
			error.value = requestError instanceof Error ? requestError.message : 'DNS 记录预览任务读取失败'
		}
	}

	function resetCreateRecordPreview() {
		createRecordModalVisible.value = false
		createRecordForm.value = emptyCreateRecord()
		createPreviewJob.value = null
	}

	const stopPolling = () => {
		clearTimeout(pollTimer)
		pollTimer = undefined
	}

	return {
		credentials,
		zones,
		snapshot,
		credentialID,
		zone,
		loading,
		adoptLoading,
		createPreviewLoading,
		error,
		health,
		adoptJob,
		createRecordModalVisible,
		createRecordForm,
		createPreviewJob,
		credentialOptions,
		zoneOptions,
		recordFilters,
		recordSort,
		visibleRecords,
		canStartAdoptPreview,
		canCreateRecordPreview,
		setRecordSort,
		refreshCredentials,
		refreshZones,
		refreshSnapshot,
		refreshHealth,
		setZone,
		startAdoptPreview,
		openCreateRecordForm,
		startCreateRecordPreview,
		stopPolling,
	}
}

const normalizedCreateRecord = (form: DNSCreateRecord): DNSCreateRecord => {
	const record: DNSCreateRecord = { name: form.name.trim(), type: form.type, ttl: form.ttl, value: form.value.trim() }
	if (form.type === 'MX') record.priority = form.priority
	if (form.type === 'SRV') {
		record.priority = form.priority
		record.weight = form.weight
		record.port = form.port
	}
	if (form.type === 'CAA') {
		record.caaFlags = form.caaFlags
		record.caaTag = form.caaTag?.trim()
	}
	return record
}

const validateCreateRecord = (record: DNSCreateRecord): string => {
	if (!record.name || !record.value || !Number.isInteger(record.ttl) || record.ttl < 600 || record.ttl > 86400) return '请完整填写记录名称、记录值和有效 TTL'
	const validUint16 = (value: number | undefined) => Number.isInteger(value) && value !== undefined && value >= 0 && value <= 65535
	if (record.type === 'MX' && !validUint16(record.priority)) return '请完整填写 MX 记录字段'
	if (record.type === 'SRV' && (!validUint16(record.priority) || !validUint16(record.weight) || !validUint16(record.port))) return '请完整填写 SRV 记录字段'
	if (record.type === 'CAA' && (!Number.isInteger(record.caaFlags) || record.caaFlags === undefined || record.caaFlags < 0 || record.caaFlags > 255 || !record.caaTag)) return '请完整填写 CAA 记录字段'
	return ''
}

const createIdempotencyKey = () => {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
	return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

const filterRecords = (records: DNSRecord[], filters: DNSRecordFilters, sort: { key: DNSRecordSortKey; order: DNSRecordSortOrder }): DNSRecord[] => {
	const includes = (value: string, filter: string) => value.toLowerCase().includes(filter.trim().toLowerCase())
	return records
		.filter(
			(record) =>
				includes(record.name, filters.name) &&
				includes(record.value, filters.value) &&
				(!filters.type || record.type === filters.type) &&
				(!filters.line || record.line === filters.line) &&
				(!filters.status || record.status === filters.status),
		)
		.slice()
		.sort((left, right) => compareRecords(left, right, sort))
}

const compareRecords = (left: DNSRecord, right: DNSRecord, sort: { key: DNSRecordSortKey; order: DNSRecordSortOrder }) => {
	const leftValue = left[sort.key]
	const rightValue = right[sort.key]
	const result = typeof leftValue === 'number' && typeof rightValue === 'number' ? leftValue - rightValue : String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true })
	return sort.order === 'ascend' ? result : -result
}

export const useController = () =>
	createDNSController({
		getCredentials: async () => (await import('@api/dns')).getDNSCredentials(),
		getZones: async (credentialID) => (await import('@api/dns')).getDNSZones(credentialID),
		getSnapshot: async (credentialID, zone) => (await import('@api/dns')).getDNSSnapshot(credentialID, zone),
		getHealth: async () => (await import('@api/dns')).getDNSHealth(),
		bindZone: async (input) => (await import('@api/dns')).bindDNSZone(input),
		createRecordPreview: async (input) => (await import('@api/dns')).createDNSRecordPreview(input),
		getJob: async (jobID) => (await import('@api/dns')).getDNSAdoptJob(jobID),
	})
