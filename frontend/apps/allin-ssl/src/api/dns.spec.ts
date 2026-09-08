import { beforeEach, describe, expect, it, vi } from 'vitest'

import { instance } from '@api/index'

import type { DNSRecordInput } from '@/types/dns'

import { createDNSRecord, deleteDNSRecord, setDNSRecordStatus, updateDNSRecord } from './dns'

vi.mock('@api/index', () => ({ instance: { post: vi.fn() } }))

const post = vi.mocked(instance.post)
const record: DNSRecordInput = { name: '_sip._tcp', type: 'SRV', ttl: 600, value: 'service.example.net', line: 'default', priority: 10, weight: 20, port: 443 }

describe('direct DNS record API', () => {
	beforeEach(() => {
		post.mockReset()
		post.mockResolvedValue({ data: { data: { zone: 'example.com', records: [] } } })
	})

	it('serializes create and update forms with only type-specific fields', async () => {
		await createDNSRecord({ credentialID: 1, zone: 'example.com', record, csrfToken: 'csrf-a' })
		await updateDNSRecord({ credentialID: 1, zone: 'example.com', recordID: 'record-1', record, csrfToken: 'csrf-a' })
		for (const [path, expectedID] of [['/v1/dns/create_record', undefined], ['/v1/dns/update_record', 'record-1']] as const) {
			expect(post).toHaveBeenCalledWith(path, expect.objectContaining({ credential_id: 1, zone: 'example.com', name: '_sip._tcp', priority: 10, weight: 20, port: 443, csrf_token: 'csrf-a', ...(expectedID ? { record_id: expectedID } : {}) }))
		}
	})

	it('serializes delete and status without record fields', async () => {
		await deleteDNSRecord({ credentialID: 1, zone: 'example.com', recordID: 'record-1', csrfToken: 'csrf-a' })
		await setDNSRecordStatus({ credentialID: 1, zone: 'example.com', recordID: 'record-1', status: 'DISABLE', csrfToken: 'csrf-a' })
		expect(post).toHaveBeenNthCalledWith(1, '/v1/dns/delete_record', { credential_id: 1, zone: 'example.com', record_id: 'record-1', csrf_token: 'csrf-a' })
		expect(post).toHaveBeenNthCalledWith(2, '/v1/dns/set_record_status', { credential_id: 1, zone: 'example.com', record_id: 'record-1', status: 'DISABLE', csrf_token: 'csrf-a' })
	})
})
