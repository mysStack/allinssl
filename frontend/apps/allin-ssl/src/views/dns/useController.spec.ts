import { describe, expect, it, vi } from 'vitest'

import { createDNSController } from './useController'

describe('createDNSController', () => {
	it('does not load a snapshot until an authorization and zone are selected', async () => {
		const getSnapshot = vi.fn()
		const controller = createDNSController({
			getCredentials: vi.fn().mockResolvedValue([]),
			getZones: vi.fn().mockResolvedValue([]),
			getSnapshot,
		})

		await controller.refreshSnapshot()
		expect(getSnapshot).not.toHaveBeenCalled()

		controller.credentialID.value = 1
		await controller.refreshSnapshot()
		expect(getSnapshot).not.toHaveBeenCalled()

		controller.zone.value = 'example.com'
		await controller.refreshSnapshot()
		expect(getSnapshot).toHaveBeenCalledWith(1, 'example.com')
	})

	it('filters records locally and defaults to newest record ID first', () => {
		const getSnapshot = vi.fn()
		const controller = createDNSController({
			getCredentials: vi.fn().mockResolvedValue([]),
			getZones: vi.fn().mockResolvedValue([]),
			getSnapshot,
		})

		controller.snapshot.value = {
			zone: 'example.com',
			records: [
				{ provider_record_id: '9', name: 'api', type: 'A', ttl: 600, value: '192.0.2.9', line: 'default', status: 'ENABLE', protected: false },
				{ provider_record_id: '100', name: 'www', type: 'CNAME', ttl: 600, value: 'origin.example.com', line: 'telecom', status: 'DISABLE', protected: false },
				{ provider_record_id: '21', name: 'www', type: 'A', ttl: 600, value: '192.0.2.21', line: 'default', status: 'ENABLE', protected: false },
			],
			snapshot_hash: 'snapshot',
			business_identity_hash: 'business',
			inventory_hash: 'inventory',
			compatible: true,
			write_enabled: false,
			read_only_reasons: [],
		}
		controller.recordFilters.value = { name: 'www', type: 'A', line: 'default', status: 'ENABLE', value: '192.0.2' }

		expect(controller.visibleRecords.value.map((record) => record.provider_record_id)).toEqual(['21'])

		controller.recordFilters.value = { name: '', type: '', line: '', status: '', value: '' }
		expect(controller.visibleRecords.value.map((record) => record.provider_record_id)).toEqual(['100', '21', '9'])

		controller.setRecordSort('name', 'ascend')
		expect(controller.visibleRecords.value.map((record) => record.name)).toEqual(['api', 'www', 'www'])
		expect(getSnapshot).not.toHaveBeenCalled()
	})
})
