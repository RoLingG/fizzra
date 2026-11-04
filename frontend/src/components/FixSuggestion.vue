<script setup lang="ts">
/**
 * 修改前后对照。缩进原样保留（white-space: pre），复制按钮只复制 after
 */
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

import type { FixExample } from '@/api/types'

const props = defineProps<{
  fix: FixExample
  /** 紧凑模式隐藏「修改前/修改后」标签 */
  compact?: boolean
}>()

const { t } = useI18n()

const copied = ref(false)

const hasBefore = computed(() => props.fix.before?.trim().length > 0)
const hasAfter = computed(() => props.fix.after?.trim().length > 0)
const hasContent = computed(() => hasBefore.value || hasAfter.value)

async function copyAfter() {
  try {
    await navigator.clipboard.writeText(props.fix.after)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // 剪贴板 API 需要安全上下文，失败时回退为提示
    ElMessage.info(t('errors.copyFailed'))
  }
}
</script>

<template>
  <div v-if="hasContent" class="fix">
    <div class="fix-head">
      <span class="label">{{ t('fix.heading') }}</span>
      <el-button
        v-if="hasAfter"
        text
        size="small"
        :icon="copied ? 'Check' : 'CopyDocument'"
        @click.stop="copyAfter"
      >
        {{ copied ? t('common.copied') : t('common.copy') }}
      </el-button>
    </div>

    <div class="blocks">
      <div v-if="hasBefore" class="block block-before">
        <div v-if="!compact" class="block-label">
          <span class="marker marker-before" aria-hidden="true">−</span>
          <span>{{ t('fix.before') }}</span>
        </div>
        <pre class="code-block">{{ fix.before }}</pre>
      </div>

      <div v-if="hasAfter" class="block block-after">
        <div v-if="!compact" class="block-label">
          <span class="marker marker-after" aria-hidden="true">+</span>
          <span>{{ t('fix.after') }}</span>
        </div>
        <pre class="code-block">{{ fix.after }}</pre>
      </div>
    </div>

    <p v-if="fix.note" class="note">
      {{ fix.note }}
    </p>
  </div>
</template>

<style scoped>
.fix {
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
}

.fix-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-sm);
}

.blocks {
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
}

.block {
  border-radius: var(--r-md);
  overflow: hidden;
  border: 1px solid var(--border);
}

/* 沿用差异对比的红绿约定，着色压淡以保证代码可读性 */
.block-before {
  background: var(--sev-error-bg);
  border-color: color-mix(in srgb, var(--sev-error) 30%, transparent);
}

.block-after {
  background: var(--sev-ok-bg);
  border-color: color-mix(in srgb, var(--sev-ok) 30%, transparent);
}

.block-label {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  padding: 4px var(--s-sm);
  font: var(--t-caption-up);
  letter-spacing: var(--ls-caption-up);
  text-transform: uppercase;
  border-bottom: 1px solid color-mix(in srgb, currentColor 12%, transparent);
}

.block-before .block-label {
  color: var(--sev-error);
}

.block-after .block-label {
  color: var(--sev-ok);
}

.marker {
  font-family: var(--font-mono);
  font-size: 13px;
  line-height: 1;
}

.code-block {
  color: var(--text-body);
  background: transparent;
}

.note {
  margin: 0;
  font: var(--t-body-sm);
  color: var(--text-muted);
  line-height: 1.5;
}

/* 宽屏下前后两栏并排 */
@media (min-width: 900px) {
  .blocks {
    flex-direction: row;
    align-items: stretch;
  }
  .blocks > .block {
    flex: 1 1 0;
    min-width: 0;
  }
}
</style>
