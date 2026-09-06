import { computed, ref } from 'vue'

import type { DNSCredential, DNSRecord, DNSSnapshot, DNSZone } from '@/types/dns'

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
}

export const createDNSController = (gateway: DNSGateway) => {
	const credentials = ref<DNSCredential[]>([])
	const zones = ref<DNSZone[]>([])
	const snapshot = ref<DNSSnapshot | null>(null)
	const credentialID = ref<number | null>(null)
	const zone = ref<string | null>(null)
	const loading = ref(false)
	const error = ref('')
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

	return {
		credentials,
		zones,
		snapshot,
		credentialID,
		zone,
		loading,
		error,
		credentialOptions,
		zoneOptions,
		recordFilters,
		recordSort,
		visibleRecords,
		setRecordSort,
		refreshCredentials,
		refreshZones,
		refreshSnapshot,
	}
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
	})
