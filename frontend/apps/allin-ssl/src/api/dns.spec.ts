import { describe, expect, it } from 'vitest'

import type { DNSRecord } from '@/types/dns'

describe('DNS record type', () => {
	it('retains the provider record identifier for later direct operations', () => {
		const record: DNSRecord = {
			provider_record_id: '1', name: 'api', type: 'A', ttl: 600, value: '192.0.2.10', line: 'default', status: 'ENABLE', protected: false,
		}
		expect(record.provider_record_id).toBe('1')
	})
})
