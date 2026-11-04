<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

import { issueApi, reportError } from '@/api/client'
import type { Issue } from '@/api/types'
import { severityClass } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { localizeIssueText } from '@/locales/ruleText'
import FixSuggestion from '@/components/FixSuggestion.vue'

const props = defineProps<{ id: string }>()
const router = useRouter()
const { t } = useI18n()

const { severityLabel, statusLabel, formatDate } = useFormat()

const loading = ref(true)
const issue = ref<Issue | null>(null)

const displayIssue = computed(() =>
  issue.value ? localizeIssueText(issue.value, issue.value.analyzer) : null,
)

const location = computed(() => {
  if (!issue.value?.file) return ''
  return issue.value.line ? `${issue.value.file}:${issue.value.line}` : issue.value.file
})

// 独立页面不经过 IssueTable，这里自带一份
function originHint(target: Issue): string {
  const path = target.dependencyPath ?? []
  if (target.origin === 'transitive' && path.length > 1) {
    return t('issue.originTransitiveHint', { path: path.join(' > ') })
  }
  return t(`issue.originHint_${target.origin}`)
}

async function load() {
  loading.value = true
  try {
    issue.value = await issueApi.get(props.id)
  } catch (error) {
    reportError(error, t('errors.loadIssue'))
  } finally {
    loading.value = false
  }
}

async function copy(text: string, label: string) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(t('issueDetail.copiedLabel', { label }))
  } catch {
    // 剪贴板 API 仅在安全上下文可用，失败时改用信息框展示原文供手动复制
    ElMessage.info(text)
  }
}

async function setStatus(status: 'open' | 'ignored' | 'false_positive') {
  if (!issue.value) return
  try {
    issue.value = await issueApi.setStatus(issue.value.id, status)
    ElMessage.success(
      status === 'open' ? t('issueDetail.reopened') : t('issueDetail.markedAs', { status: statusLabel(status) }),
    )
  } catch (error) {
    reportError(error, t('errors.updateIssue'))
  }
}

onMounted(load)
</script>

<template>
  <div v-loading="loading" class="issue-page">
    <template v-if="displayIssue">
      <nav class="crumbs">
        <el-button text :icon="'ArrowLeft'" @click="router.back()">
          {{ t('issueDetail.backToProject') }}
        </el-button>
        <el-button
          text
          :icon="'Folder'"
          @click="router.push({ name: 'project-detail', params: { id: displayIssue.projectId } })"
        >
          {{ t('issueDetail.projectNav') }}
        </el-button>
      </nav>

      <header class="head" v-reveal>
        <span :class="severityClass(displayIssue.severity)">
          {{ severityLabel(displayIssue.severity) }}
        </span>
        <span class="rule">{{ displayIssue.rule }}</span>
        <el-tag size="small" effect="plain">{{ displayIssue.analyzer }}</el-tag>
        <!-- 缺少来源标签时，直接链接打开的依赖问题会被误认为项目自身代码缺陷 -->
        <el-tooltip
          v-if="displayIssue.origin"
          placement="top"
          :content="originHint(displayIssue)"
        >
          <el-tag
            size="small"
            effect="plain"
            :type="displayIssue.origin === 'direct' ? 'warning' : 'info'"
          >
            {{ t(`issue.origin_${displayIssue.origin}`) }}
          </el-tag>
        </el-tooltip>
        <el-tag v-if="displayIssue.status !== 'open'" size="small" type="info" effect="plain">
          {{ statusLabel(displayIssue.status) }}
        </el-tag>
      </header>

      <h2 class="message" v-reveal>{{ displayIssue.message }}</h2>

      <div v-if="displayIssue.advice" class="card block" v-reveal>
        <h3 class="block-title">{{ t('issueDetail.whyThisMatters') }}</h3>
        <p class="advice">{{ displayIssue.advice }}</p>
      </div>

      <FixSuggestion v-if="displayIssue.fix" :fix="displayIssue.fix" class="card block" v-reveal />

      <div class="card block" v-reveal>
        <h3 class="block-title">{{ t('issueDetail.location') }}</h3>
        <div class="location-row">
          <code class="mono">{{ location || t('common.projectWide') }}</code>
          <el-button
            v-if="location"
            size="small"
            text
            :icon="'CopyDocument'"
            @click="copy(location, t('issueDetail.copyLocation'))"
          >
            {{ t('common.copy') }}
          </el-button>
        </div>
      </div>

      <div v-if="displayIssue.code" class="card block" v-reveal>
        <h3 class="block-title">{{ t('issueDetail.source') }}</h3>
        <pre class="code mono">{{ displayIssue.code }}</pre>
      </div>

      <div class="card block" v-reveal>
        <h3 class="block-title">{{ t('issueDetail.details') }}</h3>
        <dl class="meta">
          <dt>{{ t('issueDetail.detected') }}</dt>
          <dd>{{ formatDate(displayIssue.createdAt) }}</dd>
          <dt>{{ t('issueDetail.firstSeen') }}</dt>
          <dd>
            <span v-if="displayIssue.isNew" class="new-pill">{{ t('issueDetail.newInScan') }}</span>
            <span v-else class="muted">{{ t('issueDetail.knownFromBefore') }}</span>
          </dd>
          <dt>{{ t('issueDetail.analyzer') }}</dt>
          <dd>{{ displayIssue.analyzer }}</dd>
          <dt>{{ t('issueDetail.rule') }}</dt>
          <dd>{{ displayIssue.rule }}</dd>
          <dt>{{ t('issueDetail.fingerprint') }}</dt>
          <dd
            class="mono fingerprint"
            @click="copy(displayIssue.fingerprint, t('issueDetail.copyFingerprint'))"
          >
            {{ displayIssue.fingerprint }}
          </dd>
        </dl>
        <p class="muted footnote">{{ t('issueDetail.fingerprintNote') }}</p>
      </div>

      <div class="card block detail-foot" v-reveal>
        <a
          v-if="displayIssue.docUrl"
          :href="displayIssue.docUrl"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ t('issueDetail.ruleDocs') }}
        </a>
        <div class="actions">
          <el-button v-if="displayIssue.status === 'open'" @click="setStatus('ignored')">
            {{ t('issueDetail.ignore') }}
          </el-button>
          <el-button v-if="displayIssue.status === 'open'" @click="setStatus('false_positive')">
            {{ t('issueDetail.falsePositive') }}
          </el-button>
          <el-button v-if="displayIssue.status !== 'open'" type="primary" @click="setStatus('open')">
            {{ t('issueDetail.reopen') }}
          </el-button>
        </div>
      </div>
    </template>

    <el-empty v-else-if="!loading" :description="t('issueDetail.notFound')" />
  </div>
