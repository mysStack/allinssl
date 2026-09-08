import { describe, expect, it, vi } from 'vitest'

import { createDNSController } from './useController'

describe('createDNSController', () => {
	it('does not load a snapshot until an authorization and zone are selected', async () => {
		const getSnapshot = vi.fn()
		const controller = createDNSController({ getCredentials: vi.fn().mockResolvedValue([]), getZones: vi.fn().mockResolvedValue([]), getSnapshot })

		await controller.refreshSnapshot()
		expect(getSnapshot).not.toHaveBeenCalled()
		controller.credentialID.value = 1
		controller.zone.value = 'example.com'
		await controller.refreshSnapshot()
		expect(getSnapshot).toHaveBeenCalledWith(1, 'example.com')
	})
})
