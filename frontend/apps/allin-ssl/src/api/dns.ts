import type {
	DNSCredential,
	DNSCredentialsResponse,
	DNSCreateRecordPreviewInput,
	DNSCreateRecordPreviewResponse,
	DNSCreateRecordPreviewResponseData,
	DNSAdoptJob,
	DNSAdoptJobResponse,
	DNSBindZoneInput,
	DNSBindZoneResponse,
	DNSBindZoneResponseData,
	DNSHealth,
	DNSHealthResponse,
	DNSSnapshot,
	DNSSnapshotResponse,
	DNSZone,
	DNSZonesResponse,
} from '@/types/dns'

import { instance } from '@api/index'

const responseData = <T>(response: { data: { data: T } }): T => response.data.data

export const getDNSCredentials = async (): Promise<DNSCredential[]> =>
	responseData(await instance.post<DNSCredentialsResponse>('/v1/dns/get_credentials'))

export const getDNSZones = async (credentialID: number): Promise<DNSZone[]> =>
	responseData(await instance.post<DNSZonesResponse>('/v1/dns/get_zones', { credential_id: credentialID }))

export const getDNSSnapshot = async (credentialID: number, zone: string): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/get_snapshot', { credential_id: credentialID, zone }))

export const getDNSHealth = async (): Promise<DNSHealth> => responseData(await instance.post<DNSHealthResponse>('/v1/dns/get_health'))

export const bindDNSZone = async (input: DNSBindZoneInput): Promise<DNSBindZoneResponse> =>
	responseData(
		await instance.post<DNSBindZoneResponseData>('/v1/dns/bind_zone', {
			credential_id: input.credentialID,
			zone: input.zone,
			snapshot_hash: input.snapshotHash,
			adopt_all: input.adoptAll,
			idempotency_key: input.idempotencyKey,
			csrf_token: input.csrfToken,
		}),
	)

export const getDNSAdoptJob = async (jobID: string): Promise<DNSAdoptJob> =>
	responseData(await instance.post<DNSAdoptJobResponse>('/v1/dns/get_job', { job_id: jobID }))

export const createDNSRecordPreview = async (input: DNSCreateRecordPreviewInput): Promise<DNSCreateRecordPreviewResponse> => {
	const specificFields =
		input.record.type === 'MX'
			? { priority: input.record.priority }
			: input.record.type === 'SRV'
				? { priority: input.record.priority, weight: input.record.weight, port: input.record.port }
				: input.record.type === 'CAA'
					? { caa_flags: input.record.caaFlags, caa_tag: input.record.caaTag }
					: {}
	return responseData(
		await instance.post<DNSCreateRecordPreviewResponseData>('/v1/dns/create_record_preview', {
			credential_id: input.credentialID,
			zone: input.zone,
			base_snapshot_hash: input.baseSnapshotHash,
			name: input.record.name,
			type: input.record.type,
			ttl: input.record.ttl,
			value: input.record.value,
			...specificFields,
			idempotency_key: input.idempotencyKey,
			csrf_token: input.csrfToken,
		}),
	)
}
