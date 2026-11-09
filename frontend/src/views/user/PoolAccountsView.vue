<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import OpenAIUsageWindows from '@/components/account/OpenAIUsageWindows.vue'
import AccountStatsModal from '@/components/admin/account/AccountStatsModal.vue'
import { poolAccountsAPI, type PoolAccount } from '@/api/quotaPools'

const { t } = useI18n()
const accounts = ref<PoolAccount[]>([])
const loading = ref(true)
const error = ref('')
const search = ref('')
const selected = ref<PoolAccount | null>(null)
const filtered = computed(() =>
  accounts.value.filter((a) =>
    `${a.name} ${a.pool_name}`
      .toLowerCase()
      .includes(search.value.toLowerCase())
  )
)
async function load() {
  loading.value = true
  error.value = ''
  try {
    accounts.value = await poolAccountsAPI.list()
  } catch (e) {
    error.value =
      (e as { message?: string }).message || t('quotaPools.loadFailed')
  } finally {
    loading.value = false
  }
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
          {{ t('quotaPools.accountsTitle') }}
        </h1>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">
          {{ t('quotaPools.readOnlyDescription') }}
        </p>
      </div>
      <label class="relative block max-w-md"
        ><span class="sr-only">{{ t('quotaPools.search') }}</span
        ><Icon
          name="search"
          size="sm"
          class="pointer-events-none absolute left-3 top-3 text-gray-400" /><input
          v-model="search"
          class="input w-full pl-9"
          :placeholder="t('quotaPools.search')"
      /></label>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <div
        v-else-if="error"
        role="alert"
        class="rounded-xl bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300"
      >
        {{ error }}
        <button class="btn btn-secondary ml-3" @click="load">
          {{ t('quotaPools.retry') }}
        </button>
      </div>
      <p
        v-else-if="!filtered.length"
        class="card p-8 text-center text-gray-500"
      >
        {{
          accounts.length ? t('quotaPools.noResults') : t('quotaPools.noAccess')
        }}
      </p>
      <div v-else class="grid gap-4 xl:grid-cols-2">
        <article
          v-for="account in filtered"
          :key="account.id"
          class="card overflow-hidden"
        >
          <div
            class="flex items-start justify-between gap-3 border-b border-gray-100 p-5 dark:border-dark-700"
          >
            <div class="min-w-0">
              <h2 class="break-words font-semibold">{{ account.name }}</h2>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ account.pool_name }} · OpenAI OAuth
              </p>
            </div>
            <span
              class="shrink-0 rounded-full px-2.5 py-1 text-xs font-medium"
              :class="
                account.status === 'active'
                  ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
              "
              >{{ t(`quotaPools.accountStates.${account.status}`) }}</span
            >
          </div>
          <div class="space-y-4 p-5">
            <div class="flex items-center justify-between text-sm">
              <span class="text-gray-500 dark:text-gray-400">{{
                t('quotaPools.shares')
              }}</span
              ><span
                class="font-semibold tabular-nums text-primary-600 dark:text-primary-400"
                >{{ account.share_percent }}%</span
              >
            </div>
            <OpenAIUsageWindows
              :five-hour="account.five_hour"
              :seven-day="account.seven_day"
              :estimate="account.estimate"
            />
            <button
              class="btn btn-secondary w-full"
              @click="selected = account"
            >
              {{ t('quotaPools.details') }}
            </button>
          </div>
        </article>
      </div>
      <AccountStatsModal
        :show="selected !== null"
        :account="selected"
        :load-stats="poolAccountsAPI.stats"
        @close="selected = null"
      />
    </div>
  </AppLayout>
</template>
