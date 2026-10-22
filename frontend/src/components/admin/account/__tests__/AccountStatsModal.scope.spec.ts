import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountStatsModal from '../AccountStatsModal.vue'

const { adminStats } = vi.hoisted(() => ({ adminStats: vi.fn() }))
vi.mock('@/api/admin', () => ({
  adminAPI: { accounts: { getStats: adminStats } }
}))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))

const render = (loadStats?: (id: number) => Promise<never>) =>
  mount(AccountStatsModal, {
    props: {
      show: false,
      account: { id: 8, name: 'Pool account', status: 'active' },
      loadStats
    },
    global: {
      stubs: {
        BaseDialog: {
          props: ['show'],
          template: '<section v-if="show"><slot /></section>'
        },
        Icon: true,
        LoadingSpinner: true,
        ModelDistributionChart: true,
        EndpointDistributionChart: true
      }
    }
  })

describe('AccountStatsModal scope', () => {
  it('uses the scoped loader and never falls back to an admin API on failure', async () => {
    adminStats.mockClear()
    const scoped = vi.fn().mockRejectedValue(new Error('permission revoked'))
    const log = vi.spyOn(console, 'error').mockImplementation(() => {})
    const wrapper = render(scoped)
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(scoped).toHaveBeenCalledWith(8)
    expect(adminStats).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain(
      'quotaPools.loadFailed'
    )
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(scoped).toHaveBeenCalledTimes(2)
    expect(adminStats).not.toHaveBeenCalled()
    log.mockRestore()
  })

  it('retains the existing admin loader when no scoped loader is supplied', async () => {
    adminStats.mockReset().mockResolvedValue(null)
    const wrapper = render()
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(adminStats).toHaveBeenCalledWith(8, 30)
    expect(wrapper.text()).toContain('admin.accounts.stats.noData')
  })

  it('shows loading until the local statistics request finishes', async () => {
    const scoped = vi.fn(() => new Promise<never>(() => {}))
    const wrapper = render(scoped)
    await wrapper.setProps({ show: true })
    expect(wrapper.findComponent({ name: 'LoadingSpinner' }).exists()).toBe(
      true
    )
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
