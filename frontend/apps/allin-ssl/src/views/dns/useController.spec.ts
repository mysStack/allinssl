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

	it('creates a zero-difference adopt preview from a compatible snapshot', async () => {
		const getHealth = vi.fn().mockResolvedValue({ version: 'v5.0.3', preview_available: true, csrf_token: 'csrf-a' })
		const bindZone = vi.fn().mockResolvedValue({ job_id: 'job-1', state: 'queued' })
		const getJob = vi.fn().mockResolvedValue({ id: 'job-1', state: 'queued', zone: 'example.com' })
		const controller = createDNSController({
			getCredentials: vi.fn().mockResolvedValue([]),
			getZones: vi.fn().mockResolvedValue([]),
			getSnapshot: vi.fn(),
			getHealth,
			bindZone,
			getJob,
		})
		controller.credentialID.value = 1
		controller.zone.value = 'example.com'
		controller.snapshot.value = {
			zone: 'example.com',
			records: [],
			snapshot_hash: 'snapshot-a',
			business_identity_hash: 'business-a',
			inventory_hash: 'inventory-a',
			compatible: true,
			write_enabled: false,
			read_only_reasons: [],
		}

		await controller.startAdoptPreview()

		expect(getHealth).toHaveBeenCalledOnce()
		expect(bindZone).toHaveBeenCalledWith(
			expect.objectContaining({
				credentialID: 1,
				zone: 'example.com',
				snapshotHash: 'snapshot-a',
				csrfToken: 'csrf-a',
				adoptAll: true,
			}),
		)
		expect(controller.adoptJob.value).toMatchObject({ id: 'job-1', state: 'queued', zone: 'example.com' })
	})

	it('enables record preview only for the current compatible adopted snapshot', () => {
		const controller = createDNSController({
			getCredentials: vi.fn().mockResolvedValue([]),
			getZones: vi.fn().mockResolvedValue([]),
			getSnapshot: vi.fn(),
		})
		controller.credentialID.value = 1
		controller.zone.value = 'example.com'
		controller.snapshot.value = compatibleSnapshot()

		expect(controller.canCreateRecordPreview.value).toBe(false)
		controller.health.value = { version: 'v5.0.3', preview_available: true, csrf_token: 'csrf-a' }
		expect(controller.canCreateRecordPreview.value).toBe(false)
		controller.adoptJob.value = {
			id: 'adopt-1',
			kind: 'adopt',
			zone: 'example.com',
			credential_id: 1,
			state: 'adopted',
			snapshot_hash: 'snapshot-a',
		}
		expect(controller.canCreateRecordPreview.value).toBe(true)

		controller.snapshot.value = { ...compatibleSnapshot(), compatible: false, read_only_reasons: ['UNSUPPORTED_LINE'] }
		expect(controller.canCreateRecordPreview.value).toBe(false)
	})

	it('creates an A record preview without changing the read-only snapshot', async () => {
		const createRecordPreview = vi.fn().mockResolvedValue({ job_id: 'change-1', state: 'queued' })
		const getJob = vi.fn().mockResolvedValue({
			id: 'change-1',
			kind: 'create_record_preview',
			zone: 'example.com',
			credential_id: 1,
			state: 'previewed',
			snapshot_hash: 'snapshot-a',
			candidate_record: { name: 'api', type: 'A', ttl: 600 },
			change_summary: { corrections: 1, details: ['sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'] },
		})
		const controller = createReadyController(createRecordPreview, getJob)
		const originalSnapshot = compatibleSnapshot()
		controller.createRecordForm.value = { name: 'api', type: 'A', ttl: 600, value: '192.0.2.10' }

		await controller.startCreateRecordPreview()

		expect(createRecordPreview).toHaveBeenCalledWith({
			credentialID: 1,
			zone: 'example.com',
			baseSnapshotHash: 'snapshot-a',
			record: { name: 'api', type: 'A', ttl: 600, value: '192.0.2.10' },
			idempotencyKey: expect.any(String),
			csrfToken: 'csrf-a',
		})
		expect(controller.createPreviewJob.value).toMatchObject({ id: 'change-1', state: 'previewed' })
		expect(controller.snapshot.value).toEqual(originalSnapshot)
	})

	it.each([
		['MX', { priority: 10 }],
		['SRV', { priority: 10, weight: 20, port: 443 }],
		['CAA', { caaFlags: 0, caaTag: 'issue' }],
	] as const)('serializes only the %s-specific record fields', async (recordType, specificFields) => {
		const createRecordPreview = vi.fn().mockResolvedValue({ job_id: `change-${recordType}`, state: 'queued' })
		const getJob = vi.fn().mockResolvedValue({
			id: `change-${recordType}`,
			kind: 'create_record_preview',
			zone: 'example.com',
			credential_id: 1,
			state: 'previewed',
		})
		const controller = createReadyController(createRecordPreview, getJob)
		controller.createRecordForm.value = {
			name: recordType === 'SRV' ? '_sip._tcp' : 'mail',
			type: recordType,
			ttl: 600,
			value: recordType === 'CAA' ? 'letsencrypt.org' : 'target.example.net',
			...specificFields,
		}

		await controller.startCreateRecordPreview()

		expect(createRecordPreview).toHaveBeenCalledWith(
			expect.objectContaining({
				record: {
					name: recordType === 'SRV' ? '_sip._tcp' : 'mail',
					type: recordType,
					ttl: 600,
					value: recordType === 'CAA' ? 'letsencrypt.org' : 'target.example.net',
					...specificFields,
				},
			}),
		)
	})

	it('rejects an incomplete type-specific form before calling the gateway', async () => {
		const createRecordPreview = vi.fn()
		const controller = createReadyController(createRecordPreview, vi.fn())
		controller.createRecordForm.value = { name: 'mail', type: 'MX', ttl: 600, value: 'mail.example.net' }

		await controller.startCreateRecordPreview()

		expect(createRecordPreview).not.toHaveBeenCalled()
		expect(controller.error.value).toBe('请完整填写 MX 记录字段')
	})
})

const compatibleSnapshot = () => ({
	zone: 'example.com',
	records: [],
	snapshot_hash: 'snapshot-a',
	business_identity_hash: 'business-a',
	inventory_hash: 'inventory-a',
	compatible: true,
	write_enabled: false as const,
	read_only_reasons: [],
})

const createReadyController = (createRecordPreview: ReturnType<typeof vi.fn>, getJob: ReturnType<typeof vi.fn>) => {
	const controller = createDNSController({
		getCredentials: vi.fn().mockResolvedValue([]),
		getZones: vi.fn().mockResolvedValue([]),
		getSnapshot: vi.fn(),
		getHealth: vi.fn().mockResolvedValue({ version: 'v5.0.3', preview_available: true, csrf_token: 'csrf-a' }),
		bindZone: vi.fn(),
		createRecordPreview,
		getJob,
	})
	controller.credentialID.value = 1
	controller.zone.value = 'example.com'
	controller.snapshot.value = compatibleSnapshot()
	controller.health.value = { version: 'v5.0.3', preview_available: true, csrf_token: 'csrf-a' }
	controller.adoptJob.value = {
		id: 'adopt-1',
		kind: 'adopt',
		zone: 'example.com',
		credential_id: 1,
		state: 'adopted',
		snapshot_hash: 'snapshot-a',
	}
	return controller
}
