import type { DNSCredential, DNSCredentialsResponse, DNSRecordInput, DNSRecordMutationInput, DNSSession, DNSSessionResponse, DNSSnapshot, DNSSnapshotResponse, DNSZone, DNSZonesResponse } from '@/types/dns'

import { instance } from '@api/index'

const responseData = <T>(response: { data: { data: T } }): T => response.data.data

export const getDNSCredentials = async (): Promise<DNSCredential[]> => responseData(await instance.post<DNSCredentialsResponse>('/v1/dns/get_credentials'))

export const getDNSZones = async (credentialID: number): Promise<DNSZone[]> =>
	responseData(await instance.post<DNSZonesResponse>('/v1/dns/get_zones', { credential_id: credentialID }))

export const getDNSSnapshot = async (credentialID: number, zone: string): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/get_snapshot', { credential_id: credentialID, zone }))

export const getDNSSession = async (): Promise<DNSSession> => responseData(await instance.post<DNSSessionResponse>('/v1/dns/get_session'))

export const createDNSRecord = async (input: DNSRecordMutationInput & { record: DNSRecordInput }): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/create_record', recordPayload(input)))

export const updateDNSRecord = async (input: DNSRecordMutationInput & { record: DNSRecordInput; recordID: string }): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/update_record', { ...recordPayload(input), record_id: input.recordID }))

export const deleteDNSRecord = async (input: DNSRecordMutationInput & { recordID: string }): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/delete_record', { credential_id: input.credentialID, zone: input.zone, record_id: input.recordID, csrf_token: input.csrfToken }))

export const setDNSRecordStatus = async (input: DNSRecordMutationInput & { recordID: string; status: 'ENABLE' | 'DISABLE' }): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/set_record_status', { credential_id: input.credentialID, zone: input.zone, record_id: input.recordID, status: input.status, csrf_token: input.csrfToken }))

const recordPayload = (input: DNSRecordMutationInput & { record: DNSRecordInput }) => {
	const record = input.record
	const specificFields = record.type === 'MX' ? { priority: record.priority } : record.type === 'SRV' ? { priority: record.priority, weight: record.weight, port: record.port } : record.type === 'CAA' ? { caa_flags: record.caaFlags, caa_tag: record.caaTag } : {}
	return { credential_id: input.credentialID, zone: input.zone, name: record.name, type: record.type, ttl: record.ttl, value: record.value, line: record.line, ...specificFields, csrf_token: input.csrfToken }
}
