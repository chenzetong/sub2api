<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
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
    <div class="space-y-5 p-4 md:p-6">
      <div>
        <h1 class="text-xl font-semibold">{{ t('quotaPools.title') }}</h1>
        <p class="mt-1 text-sm text-gray-500">
          {{ t('quotaPools.description') }}
        </p>
      </div>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <div
        v-if="error"
        role="alert"
        class="rounded-lg bg-red-50 p-4 text-red-700"
      >
        {{ error }}
        <button v-if="!data" class="btn btn-secondary" @click="load">
          {{ t('quotaPools.retry') }}
        </button>
      </div>
      <p
        v-if="message"
        role="status"
        class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800"
      >
        {{ message }}
      </p>
      <template v-if="data">
        <fieldset :disabled="busy" class="card space-y-4 p-5">
          <legend class="font-semibold">{{ t('quotaPools.schedule') }}</legend>
          <p class="text-sm text-amber-700">
            {{ t('quotaPools.balanceWarning') }}
          </p>
          <div class="flex flex-wrap items-center gap-4">
            <label class="flex items-center gap-2"
              ><input v-model="data.settings.enabled" type="checkbox" />{{
                t('quotaPools.autoSync')
              }}</label
            >
            <label class="flex items-center gap-2"
              >{{ t('quotaPools.interval')
              }}<input
                v-model.number="data.settings.interval_minutes"
                type="number"
                min="5"
                max="1440"
                step="1"
                class="input w-24"
            /></label>
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
          <p v-if="data.settings.next_run_at" class="text-sm text-gray-500">
            {{ t('quotaPools.nextRun') }}: {{ date(data.settings.next_run_at) }}
          </p>
        </fieldset>
        <fieldset :disabled="busy" class="space-y-4">
          <div class="flex flex-wrap gap-2">
            <button class="btn btn-secondary" @click="addPool">
              {{ t('quotaPools.addPool') }}
            </button>
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
            <button
              class="btn btn-primary"
              :disabled="!dirty || invalid"
              @click="save"
            >
              {{ t('quotaPools.saveAllocation') }}
            </button>
            <button
              class="btn btn-secondary"
              :disabled="dirty"
              @click="showPreview"
            >
              {{ t('quotaPools.preview') }}
            </button>
          </div>
          <p v-if="dirty" class="text-sm text-amber-700">
            {{ t('quotaPools.unsaved') }}
          </p>
          <p v-if="!pools.length" class="card p-8 text-center text-gray-500">
            {{ t('quotaPools.noPools') }}
          </p>
          <section
            v-for="(pool, index) in pools"
            :key="`${pool.id}-${index}`"
            class="card space-y-4 p-5"
          >
            <div class="flex flex-wrap items-center gap-3">
              <input
                v-model="selectedPools"
                type="checkbox"
                :value="index"
                :aria-label="t('quotaPools.selectPool', { name: pool.name })"
              />
              <input
                v-model="pool.name"
                class="input min-w-0 flex-1"
                maxlength="160"
                :aria-label="t('quotaPools.poolName')"
              />
              <span
                :class="total(pool) > 100 ? 'text-red-600' : 'text-gray-500'"
                >{{ total(pool) }}% / 100%</span
              >
              <button class="btn btn-secondary" @click="removePool(index)">
                {{ t('quotaPools.removePool') }}
              </button>
            </div>
            <div class="grid gap-4 lg:grid-cols-2">
              <div class="space-y-2">
                <label
                  :for="`pool-accounts-${index}`"
                  class="block text-sm font-medium"
                  >{{ t('quotaPools.accounts') }}</label
                >
                <select
                  :id="`pool-accounts-${index}`"
                  v-model="pool.account_ids"
                  multiple
                  class="input h-40 w-full"
                >
                  <option
                    v-for="account in data.accounts"
                    :key="account.id"
                    :value="account.id"
                    :disabled="usedElsewhere(account.id, pool)"
                  >
                    {{ account.name }} ·
                    {{ money(account.estimate?.total_cost ?? null) }}
                  </option>
                </select>
                <div
                  v-if="pool.account_ids.length > 1"
                  class="flex flex-wrap gap-3"
                >
                  <label
                    v-for="id in pool.account_ids"
                    :key="id"
                    class="flex items-center gap-1 text-sm"
                    ><input v-model="splitIDs" type="checkbox" :value="id" />{{
                      data.accounts.find((a) => a.id === id)?.name || id
                    }}</label
                  >
                </div>
              </div>
              <div class="space-y-2">
                <label
                  :for="`pool-user-${index}`"
                  class="block text-sm font-medium"
                  >{{ t('quotaPools.shares') }}</label
                >
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
                <div
                  v-for="(share, shareIndex) in pool.shares"
                  :key="share.user_id"
                  class="flex items-center gap-2"
                >
                  <label
                    :for="`share-${index}-${share.user_id}`"
                    class="min-w-0 flex-1 truncate"
                    >{{
                      data.users.find((u) => u.id === share.user_id)?.name ||
                      share.user_id
                    }}</label
                  >
                  <input
                    :id="`share-${index}-${share.user_id}`"
                    v-model.number="share.share_percent"
                    type="number"
                    min="0"
                    max="100"
                    step="0.001"
                    class="input w-28"
                  /><span>%</span>
                  <button
                    class="btn btn-secondary"
                    :aria-label="t('quotaPools.removeShare')"
                    @click="pool.shares.splice(shareIndex, 1)"
                  >
                    ×
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
