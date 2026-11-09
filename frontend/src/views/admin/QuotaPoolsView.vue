<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import {
  quotaPoolsAPI,
  type PoolAllocation,
  type PoolPreview,
  type PoolRun,
  type QuotaPool
} from '@/api/quotaPools'

const { t } = useI18n()
const data = ref<PoolAllocation | null>(null)
const pools = ref<QuotaPool[]>([])
const saved = ref('')
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const message = ref('')
const selectedPools = ref<number[]>([])
const splitIDs = ref<number[]>([])
const preview = ref<PoolPreview | null>(null)
const runs = ref<PoolRun[]>([])
const syncKey = ref('')
const accountQueries = ref<Record<number, string>>({})
const assignedAccounts = computed(
  () => new Set(pools.value.flatMap((pool) => pool.account_ids)).size
)
const participants = computed(
  () =>
    new Set(
      pools.value.flatMap((pool) =>
        pool.shares
          .filter((share) => share.share_percent > 0)
          .map((share) => share.user_id)
      )
    ).size
)
function filteredAccounts(index: number) {
  const query = (accountQueries.value[index] || '').trim().toLowerCase()
  return (
    data.value?.accounts.filter((account) =>
      account.name.toLowerCase().includes(query)
    ) || []
  )
}
function toggleAccount(pool: QuotaPool, accountID: number) {
  if (pool.account_ids.includes(accountID)) {
    pool.account_ids = pool.account_ids.filter((id) => id !== accountID)
    splitIDs.value = splitIDs.value.filter((id) => id !== accountID)
  } else {
    pool.account_ids.push(accountID)
  }
}
const dirty = computed(() => JSON.stringify(pools.value) !== saved.value)
const invalid = computed(() =>
  pools.value.some(
    (p) =>
      !p.name.trim() ||
      !p.account_ids.length ||
      total(p) > 100 ||
      p.shares.some(
        (s) =>
          !Number.isFinite(s.share_percent) ||
          s.share_percent < 0 ||
          s.share_percent > 100 ||
          Math.abs(
            s.share_percent * 1000 - Math.round(s.share_percent * 1000)
          ) > 1e-6
      )
  )
)
const money = (v: number | null) =>
  v == null ? '—' : `$${v.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')}`
const date = (value: string) => new Date(value).toLocaleString()
const total = (p: QuotaPool) =>
  Math.round(p.shares.reduce((sum, s) => sum + s.share_percent, 0) * 1000) /
  1000
