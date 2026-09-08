import type { AxiosResponseData } from './public'

export interface DNSCredential {
	id: number
	name: string
	type: 'aliyun'
}

export interface DNSZone {
	name: string
	provider_zone_id: string
}

export interface DNSRecord {
	provider_record_id: string
	name: string
	type: string
	ttl: number
	value: string
	line: string
	status: string
	protected: boolean
	read_only_reasons?: string[]
}

export interface DNSSnapshot {
	zone: string
	records: DNSRecord[]
	snapshot_hash: string
	business_identity_hash: string
	inventory_hash: string
	compatible: boolean
	write_enabled: false
	read_only_reasons: string[]
}

export type DNSCredentialsResponse = AxiosResponseData<DNSCredential[]>
export type DNSZonesResponse = AxiosResponseData<DNSZone[]>
export type DNSSnapshotResponse = AxiosResponseData<DNSSnapshot>