</template>

<style scoped>
.issue-page {
  max-width: 880px;
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
}

.crumbs {
  display: flex;
  gap: var(--s-xxs);
  margin-bottom: -6px;
}

.head {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  flex-wrap: wrap;
}

.rule {
  font-weight: 600;
}

/* 这条消息同时是页面标题，尺度比 .page-title 小一档，
   因为上面还有一行严重级别 */
.message {
  margin: var(--s-sm) 0 var(--s-md);
  font: var(--t-display-xs);
  font-size: 22px;
  letter-spacing: -0.4px;
  line-height: 1.35;
  color: var(--text);
}

.block {
  padding: var(--s-lg);
}

.block-title {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin: 0 0 8px;
}

.advice {
  margin: 0;
  line-height: 1.6;
}

.location-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-xs);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--r-sm);
  padding: var(--s-sm) var(--s-md);
}

.location-row code {
  word-break: break-all;
}

.code {
  margin: 0;
  padding: var(--s-sm);
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--r-sm);
  overflow-x: auto;
  white-space: pre;
  line-height: 1.5;
}

.meta {
  display: grid;
  grid-template-columns: 120px 1fr;
  gap: 5px 14px;
  margin: 0;
  font-size: 13px;
}

.meta dt {
  color: var(--text-muted);
}

.meta dd {
  margin: 0;
  word-break: break-all;
}

.fingerprint {
  cursor: copy;
}

.new-pill {
  display: inline-block;
  padding: 1px 8px;
  border-radius: var(--r-pill);
  background: var(--sev-info-bg);
  color: var(--sev-info);
  font: var(--t-caption);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.03em;
  text-transform: uppercase;
}

.footnote {
  font-size: 12px;
  margin: 10px 0 0;
  line-height: 1.5;
}

.detail-foot {
  display: flex;
  align-items: center;
  gap: var(--s-sm);
  flex-wrap: wrap;
}

.actions {
  display: flex;
  gap: var(--s-xs);
  flex-wrap: wrap;
  margin-left: auto;
}
</style>
