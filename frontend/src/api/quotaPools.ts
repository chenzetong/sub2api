import { apiClient } from './client'
import type {
  Account,
  AccountUsageStatsResponse,
  QuotaEstimate,
  UsageProgress
} from '@/types'
export interface PoolShare {
  user_id: number
  share_percent: number
}
export interface QuotaPool {
  id: number
  name: string
  revision: number
  account_ids: number[]
  shares: PoolShare[]
}
export interface PoolAccount {
  id: number
  name: string
  platform: string
  type: string
  status: Account['status']
  pool_id: number
  pool_name: string
  share_percent: number
  five_hour: UsageProgress | null
  seven_day: UsageProgress | null
  estimate: QuotaEstimate | null
}
export interface PoolUser {
  id: number
  name: string
  balance: number
  frozen_balance: number
}
export interface PoolSettings {
  enabled: boolean
  interval_minutes: number
  next_run_at: string | null
}
export interface PoolAllocation {
  revision: number
  pools: QuotaPool[]
  accounts: PoolAccount[]
  users: PoolUser[]
  settings: PoolSettings
}
export interface PoolUserResult {
  user_id: number
  name: string
  current_balance: number
  frozen_balance: number
  entitlement: number
  consumed: number
  target_balance: number | null
  status: string
  reason?: string
  release: boolean
}
export interface PoolPreview {
  revision: number
  accounts: PoolAccount[]
  users: PoolUserResult[]
  created_at: string
  operation_key?: string
  status: string
  error?: string
}
export interface PoolRun {
  id: number
  source: string
  operation_key: string
  status: string
  created_at: string
  result: PoolPreview
}

const root = '/admin/quota-pools'
export const quotaPoolsAPI = {
  async allocation() {
    return (await apiClient.get<PoolAllocation>(root)).data
  },
  async save(revision: number, pools: QuotaPool[]) {
    return (await apiClient.put<PoolAllocation>(root, { revision, pools })).data
  },
  async settings(settings: PoolSettings) {
    return (await apiClient.put<PoolSettings>(`${root}/settings`, settings))
      .data
  },
  async preview() {
    return (await apiClient.get<PoolPreview>(`${root}/preview`)).data
  },
  async sync(operation_key: string, revision: number) {
    return (
      await apiClient.post<PoolPreview>(`${root}/sync`, {
        operation_key,
        revision
      })
    ).data
  },
  async runs() {
    return (await apiClient.get<PoolRun[]>(`${root}/runs`)).data
  }
}
export const poolAccountsAPI = {
  async list() {
    return (await apiClient.get<PoolAccount[]>('/pool-accounts')).data
  },
  async stats(id: number) {
    return (
      await apiClient.get<AccountUsageStatsResponse>(
        `/pool-accounts/${id}/stats`
      )
    ).data
  }
}
