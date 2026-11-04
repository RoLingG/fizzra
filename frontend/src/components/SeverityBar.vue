<script setup lang="ts">
/** 原生 div/CSS 条：无需等图表库加载，打印时也能正常渲染 */
import { computed } from 'vue'

import { SEVERITY_ORDER, severityColorVar } from '@/api/format'
import { useFormat } from '@/composables/useFormat'

const props = defineProps<{
  counts: Record<string, number> | undefined
  legend?: boolean
}>()

// 文案走 i18n 而不是 text-transform
const { t, severityLabel } = useFormat()

const total = computed(() => {
  const c = props.counts ?? {}
  return SEVERITY_ORDER.reduce((sum, sev) => sum + (c[sev] ?? 0), 0)
})

/** 只保留宽度非零的分段，顺序由 SEVERITY_ORDER 决定 */
const segments = computed(() => {
  const c = props.counts ?? {}
  const sum = total.value
  if (!sum) return []
  return SEVERITY_ORDER.map((sev) => ({
    severity: sev,
    count: c[sev] ?? 0,
    percent: ((c[sev] ?? 0) / sum) * 100,
    color: severityColorVar(sev),
  })).filter((s) => s.percent > 0)
})

const legendItems = computed(() => {
  const c = props.counts ?? {}
  return SEVERITY_ORDER.map((sev) => ({
    severity: sev,
    count: c[sev] ?? 0,
    color: severityColorVar(sev),
  }))
})
</script>

<template>
  <div class="wrap">
    <div
      class="bar"
      role="img"
      :aria-label="`${t('chart.severityByIssues')}: ${legendItems.map((l) => t('chart.countBySeverity', { count: l.count, severity: severityLabel(l.severity) })).join(', ')}`"
    >
      <span
        v-for="segment in segments"
        :key="segment.severity"
        class="seg"
        :style="{ width: `${segment.percent}%`, background: segment.color }"
        :title="t('chart.countBySeverity', { count: segment.count, severity: severityLabel(segment.severity) })"
      />
      <span v-if="!segments.length" class="empty">{{ t('chart.noIssues') }}</span>
    </div>

    <div v-if="legend" class="legend">
      <span v-for="item in legendItems" :key="item.severity" class="legend-item">
        <i class="dot" :style="{ background: item.color }" />
        <span class="legend-label">{{ severityLabel(item.severity) }}</span>
        <strong>{{ item.count }}</strong>
      </span>
    </div>
  </div>
</template>

<style scoped>
.wrap {
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
}

.bar {
  display: flex;
  height: 12px;
  border-radius: var(--r-pill);
  overflow: hidden;
  background: var(--surface-strong);
  align-items: center;
  justify-content: center;
}

.seg {
  display: block;
  height: 100%;
  transition: width 0.3s ease;
}

/* critical 与 error 颜色接近，靠一道画布色细缝区分相邻分段 */
.seg + .seg {
  border-left: 1px solid var(--surface);
}

.empty {
  font: var(--t-caption);
  font-size: 11px;
  color: var(--text-soft);
}

.legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-md);
}

.legend-item {
  display: flex;
  align-items: center;
  gap: var(--s-xxs);
  font: var(--t-caption);
  font-size: 12px;
  color: var(--text-body);
}

.legend-label {
  color: var(--text-muted);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}
</style>
