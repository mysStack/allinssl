import { beforeEach, describe, expect, it, vi } from 'vitest'

import { instance } from '@api/index'

import type { DNSCreateRecord, DNSCreateRecordPreviewInput } from '@/types/dns'

import { createDNSRecordPreview } from './dns'

vi.mock('@api/index', () => ({ instance: { post: vi.fn() } }))

const post = vi.mocked(instance.post)

describe('createDNSRecordPreview', () => {
	beforeEach(() => {
		post.mockReset()
		post.mockResolvedValue({ data: { data: { job_id: 'job-1', state: 'queued' } } })
	})

	it.each([
		['A', { name: 'api', type: 'A', ttl: 600, value: '192.0.2.10' }, {}],
		['MX', { name: 'mail', type: 'MX', ttl: 600, value: 'mail.example.net', priority: 10 }, { priority: 10 }],
		[
			'SRV',
			{ name: '_sip._tcp', type: 'SRV', ttl: 600, value: 'service.example.net', priority: 10, weight: 20, port: 443 },
			{ priority: 10, weight: 20, port: 443 },
		],
		['CAA', { name: 'api', type: 'CAA', ttl: 600, value: 'letsencrypt.org', caaFlags: 0, caaTag: 'issue' }, { caa_flags: 0, caa_tag: 'issue' }],
	] as const)('sends only the allowed %s form fields', async (_recordType, record, specificFields) => {
		await createDNSRecordPreview(createInput(record as DNSCreateRecord))

		expect(post).toHaveBeenCalledWith('/v1/dns/create_record_preview', {
			credential_id: 1,
			zone: 'example.com',
			base_snapshot_hash: 'snapshot-a',
			name: record.name,
			type: record.type,
			ttl: 600,
			value: record.value,
			...specificFields,
			idempotency_key: 'request-a',
			csrf_token: 'csrf-a',
		})
	})
})

const createInput = (record: DNSCreateRecord): DNSCreateRecordPreviewInput => ({
	credentialID: 1,
	zone: 'example.com',
	baseSnapshotHash: 'snapshot-a',
	record,
	idempotencyKey: 'request-a',
	csrfToken: 'csrf-a',
})
