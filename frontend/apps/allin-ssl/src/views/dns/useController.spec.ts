import { describe, expect, it, vi } from 'vitest'

import { createDNSController } from './useController'

describe('createDNSController', () => {
	it('does not load a snapshot until an authorization and zone are selected', async () => {
		const getSnapshot = vi.fn()
		const controller = createDNSController({ getCredentials: vi.fn().mockResolvedValue([]), getZones: vi.fn().mockResolvedValue([]), getSnapshot, getSession: vi.fn(), createRecord: vi.fn(), updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })

		await controller.refreshSnapshot()
		expect(getSnapshot).not.toHaveBeenCalled()
		controller.credentialID.value = 1
		controller.zone.value = 'example.com'
		await controller.refreshSnapshot()
		expect(getSnapshot).toHaveBeenCalledWith(1, 'example.com')
	})

	it('replaces the snapshot after creating a manageable record', async () => {
		const createdSnapshot = { zone: 'example.com', records: [{ provider_record_id: 'created-1', name: 'www', type: 'A', ttl: 600, value: '192.0.2.1', line: 'default', status: 'ENABLE', protected: false }], snapshot_hash: 'after', business_identity_hash: '', inventory_hash: '', compatible: true, write_enabled: false, read_only_reasons: [] }
		const createRecord = vi.fn().mockResolvedValue(createdSnapshot)
		const controller = createDNSController({ getCredentials: vi.fn(), getZones: vi.fn(), getSnapshot: vi.fn(), getSession: vi.fn().mockResolvedValue({ csrf_token: 'csrf-a' }), createRecord, updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })
		controller.credentialID.value = 1
		controller.zone.value = 'example.com'
		await controller.refreshSession()
		controller.openCreateRecordForm()
		controller.recordForm.value = { name: 'www', type: 'A', ttl: 600, line: 'default', loadBalancingPolicy: 'round_robin', values: [{ name: 'www', type: 'A', ttl: 600, value: '192.0.2.1', line: 'default', loadBalancingWeight: 1, status: 'ENABLE' }] }
		await controller.saveRecord()
		expect(createRecord).toHaveBeenCalledWith({ credentialID: 1, zone: 'example.com', record: { name: 'www', type: 'A', ttl: 600, value: '192.0.2.1', line: 'default', loadBalancingPolicy: 'round_robin', loadBalancingWeight: 1 }, csrfToken: 'csrf-a' })
		expect(controller.snapshot.value).toEqual(createdSnapshot)
	})

	it('keeps protected and NS records read-only', () => {
		const controller = createDNSController({ getCredentials: vi.fn(), getZones: vi.fn(), getSnapshot: vi.fn(), getSession: vi.fn(), createRecord: vi.fn(), updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })
		expect(controller.canManageRecord({ provider_record_id: '1', name: '@', type: 'NS', ttl: 600, value: 'ns1.example.com', line: 'default', status: 'ENABLE', protected: false })).toBe(false)
		expect(controller.canManageRecord({ provider_record_id: '2', name: '_acme-challenge', type: 'TXT', ttl: 600, value: 'token', line: 'default', status: 'ENABLE', protected: true })).toBe(false)
	})

	it('allows changing an editable record type while preserving its other fields', () => {
		const controller = createDNSController({ getCredentials: vi.fn(), getZones: vi.fn(), getSnapshot: vi.fn(), getSession: vi.fn(), createRecord: vi.fn(), updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })
		controller.openEditRecordForm({ provider_record_id: '1', name: 'argo', type: 'CNAME', ttl: 600, value: 'alb.example.com.', line: 'default', status: 'ENABLE', protected: false })

		controller.setRecordType('A')

		expect(controller.recordForm.value).toMatchObject({ name: 'argo', type: 'A', ttl: 600, line: 'default', loadBalancingPolicy: 'round_robin' })
		expect(controller.recordForm.value.values[0]).toMatchObject({ value: 'alb.example.com.', loadBalancingWeight: 1 })
	})

	it('preserves the configured A record load-balancing policy when editing', () => {
		const controller = createDNSController({ getCredentials: vi.fn(), getZones: vi.fn(), getSnapshot: vi.fn(), getSession: vi.fn(), createRecord: vi.fn(), updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })
		controller.openEditRecordForm({ provider_record_id: '1', name: 'api', type: 'A', ttl: 600, value: '192.0.2.10', line: 'default', status: 'ENABLE', protected: false, load_balancing_policy: 'weight', load_balancing_weight: 20 })

		expect(controller.recordForm.value.loadBalancingPolicy).toBe('weight')
		expect(controller.recordForm.value.values[0].loadBalancingWeight).toBe(20)
	})

	it('edits records with the same host, type and line as one record set', () => {
		const controller = createDNSController({ getCredentials: vi.fn(), getZones: vi.fn(), getSnapshot: vi.fn(), getSession: vi.fn(), createRecord: vi.fn(), updateRecord: vi.fn(), deleteRecord: vi.fn(), setRecordStatus: vi.fn() })
		controller.snapshot.value = {
			zone: 'example.com', snapshot_hash: 'snapshot', business_identity_hash: '', inventory_hash: '', compatible: true, write_enabled: false, read_only_reasons: [],
			records: [
				{ provider_record_id: 'a-1', name: 'api', type: 'A', ttl: 600, value: '192.0.2.10', line: 'default', status: 'ENABLE', protected: false, load_balancing_policy: 'weight', load_balancing_weight: 10, remark: '主站' },
				{ provider_record_id: 'a-2', name: 'api', type: 'A', ttl: 600, value: '192.0.2.11', line: 'default', status: 'DISABLE', protected: false, load_balancing_policy: 'weight', load_balancing_weight: 20, remark: '备用' },
			],
		}

		controller.openEditRecordForm(controller.snapshot.value.records[0])

		expect(controller.recordForm.value).toMatchObject({ name: 'api', type: 'A', ttl: 600, line: 'default', loadBalancingPolicy: 'weight' })
		expect(controller.recordForm.value.values).toEqual([
			expect.objectContaining({ recordID: 'a-1', value: '192.0.2.10', status: 'ENABLE', remark: '主站', loadBalancingWeight: 10 }),
			expect.objectContaining({ recordID: 'a-2', value: '192.0.2.11', status: 'DISABLE', remark: '备用', loadBalancingWeight: 20 }),
		])
	})
})
