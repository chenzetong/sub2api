<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
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
    <div class="space-y-5 p-4 md:p-6">
      <div>
        <h1 class="text-xl font-semibold">
          {{ t('quotaPools.accountsTitle') }}
        </h1>
        <p class="mt-1 text-sm text-gray-500">
          {{ t('quotaPools.readOnlyDescription') }}
        </p>
      </div>
      <label class="block max-w-md"
        ><span class="sr-only">{{ t('quotaPools.search') }}</span
        ><input
          v-model="search"
          class="input w-full"
          :placeholder="t('quotaPools.search')"
      /></label>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <div
        v-else-if="error"
        role="alert"
        class="rounded-lg bg-red-50 p-4 text-red-700"
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
          class="card space-y-4 p-5"
        >
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div>
              <h2 class="font-semibold">{{ account.name }}</h2>
              <p class="text-sm text-gray-500">
                {{ account.pool_name }} · {{ account.share_percent }}% · OpenAI
                OAuth
              </p>
            </div>
            <span
              class="rounded bg-gray-100 px-2 py-1 text-xs dark:bg-gray-800"
              >{{ account.status }}</span
            >
          </div>
          <OpenAIUsageWindows
            :five-hour="account.five_hour"
            :seven-day="account.seven_day"
            :estimate="account.estimate"
          />
          <button class="btn btn-secondary" @click="selected = account">
            {{ t('quotaPools.details') }}
          </button>
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
