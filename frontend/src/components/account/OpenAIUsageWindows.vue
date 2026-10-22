<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import UsageProgressBar from './UsageProgressBar.vue'
import type { QuotaEstimate, UsageProgress } from '@/types'

defineProps<{
  fiveHour?: UsageProgress | null
  sevenDay?: UsageProgress | null
  estimate?: QuotaEstimate | null
}>()
const { t } = useI18n()
</script>

<template>
  <div class="space-y-1">
    <UsageProgressBar
      v-if="fiveHour"
      label="5h"
      :utilization="fiveHour.utilization"
      :resets-at="fiveHour.resets_at"
      :window-stats="fiveHour.window_stats"
      :show-now-when-idle="true"
      color="indigo"
    />
    <UsageProgressBar
      v-if="sevenDay"
      label="7d"
      :utilization="sevenDay.utilization"
      :resets-at="sevenDay.resets_at"
      :window-stats="sevenDay.window_stats"
      :estimated-total-cost="estimate?.total_cost"
      :show-now-when-idle="true"
      color="emerald"
    />
    <span
      v-if="
        estimate?.total_cost != null &&
        !sevenDay?.window_stats?.requests &&
        !sevenDay?.window_stats?.tokens
      "
      data-test="estimated-total-cost"
      class="block text-xs text-gray-500"
      >{{
        t('admin.accounts.usageWindow.estimatedTotalCost', {
          cost: estimate.total_cost.toFixed(2)
        })
      }}</span
    >
    <span
      v-if="estimate?.source === 'last_valid'"
      class="text-xs text-amber-600"
      >{{ t('quotaPools.lastValid') }}</span
    >
    <span
      v-else-if="estimate?.source === 'unavailable'"
      class="text-xs text-gray-500"
      >{{ t('quotaPools.pending') }} ·
      {{
        t(`quotaPools.reasons.${estimate.reason || 'invalid_prediction'}`)
      }}</span
    >
    <span v-else-if="!fiveHour && !sevenDay" class="text-sm text-gray-500">{{
      t('quotaPools.pending')
    }}</span>
  </div>
</template>
