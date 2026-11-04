<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    value: string | number
    hint?: string
    /** 来自 format.ts 的评分或严重级别着色类 */
    tone?: string
    unit?: string
  }>(),
  { hint: '', tone: '', unit: '' },
)
</script>

<template>
  <!-- 所有指标卡共用 `.card` 一种形态：白底、细描边、柔和投影 -->
  <div class="tile card">
    <div class="tile-label label">{{ label }}</div>
    <div class="tile-value display-xs" :class="tone">
      {{ value }}<span v-if="unit" class="unit">{{ unit }}</span>
    </div>
    <div v-if="hint" class="tile-hint">{{ hint }}</div>
  </div>
</template>

<style scoped>
.tile {
  padding: var(--s-md) var(--s-lg);
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.tile-label {
  color: var(--text-muted);
}

.tile-value {
  font-variant-numeric: tabular-nums;
  line-height: 1.15;
}

.unit {
  font-size: 0.55em;
  font-weight: 500;
  opacity: 0.6;
  margin-left: 2px;
}

.tile-hint {
  font: var(--t-body-sm);
  font-size: 12px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
