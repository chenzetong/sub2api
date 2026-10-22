import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import PoolAccountsView from '../PoolAccountsView.vue'

const { list, stats } = vi.hoisted(() => ({ list: vi.fn(), stats: vi.fn() }))
vi.mock('@/api/quotaPools', () => ({ poolAccountsAPI: { list, stats } }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' }
}))
vi.mock('@/components/admin/account/AccountStatsModal.vue', () => ({
  default: {
    name: 'AccountStatsModal',
    props: ['show', 'account', 'loadStats'],
    template: '<div class="stats" v-if="show">{{ account.name }}</div>'
  }
}))

const account = {
  id: 1,
  name: 'Shared account',
  pool_name: 'Pool A',
  pool_id: 3,
  share_percent: 20,
  status: 'active',
  platform: 'openai',
  type: 'oauth',
  five_hour: null,
  seven_day: null,
  estimate: {
    source: 'unavailable',
    total_cost: null,
    reason: 'no_previous_prediction'
  }
}
const render = () =>
  mount(PoolAccountsView, {
    global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } }
  })
beforeEach(() => {
  vi.clearAllMocks()
  list.mockResolvedValue([account])
})

describe('PoolAccountsView', () => {
  it('shows authorized account usage and opens the shared statistics in user scope', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('Shared account')
    expect(wrapper.text()).toContain('quotaPools.pending')
    expect(wrapper.findAll('button').map((b) => b.text())).toEqual([
      'quotaPools.details'
    ])
    await wrapper.get('button').trigger('click')
    const modal = wrapper.findComponent({ name: 'AccountStatsModal' })
    // The shared dialog receives the scoped GET loader instead of invoking an admin API.
    expect(wrapper.find('.stats').text()).toBe('Shared account')
    expect(modal.exists()).toBe(true)
    expect(modal.props('loadStats')).toBe(stats)
    expect(stats).not.toHaveBeenCalled()
  })
  it('filters locally and handles no granted accounts', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input').setValue('absent')
    expect(wrapper.text()).toContain('quotaPools.noResults')
    list.mockResolvedValue([])
    const empty = render()
    await flushPromises()
    expect(empty.text()).toContain('quotaPools.noAccess')
    expect(empty.findAll('button')).toHaveLength(0)
  })
  it('shows errors and retries the scoped read', async () => {
    list.mockRejectedValueOnce(new Error('offline'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('offline')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('Shared account')
  })
})