const fail = (e: unknown) => {
  error.value =
    (e as { message?: string }).message || t('quotaPools.actionFailed')
}
function accept(value: PoolAllocation) {
  data.value = value
  pools.value = structuredClone(value.pools)
  saved.value = JSON.stringify(pools.value)
  selectedPools.value = []
  splitIDs.value = []
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    const [allocation, history] = await Promise.all([
      quotaPoolsAPI.allocation(),
      quotaPoolsAPI.runs()
    ])
    accept(allocation)
    runs.value = history
  } catch (e) {
    fail(e)
  } finally {
    loading.value = false
  }
}
async function save() {
  if (!data.value || invalid.value) return
  busy.value = true
  error.value = ''
  message.value = ''
  try {
    accept(await quotaPoolsAPI.save(data.value.revision, pools.value))
    preview.value = null
    message.value = t('quotaPools.saved')
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
async function saveSettings() {
  if (!data.value) return
  busy.value = true
  error.value = ''
  try {
    data.value.settings = await quotaPoolsAPI.settings(data.value.settings)
    message.value = t('quotaPools.settingsSaved')
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
async function showPreview() {
  busy.value = true
  error.value = ''
  try {
    preview.value = await quotaPoolsAPI.preview()
    syncKey.value = crypto.randomUUID()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
async function sync() {
  if (!preview.value || dirty.value) return
  busy.value = true
  error.value = ''
  try {
    preview.value = await quotaPoolsAPI.sync(
      syncKey.value,
      preview.value.revision
    )
    runs.value = await quotaPoolsAPI.runs()
    message.value = t('quotaPools.synced')
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}
function addPool() {
  pools.value.push({
    id: 0,
    name: `${t('quotaPools.pool')} ${pools.value.length + 1}`,
    revision: 0,
    account_ids: [],
    shares: []
  })
}
function usedElsewhere(accountID: number, pool: QuotaPool) {
  return pools.value.some(
    (p) => p !== pool && p.account_ids.includes(accountID)
  )
}
function addUser(pool: QuotaPool, event: Event) {
  const select = event.target as HTMLSelectElement
  if (select.value)
    pool.shares.push({ user_id: Number(select.value), share_percent: 0 })
  select.value = ''
}
function removePool(index: number) {
  pools.value.splice(index, 1)
  selectedPools.value = []
}
function merge() {
  const selected = pools.value.filter((_, index) =>
    selectedPools.value.includes(index)
  )
  if (selected.length < 2) return
  const signature = (p: QuotaPool) =>
    JSON.stringify(
      [...p.shares]
        .filter((s) => s.share_percent > 0)
        .sort((a, b) => a.user_id - b.user_id)
    )
  const matching = selected.every(
    (p) => signature(p) === signature(selected[0]!)
  )
  const first = selected[0]!
  const merged = {
    ...first,
    name: `${t('quotaPools.pool')} ${first.name}`,
    account_ids: selected.flatMap((p) => p.account_ids),
    shares: matching ? first.shares.map((s) => ({ ...s })) : []
  }
  pools.value = pools.value.filter((p) => !selected.includes(p))
  pools.value.push(merged)
  selectedPools.value = []
  if (!matching) message.value = t('quotaPools.mergeCleared')
}
function split() {
  const next: QuotaPool[] = []
  for (const pool of pools.value) {
    const selected = pool.account_ids.filter((id) =>
      splitIDs.value.includes(id)
    )
    if (pool.account_ids.length < 2 || !selected.length) {
      next.push(pool)
      continue
    }
    const remaining = pool.account_ids.filter((id) => !selected.includes(id))
    if (remaining.length) next.push({ ...pool, account_ids: remaining })
    for (const id of selected)
      next.push({
        id: 0,
        name: data.value?.accounts.find((a) => a.id === id)?.name || String(id),
        revision: 0,
        account_ids: [id],
        shares: pool.shares.map((s) => ({ ...s }))
      })
  }
  pools.value = next
  splitIDs.value = []
  selectedPools.value = []
}
onMounted(load)
</script>

<template>
  <AppLayout>
    <div
      class="mx-auto max-w-7xl space-y-6 p-4 text-gray-900 dark:text-gray-100 md:p-6"
    >
      <div>
        <h1 class="text-2xl font-semibold tracking-tight">
          {{ t('quotaPools.title') }}
        </h1>
        <p
          class="mt-2 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400"
        >
          {{ t('quotaPools.description') }}
        </p>
      </div>
      <p
        v-if="loading"
        role="status"
        class="card animate-pulse p-8 text-center text-sm text-gray-500"
      >
        {{ t('common.loading') }}
      </p>
      <div
        v-if="error"
        role="alert"
        class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300"
      >
        {{ error }}
        <button v-if="!data" class="btn btn-secondary" @click="load">
          {{ t('quotaPools.retry') }}
        </button>
      </div>
      <p
        v-if="message"
        role="status"
        class="rounded-xl border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-900 dark:bg-blue-950/30 dark:text-blue-200"
      >
        {{ message }}
      </p>
      <template v-if="data">
        <div class="grid grid-cols-3 gap-3 sm:gap-4">
          <div class="card p-4 sm:p-5">
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('quotaPools.poolCount') }}
            </p>
            <p class="mt-2 text-2xl font-semibold tabular-nums">
              {{ pools.length }}
            </p>
          </div>
          <div class="card p-4 sm:p-5">
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('quotaPools.assignedAccounts') }}
            </p>
            <p class="mt-2 text-2xl font-semibold tabular-nums">
              {{ assignedAccounts
              }}<span class="ml-1 text-sm font-normal text-gray-400"
                >/ {{ data.accounts.length }}</span
              >
            </p>
          </div>
          <div class="card p-4 sm:p-5">
            <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
              {{ t('quotaPools.participants') }}
            </p>
            <p class="mt-2 text-2xl font-semibold tabular-nums">
              {{ participants }}
            </p>
          </div>
        </div>
        <fieldset :disabled="busy" class="card min-w-0 p-5">
          <div class="flex flex-wrap items-center justify-between gap-4">
            <div>
              <h2 class="font-semibold">{{ t('quotaPools.schedule') }}</h2>
              <p
                v-if="data.settings.next_run_at"
                class="mt-1 text-xs text-gray-500 dark:text-gray-400"
              >
                {{ t('quotaPools.nextRun') }}:
                {{ date(data.settings.next_run_at) }}
              </p>
            </div>
            <div class="flex flex-wrap items-center gap-4">
              <div class="flex items-center gap-2 text-sm">
                <Toggle
                  v-model="data.settings.enabled"
                  :aria-label="t('quotaPools.autoSync')"
                />
                <span>{{ t('quotaPools.autoSync') }}</span>
              </div>
              <label class="flex items-center gap-2 text-sm">
                {{ t('quotaPools.interval') }}
                <input
                  v-model.number="data.settings.interval_minutes"
                  type="number"
                  min="5"
                  max="1440"
                  step="1"
                  class="input w-24"
                />
              </label>
              <button
                class="btn btn-secondary"
                :disabled="
                  !Number.isInteger(data.settings.interval_minutes) ||
                  data.settings.interval_minutes < 5 ||
                  data.settings.interval_minutes > 1440
                "
                @click="saveSettings"
              >
                {{ t('quotaPools.saveSettings') }}
              </button>
            </div>
          </div>
          <p
            class="mt-4 rounded-lg bg-amber-50 px-3 py-2.5 text-xs leading-5 text-amber-800 dark:bg-amber-950/30 dark:text-amber-300"
          >
            {{ t('quotaPools.balanceWarning') }}
          </p>
        </fieldset>
        <fieldset :disabled="busy" class="min-w-0 space-y-4">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="font-semibold">{{ t('quotaPools.allocation') }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('quotaPools.workflowHint') }}
              </p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button class="btn btn-secondary" @click="addPool">
                <Icon name="plus" size="sm" />{{ t('quotaPools.addPool') }}
              </button>
              <button
                class="btn btn-secondary"
                :disabled="dirty"
                @click="showPreview"
              >
                {{ t('quotaPools.preview') }}
              </button>
              <button
                class="btn btn-primary"
                :disabled="!dirty || invalid"
                @click="save"
              >
                {{ t('quotaPools.saveAllocation') }}
              </button>
            </div>
          </div>
          <div
            v-if="selectedPools.length || splitIDs.length"
            class="flex flex-wrap items-center gap-2 rounded-xl border border-primary-200 bg-primary-50 p-3 dark:border-primary-800 dark:bg-primary-950/20"
          >
            <button
              class="btn btn-secondary"
              :disabled="selectedPools.length < 2"
              @click="merge"
            >
              {{ t('quotaPools.merge') }}
            </button>
            <button
              class="btn btn-secondary"
              :disabled="!splitIDs.length"
              @click="split"
            >
              {{ t('quotaPools.split') }}
            </button>
          </div>
          <p v-if="dirty" class="text-sm text-amber-700 dark:text-amber-300">
            {{ t('quotaPools.unsaved') }}
          </p>
          <p
            v-if="!pools.length"
            class="card border-dashed p-10 text-center text-sm text-gray-500 dark:text-gray-400"
          >
            {{ t('quotaPools.noPools') }}
          </p>
          <section
            v-for="(pool, index) in pools"
            :key="`${pool.id}-${index}`"
            class="card overflow-hidden"
          >
            <div
              class="flex flex-wrap items-center gap-3 border-b border-gray-100 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/50 sm:px-5"
            >
              <input
                v-model="selectedPools"
                type="checkbox"
                class="pool-checkbox"
                :value="index"
                :aria-label="t('quotaPools.selectPool', { name: pool.name })"
              />
              <input
                v-model="pool.name"
                class="input min-w-0 flex-1 font-medium"
                maxlength="160"
                :aria-label="t('quotaPools.poolName')"
              />
              <button
                class="btn btn-secondary text-red-600 dark:text-red-400"
                @click="removePool(index)"
              >
                {{ t('quotaPools.removePool') }}
              </button>
            </div>
            <div class="grid gap-6 p-4 sm:p-5 lg:grid-cols-2">
              <div class="min-w-0 space-y-3">
                <div class="flex items-center justify-between gap-2">
                  <h3
                    :id="`pool-accounts-${index}`"
                    class="text-sm font-semibold"
                  >
                    {{ t('quotaPools.accounts') }}
                  </h3>
                  <span
                    class="rounded-full bg-primary-50 px-2.5 py-1 text-xs font-medium text-primary-700 dark:bg-primary-950/30 dark:text-primary-300"
                    >{{
                      t('quotaPools.selectedCount', {
                        count: pool.account_ids.length
                      })
                    }}</span
                  >
                </div>
                <label class="relative block">
                  <span class="sr-only">{{
                    t('quotaPools.searchAccounts')
                  }}</span>
                  <Icon
                    name="search"
                    size="sm"
                    class="pointer-events-none absolute left-3 top-3 text-gray-400"
                  />
                  <input
                    v-model="accountQueries[index]"
                    type="search"
                    class="input w-full pl-9"
                    :placeholder="t('quotaPools.searchAccounts')"
                  />
                </label>
                <div
                  class="max-h-72 overflow-y-auto rounded-xl border border-gray-200 dark:border-dark-600"
                  role="group"
                  :aria-labelledby="`pool-accounts-${index}`"
                >
                  <label
                    v-for="account in filteredAccounts(index)"
                    :key="account.id"
                    class="flex items-center gap-3 border-b border-gray-100 p-3 last:border-0 dark:border-dark-700"
                    :class="
                      usedElsewhere(account.id, pool)
                        ? 'cursor-not-allowed bg-gray-50 opacity-60 dark:bg-dark-800'
                        : pool.account_ids.includes(account.id)
                          ? 'cursor-pointer bg-primary-50/70 dark:bg-primary-950/20'
                          : 'cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700'
                    "
                  >
                    <input
                      type="checkbox"
                      class="pool-checkbox"
                      :checked="pool.account_ids.includes(account.id)"
                      :disabled="usedElsewhere(account.id, pool)"
                      :aria-label="
                        t('quotaPools.selectAccount', { name: account.name })
                      "
                      @change="toggleAccount(pool, account.id)"
                    />
                    <span class="min-w-0 flex-1">
                      <span class="block break-words text-sm font-medium">{{
                        account.name
                      }}</span>
                      <span
                        class="mt-1 block text-xs text-gray-500 dark:text-gray-400"
                        >{{
                          usedElsewhere(account.id, pool)
                            ? t('quotaPools.assignedElsewhere')
                            : t(
                                `quotaPools.sources.${account.estimate?.source || 'unavailable'}`
                              )
                        }}</span
                      >
                    </span>
                    <span class="shrink-0 text-right">
                      <span class="block text-sm font-medium tabular-nums">{{
                        money(account.estimate?.total_cost ?? null)
                      }}</span>
                      <span class="mt-1 block text-xs text-gray-400">{{
                        t('quotaPools.estimatedQuota')
                      }}</span>
                    </span>
                  </label>
                  <p
                    v-if="!filteredAccounts(index).length"
                    class="p-6 text-center text-sm text-gray-500 dark:text-gray-400"
                  >
                    {{
                      data.accounts.length
                        ? t('quotaPools.noResults')
                        : t('quotaPools.noSourceAccounts')
                    }}
                  </p>
                </div>
                <p
                  v-if="!pool.account_ids.length"
                  class="text-xs text-amber-700 dark:text-amber-300"
                >
                  {{ t('quotaPools.selectAccountHint') }}
                </p>
                <div
                  v-if="pool.account_ids.length > 1"
                  class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800"
                >
                  <p class="mb-2 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('quotaPools.splitHint') }}
                  </p>
                  <div class="flex flex-wrap gap-2">
                    <label
                      v-for="id in pool.account_ids"
                      :key="id"
                      class="flex max-w-full cursor-pointer items-center gap-2 rounded-md border border-gray-200 bg-white px-2.5 py-1.5 text-xs dark:border-dark-600 dark:bg-dark-700"
                    >
                      <input
                        v-model="splitIDs"
                        type="checkbox"
                        class="pool-checkbox"
                        :value="id"
                      />
                      <span class="truncate">{{
                        data.accounts.find((a) => a.id === id)?.name || id
                      }}</span>
                    </label>
                  </div>
                </div>
              </div>
              <div class="min-w-0 space-y-3">
                <div class="flex items-center justify-between gap-2">
                  <label
                    :for="`pool-user-${index}`"
                    class="text-sm font-semibold"
                    >{{ t('quotaPools.shares') }}</label
                  >
                  <span
                    class="text-sm font-semibold tabular-nums"
                    :class="
                      total(pool) > 100
                        ? 'text-red-600 dark:text-red-400'
                        : 'text-primary-600 dark:text-primary-400'
                    "
                    >{{ total(pool) }}%
                    <span class="font-normal text-gray-400">/ 100%</span></span
                  >
                </div>
                <div
                  class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"
                  role="progressbar"
                  :aria-label="t('quotaPools.shares')"
                  :aria-valuenow="Math.min(100, Math.max(0, total(pool) || 0))"
                  :aria-valuetext="`${total(pool)}%`"
                  :aria-valuemin="0"
                  :aria-valuemax="100"
                >
                  <div
                    class="h-full rounded-full transition-all"
                    :class="total(pool) > 100 ? 'bg-red-500' : 'bg-primary-500'"
                    :style="{
                      width: `${Math.min(100, Math.max(0, total(pool) || 0))}%`
                    }"
                  />
                </div>
                <p
                  class="text-xs"
                  :class="
                    total(pool) > 100
                      ? 'text-red-600 dark:text-red-400'
                      : 'text-gray-500 dark:text-gray-400'
                  "
                >
                  {{
                    total(pool) > 100
                      ? t('quotaPools.overAllocated')
                      : t('quotaPools.unallocated', {
                          percent: Math.round((100 - total(pool)) * 1000) / 1000
                        })
                  }}
                </p>
                <select
                  :id="`pool-user-${index}`"
                  class="input w-full"
                  @change="addUser(pool, $event)"
                >
                  <option value="">{{ t('quotaPools.addUser') }}</option>
                  <option
                    v-for="user in data.users.filter(
                      (u) => !pool.shares.some((s) => s.user_id === u.id)
                    )"
                    :key="user.id"
                    :value="user.id"
                  >
                    {{ user.name }}
                  </option>
                </select>
                <p
                  v-if="!pool.shares.length"
                  class="rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
                >
                  {{ t('quotaPools.noShares') }}
                </p>
                <div
                  v-for="(share, shareIndex) in pool.shares"
                  :key="share.user_id"
                  class="flex items-center gap-2 rounded-xl border border-gray-100 p-3 dark:border-dark-700"
                >
                  <label
                    :for="`share-${index}-${share.user_id}`"
                    class="min-w-0 flex-1 break-words text-sm font-medium"
                    >{{
                      data.users.find((u) => u.id === share.user_id)?.name ||
                      share.user_id
                    }}</label
                  >
                  <div class="relative w-24 shrink-0 sm:w-28">
                    <input
                      :id="`share-${index}-${share.user_id}`"
                      v-model.number="share.share_percent"
                      type="number"
                      min="0"
                      max="100"
                      step="0.001"
                      class="input w-full pr-7 tabular-nums"
                    />
                    <span
                      class="pointer-events-none absolute right-3 top-2.5 text-sm text-gray-400"
                      >%</span
                    >
                  </div>
                  <button
                    class="rounded-lg p-2 text-gray-400 hover:bg-red-50 hover:text-red-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-red-950/30"
                    :aria-label="t('quotaPools.removeShare')"
                    @click="pool.shares.splice(shareIndex, 1)"
                  >
                    <Icon name="x" size="sm" />
                  </button>
                </div>
              </div>
            </div>
          </section>
        </fieldset>
        <section v-if="preview" class="card space-y-4 p-5">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h2 class="font-semibold">{{ t('quotaPools.preview') }}</h2>
            <button
              class="btn btn-primary"
              :disabled="
                busy ||
                dirty ||
                preview.revision !== data.revision ||
                preview.status !== 'preview'
              "
              @click="sync"
            >
              {{ t('quotaPools.syncNow') }}
            </button>
          </div>
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead>
                <tr>
                  <th class="p-2">{{ t('quotaPools.user') }}</th>
                  <th class="p-2">{{ t('quotaPools.entitlement') }}</th>
                  <th class="p-2">{{ t('quotaPools.consumed') }}</th>
                  <th class="p-2">{{ t('quotaPools.frozen') }}</th>
                  <th class="p-2">{{ t('quotaPools.balance') }}</th>
                  <th class="p-2">{{ t('quotaPools.target') }}</th>
                  <th class="p-2">{{ t('quotaPools.status') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="user in preview.users"
                  :key="user.user_id"
                  class="border-t dark:border-gray-700"
                >
                  <td class="p-2">{{ user.name }}</td>
                  <td class="p-2">{{ money(user.entitlement) }}</td>
                  <td class="p-2">{{ money(user.consumed) }}</td>
                  <td class="p-2">{{ money(user.frozen_balance) }}</td>
                  <td class="p-2">{{ money(user.current_balance) }}</td>
                  <td class="p-2">{{ money(user.target_balance) }}</td>
                  <td class="p-2">
                    {{
                      user.reason
                        ? t(`quotaPools.reasons.${user.reason}`)
                        : t(`quotaPools.states.${user.status}`)
                    }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="!preview.users.length" class="text-sm text-gray-500">
            {{ t('quotaPools.noUsers') }}
          </p>
          <div
            v-for="account in preview.accounts"
            :key="account.id"
            class="flex flex-wrap justify-between gap-2 border-t pt-2 text-sm dark:border-gray-700"
          >
            <span>{{ account.pool_name }} · {{ account.name }}</span
            ><span
              >{{ money(account.estimate?.total_cost ?? null) }} ·
              {{
                account.estimate?.reason
                  ? t(`quotaPools.reasons.${account.estimate.reason}`)
                  : t(
                      `quotaPools.sources.${account.estimate?.source || 'unavailable'}`
                    )
              }}</span
            >
          </div>
        </section>
        <section class="card space-y-3 p-5">
          <h2 class="font-semibold">{{ t('quotaPools.history') }}</h2>
          <p v-if="!runs.length" class="text-sm text-gray-500">
            {{ t('quotaPools.noHistory') }}
          </p>
          <details
            v-for="run in runs"
            :key="run.id"
            class="border-t py-2 dark:border-gray-700"
          >
            <summary class="cursor-pointer text-sm">
              {{ date(run.created_at) }} ·
              {{ t(`quotaPools.states.${run.status}`) }} ·
              {{ t(`quotaPools.runSources.${run.source}`) }}
            </summary>
            <p v-if="run.result.error" class="mt-2 text-sm text-red-600">
              {{ t(`quotaPools.reasons.${run.result.error}`) }}
            </p>
            <ul class="mt-2 space-y-1 text-sm">
              <li v-for="user in run.result.users" :key="user.user_id">
                {{ user.name }}: {{ money(user.current_balance) }} →
                {{ money(user.target_balance) }} ·
                {{
                  user.reason
                    ? t(`quotaPools.reasons.${user.reason}`)
                    : t(`quotaPools.states.${user.status}`)
                }}
              </li>
            </ul>
          </details>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<style scoped>
.pool-checkbox {
  @apply h-4 w-4 shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500 disabled:cursor-not-allowed dark:border-dark-500 dark:bg-dark-700;
}
</style>
