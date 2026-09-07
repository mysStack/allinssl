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

export interface DNSHealth {
	version: string
	preview_available: boolean
	csrf_token: string
}

export type DNSAdoptJobState = 'queued' | 'previewing' | 'adopted' | 'previewed' | 'blocked' | 'failed'

export type DNSJobKind = 'adopt' | 'create_record_preview'

export type DNSRecordType = 'A' | 'AAAA' | 'CNAME' | 'TXT' | 'MX' | 'SRV' | 'CAA'

export interface DNSCreateRecord {
	name: string
	type: DNSRecordType
	ttl: number
	value: string
	priority?: number
	weight?: number
	port?: number
	caaFlags?: number
	caaTag?: string
}

export interface DNSCandidateRecord {
	name: string
	type: DNSRecordType
	ttl: number
}

export interface DNSChangeSummary {
	corrections: number
	details: string[]
}

export interface DNSAdoptJob {
	id: string
	kind?: DNSJobKind
	zone: string
	credential_id?: number
	state: DNSAdoptJobState
	snapshot_hash?: string
	plan_hash?: string
	error_code?: string
	candidate_record?: DNSCandidateRecord
	change_summary?: DNSChangeSummary
	created_at?: string
	updated_at?: string
}

export interface DNSCreateRecordPreviewInput {
	credentialID: number
	zone: string
	baseSnapshotHash: string
	record: DNSCreateRecord
	idempotencyKey: string
	csrfToken: string
}

export interface DNSCreateRecordPreviewResponse {
	job_id: string
	state: DNSAdoptJobState
}

export interface DNSBindZoneInput {
	credentialID: number
	zone: string
	snapshotHash: string
	adoptAll: true
	idempotencyKey: string
	csrfToken: string
}

export interface DNSBindZoneResponse {
	job_id: string
	state: DNSAdoptJobState
}

export type DNSCredentialsResponse = AxiosResponseData<DNSCredential[]>
export type DNSZonesResponse = AxiosResponseData<DNSZone[]>
export type DNSSnapshotResponse = AxiosResponseData<DNSSnapshot>
export type DNSHealthResponse = AxiosResponseData<DNSHealth>
export type DNSBindZoneResponseData = AxiosResponseData<DNSBindZoneResponse>
export type DNSAdoptJobResponse = AxiosResponseData<DNSAdoptJob>
export type DNSCreateRecordPreviewResponseData = AxiosResponseData<DNSCreateRecordPreviewResponse>
