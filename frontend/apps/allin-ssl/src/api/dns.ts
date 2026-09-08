import type { DNSCredential, DNSCredentialsResponse, DNSSnapshot, DNSSnapshotResponse, DNSZone, DNSZonesResponse } from '@/types/dns'

import { instance } from '@api/index'

const responseData = <T>(response: { data: { data: T } }): T => response.data.data

export const getDNSCredentials = async (): Promise<DNSCredential[]> => responseData(await instance.post<DNSCredentialsResponse>('/v1/dns/get_credentials'))

export const getDNSZones = async (credentialID: number): Promise<DNSZone[]> =>
	responseData(await instance.post<DNSZonesResponse>('/v1/dns/get_zones', { credential_id: credentialID }))

export const getDNSSnapshot = async (credentialID: number, zone: string): Promise<DNSSnapshot> =>
	responseData(await instance.post<DNSSnapshotResponse>('/v1/dns/get_snapshot', { credential_id: credentialID, zone }))
