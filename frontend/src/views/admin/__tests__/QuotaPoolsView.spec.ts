import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import QuotaPoolsView from '../QuotaPoolsView.vue'
import type { PoolAllocation } from '@/api/quotaPools'

const api = vi.hoisted(() => ({
  allocation: vi.fn(),
  runs: vi.fn(),
  save: vi.fn(),
  preview: vi.fn(),
  sync: vi.fn(),
  settings: vi.fn()
}))
vi.mock('@/api/quotaPools', () => ({ quotaPoolsAPI: api }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))
const fixture = (): PoolAllocation => ({
  revision: 1,
  pools: [
    {
      id: 1,
      name: 'A',
      revision: 1,
      account_ids: [1, 2],
      shares: [{ user_id: 7, share_percent: 20 }]
    },
    {
      id: 2,
      name: 'B',
      revision: 1,
      account_ids: [3],
      shares: [{ user_id: 7, share_percent: 30 }]
    }
  ],
  accounts: [1, 2, 3].map((id) => ({
    id,
    name: `Account ${id}`,
    platform: 'openai',
    type: 'oauth',
    status: 'active',
    pool_id: 0,
    pool_name: '',
    share_percent: 0,
    five_hour: null,
    seven_day: null,
    estimate: null
  })),
  users: [{ id: 7, name: 'Member', balance: 10, frozen_balance: 0 }],
  settings: { enabled: false, interval_minutes: 5, next_run_at: null }
})
const render = () =>
  mount(QuotaPoolsView, {
    global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } }
  })
const button = (wrapper: ReturnType<typeof render>, text: string) =>
  wrapper.findAll('button').find((b) => b.text() === text)!
beforeEach(() => {
  vi.clearAllMocks()
  api.allocation.mockResolvedValue(fixture())
  api.runs.mockResolvedValue([])
  api.save.mockImplementation(async (_revision, pools) => ({
    ...fixture(),
    revision: 2,
    pools
  }))
})

describe('QuotaPoolsView', () => {
  it('keeps contract saving separate from synchronization', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper
      .get('input[aria-label="quotaPools.poolName"]')
      .setValue('Renamed')
    expect(
      button(wrapper, 'quotaPools.preview').attributes('disabled')
    ).toBeDefined()
    await button(wrapper, 'quotaPools.saveAllocation').trigger('click')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(
      1,
      expect.arrayContaining([expect.objectContaining({ name: 'Renamed' })])
    )
    expect(api.sync).not.toHaveBeenCalled()
  })
  it('clears differing contracts on merge and inherits shares on split', async () => {
    const wrapper = render()
    await flushPromises()
    for (const checkbox of wrapper.findAll(
      'input[aria-label="quotaPools.selectPool"]'
    ))
      await checkbox.setValue(true)
    await button(wrapper, 'quotaPools.merge').trigger('click')
    expect(wrapper.text()).toContain('quotaPools.mergeCleared')
    await button(wrapper, 'quotaPools.saveAllocation').trigger('click')
    await flushPromises()
    expect(api.save.mock.calls[0]![1]).toEqual([
      expect.objectContaining({ account_ids: [1, 2, 3], shares: [] })
    ])
    const splitWrapper = render()
    await flushPromises()
    await splitWrapper
      .get('input[type="checkbox"][value="1"]:not([aria-label])')
      .setValue(true)
    await button(splitWrapper, 'quotaPools.split').trigger('click')
    await button(splitWrapper, 'quotaPools.saveAllocation').trigger('click')
    await flushPromises()
    expect(api.save.mock.calls[1]![1]).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          account_ids: [1],
          shares: [{ user_id: 7, share_percent: 20 }]
        })
      ])
    )
  })
  it('rejects totals above 100% before sending a contract', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#share-0-7').setValue('100.001')
    expect(
      button(wrapper, 'quotaPools.saveAllocation').attributes('disabled')
    ).toBeDefined()
    expect(api.save).not.toHaveBeenCalled()
  })
  it('sends the preview revision with a separate idempotent sync', async () => {
    const preview = {
      revision: 1,
      accounts: [],
      users: [],
      created_at: new Date().toISOString(),
      status: 'preview'
    }
    api.preview.mockResolvedValue(preview)
    api.sync.mockResolvedValue({ ...preview, status: 'completed' })
    const wrapper = render()
    await flushPromises()
    await button(wrapper, 'quotaPools.preview').trigger('click')
    await flushPromises()
    await button(wrapper, 'quotaPools.syncNow').trigger('click')
    await flushPromises()
    expect(api.sync).toHaveBeenCalledWith(expect.any(String), 1)
    expect(api.save).not.toHaveBeenCalled()
    expect(
      button(wrapper, 'quotaPools.syncNow').attributes('disabled')
    ).toBeDefined()
  })
})
