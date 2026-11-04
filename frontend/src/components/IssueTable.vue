<script setup lang="ts">
// 过滤与排序在服务端完成：单次扫描可达数千条，全量下发再本地过滤会拖垮大项目
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { analysisApi, issueApi, reportError } from '@/api/client'
import type { Issue, Severity } from '@/api/types'
import { SEVERITY_ORDER, severityClass, truncate } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { localizeIssueText } from '@/locales/ruleText'
import FixSuggestion from '@/components/FixSuggestion.vue'

const router = useRouter()
const { t } = useI18n()

const { severityLabel, statusLabel, formatDate } = useFormat()

const props = defineProps<{
  analysisId: string
  analyzers?: string[]
}>()

const loading = ref(false)
const issues = ref<Issue[]>([])
const total = ref(0)

const filters = reactive({
  severities: [] as Severity[],
  analyzers: [] as string[],
  statuses: ['open'] as string[],
  newOnly: false,
  search: '',
  file: '',
  orderBy: 'severity' as 'severity' | 'file' | 'analyzer' | 'recent',
  page: 1,
  pageSize: 25,
})

const selected = ref<Issue | null>(null)
const drawerOpen = ref(false)

/** 任意过滤条件收窄了结果集 */
const filtered = computed(
  () =>
    filters.severities.length > 0 ||
    filters.analyzers.length > 0 ||
    filters.newOnly ||
    filters.search.length > 0 ||
    filters.file.length > 0 ||
    !(filters.statuses.length === 1 && filters.statuses[0] === 'open'),
)

async function load() {
  if (!props.analysisId) return
  loading.value = true
  try {
    const result = await analysisApi.issues(props.analysisId, {
      severity: filters.severities,
      analyzer: filters.analyzers,
      status: filters.statuses,
      newOnly: filters.newOnly || undefined,
      search: filters.search || undefined,
      file: filters.file || undefined,
      orderBy: filters.orderBy,
      limit: filters.pageSize,
      offset: (filters.page - 1) * filters.pageSize,
    })
    issues.value = result.issues
    total.value = result.total
  } catch (error) {
    reportError(error, t('errors.loadIssues'))
  } finally {
    loading.value = false
  }
}

function resetFilters() {
  filters.severities = []
  filters.analyzers = []
  filters.statuses = ['open']
  filters.newOnly = false
  filters.search = ''
  filters.file = ''
  filters.page = 1
}

// 搜索防抖
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(
  () => filters.search,
  () => {
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      filters.page = 1
      load()
    }, 300)
  },
)

watch(
  () => [
    filters.severities,
    filters.analyzers,
    filters.statuses,
    filters.newOnly,
    filters.orderBy,
    filters.page,
    filters.pageSize,
  ],
  () => {
    // 每页条数变小后，当前页可能已越界，回到第一页
    if ((filters.page - 1) * filters.pageSize >= total.value && filters.page > 1) {
      filters.page = 1
      return
    }
    load()
  },
  { deep: true },
)

watch(
  () => props.analysisId,
  () => {
    filters.page = 1
    load()
  },
)

function openDetail(issue: Issue) {
  selected.value = issue
  drawerOpen.value = true
}

/** 问题是否带有修改前后对照 */
function hasFix(issue: Issue): boolean {
  return Boolean(issue.fix?.before || issue.fix?.after)
}

// 传递依赖附完整链路
function originHint(issue: Issue): string {
  const path = issue.dependencyPath ?? []
  if (issue.origin === 'transitive' && path.length > 1) {
    return t('issue.originTransitiveHint', { path: path.join(' > ') })
  }
  return t(`issue.originHint_${issue.origin}`)
}

// 用 computed 而非在 load() 中改写，切换语言时表格才能随之重绘
const displayIssues = computed(() =>
  issues.value.map((issue) => localizeIssueText(issue, issue.analyzer)),
)

// 两个计数只统计当前页，需与页面说明文案的口径一致
const dependencyCount = computed(() => issues.value.filter((issue) => issue.origin).length)
const unscoredCount = computed(
  () => issues.value.filter((issue) => issue.origin === 'transitive').length,
)

/** 抽屉中展示的问题（已本地化） */
const displaySelected = computed(() =>
  selected.value ? localizeIssueText(selected.value, selected.value.analyzer) : null,
)

/** 打开该问题独立的、可链接的页面 */
function openPage(issue: Issue) {
  router.push({ name: 'issue-detail', params: { id: issue.id } })
}

/** 更新分诊状态。默认过滤会隐藏非 open 的问题，故重新加载即可 */
async function setStatus(issue: Issue, status: 'ignored' | 'false_positive' | 'open') {
  try {
    await issueApi.setStatus(issue.id, status)
    ElMessage.success(
      status === 'open' ? t('issue.reopened') : t('issue.markedAs', { status: statusLabel(status) }),
    )
    // 列表已被重新加载，无需再改动本地数组
    await load()
  } catch (error) {
    reportError(error, t('errors.updateIssue'))
  }
}

async function ignore(issue: Issue) {
  try {
    await ElMessageBox.confirm(t('issue.ignoreConfirmBody'), t('issue.ignoreConfirmTitle'), {
      confirmButtonText: t('issue.ignore'),
      cancelButtonText: t('common.cancel'),
      type: 'warning',
    })
  } catch {
    // 用户取消确认
  }
  await setStatus(issue, 'ignored')
}

/** 复制 file:line 引用，供粘贴到编辑器或工单 */
async function copyLocation(issue: Issue) {
  const text = `${issue.file}:${issue.line}`
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(t('issue.copiedLabel', { label: text }))
  } catch {
    // 剪贴板 API 需要安全上下文，失败时改为展示文本
    ElMessage.info(text)
  }
}

onMounted(load)

defineExpose({ reload: load })
</script>

<template>
  <div class="issue-table">
    <div class="toolbar">
      <el-input
        v-model="filters.search"
        :placeholder="t('issue.searchPlaceholder')"
        clearable
        class="search"
        :prefix-icon="'Search'"
      />

      <el-select
        v-model="filters.severities"
        multiple
        collapse-tags
        collapse-tags-tooltip
        :placeholder="t('issue.severityPlaceholder')"
        class="filter"
        clearable
      >
        <el-option v-for="sev in SEVERITY_ORDER" :key="sev" :label="severityLabel(sev)" :value="sev" />
      </el-select>

      <el-select
        v-if="analyzers?.length"
        v-model="filters.analyzers"
        multiple
        collapse-tags
        collapse-tags-tooltip
        :placeholder="t('issue.analyzerPlaceholder')"
        class="filter"
        clearable
      >
        <el-option v-for="a in analyzers" :key="a" :label="a" :value="a" />
      </el-select>

      <el-select
        v-model="filters.statuses"
        multiple
        collapse-tags
        :placeholder="t('issue.statusPlaceholder')"
        class="filter"
      >
        <el-option :label="t('status.open')" value="open" />
        <el-option :label="t('status.ignored')" value="ignored" />
        <el-option :label="t('status.falsePositive')" value="false_positive" />
      </el-select>

      <el-select v-model="filters.orderBy" class="filter" :aria-label="t('issue.sortBySeverity')">
        <el-option :label="t('issue.sortBySeverity')" value="severity" />
        <el-option :label="t('issue.sortByFile')" value="file" />
        <el-option :label="t('issue.sortByAnalyzer')" value="analyzer" />
        <el-option :label="t('issue.sortNewest')" value="recent" />
      </el-select>

      <!-- 筛选上一次成功扫描中不存在的发现 -->
      <el-checkbox v-model="filters.newOnly" class="new-only">{{ t('issue.newOnly') }}</el-checkbox>

      <el-button v-if="filtered" text @click="resetFilters">{{ t('common.reset') }}</el-button>
    </div>

    <div class="summary muted">
      <span v-loading="loading" class="count">
        {{ t('issue.summary', total) }}
      </span>
    </div>

    <aside v-if="dependencyCount > 0" class="notice notice-info dependency-note">
      <span class="notice-mark" aria-hidden="true">
        <svg viewBox="0 0 16 16" width="16" height="16">
          <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
          <path d="M8 4.4v4.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          <circle cx="8" cy="11.6" r="0.95" fill="currentColor" />
        </svg>
      </span>
      <div class="notice-body">
        <p class="notice-title">
          {{ t('issue.dependencyNoteTitle', { count: dependencyCount }) }}
        </p>
        <p class="notice-text">{{ t('issue.dependencyNoteBody') }}</p>
        <p v-if="unscoredCount > 0" class="notice-text">
          {{ t('issue.dependencyNoteUnscored', { count: unscoredCount }) }}
        </p>
      </div>
    </aside>

    <el-table
      v-loading="loading"
      :data="displayIssues"
      row-key="id"
      size="small"
      class="table"
      @row-click="openDetail"
      :row-style="{ cursor: 'pointer' }"
      :empty-text="t('issue.empty')"
    >
      <el-table-column :label="t('issue.colSeverity')" width="104">
        <template #default="{ row }">
          <span :class="severityClass(row.severity)">{{ severityLabel(row.severity) }}</span>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colAnalyzer')" prop="analyzer" width="112" />

      <el-table-column :label="t('issue.colRule')" width="184">
        <template #default="{ row }">
          <span>{{ row.rule }}</span>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colMessage')" min-width="280">
        <template #default="{ row }">
          <div class="message">
            <span v-if="row.isNew" class="new-badge" :title="t('issue.badgeNewHint')">
              {{ t('issue.badgeNew') }}
            </span>
            <!-- 完整链路只能放进 tooltip，光看消息读者容易误判为自己的代码问题 -->
            <span
              v-if="row.origin"
              class="origin-badge"
              :class="`origin-${row.origin}`"
              :title="originHint(row)"
            >
              {{ t(`issue.origin_${row.origin}`) }}
            </span>
            {{ truncate(row.message, 150) }}
            <span v-if="hasFix(row)" class="fix-badge" :title="t('issue.badgeFixHint')">
              {{ t('issue.badgeFix') }}
            </span>
          </div>
          <div v-if="row.advice" class="advice muted">{{ truncate(row.advice, 110) }}</div>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colLocation')" width="230">
        <template #default="{ row }">
          <span v-if="row.file" class="mono location" @click.stop="copyLocation(row)" :title="row.file">
            {{ truncate(row.file, 34) }}<template v-if="row.line">:{{ row.line }}</template>
          </span>
          <span v-else class="muted">{{ t('common.projectWide') }}</span>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colActions')" width="96" align="right">
        <template #default="{ row }">
          <el-button
            text
            size="small"
            :icon="'Link'"
            :aria-label="t('issue.openIssuePage')"
            @click.stop="openPage(row)"
          />
          <el-button
            text
            size="small"
            :icon="'MoreFilled'"
            :aria-label="t('issue.issueActions')"
            @click.stop="openDetail(row)"
          />
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      v-if="total > filters.pageSize"
      v-model:current-page="filters.page"
      v-model:page-size="filters.pageSize"
      :page-sizes="[25, 50, 100, 200]"
      :total="total"
      layout="total, sizes, prev, pager, next"
      class="pager"
      background
    />

    <el-drawer v-model="drawerOpen" size="760px" :with-header="false" direction="rtl">
      <div v-if="displaySelected" class="detail">
        <div class="detail-head">
          <span :class="severityClass(displaySelected.severity)">
            {{ severityLabel(displaySelected.severity) }}
          </span>
          <span class="rule">{{ displaySelected.rule }}</span>
          <el-tag size="small" effect="plain">{{ displaySelected.analyzer }}</el-tag>
          <!-- 抽屉是读者做修复决策的入口，没有来源标签会被读成「现在就修」 -->
          <el-tag
            v-if="displaySelected.origin"
            size="small"
            effect="plain"
            :type="displaySelected.origin === 'direct' ? 'warning' : 'info'"
          >
            {{ t(`issue.origin_${displaySelected.origin}`) }}
          </el-tag>
        </div>

        <h2 class="detail-message">{{ displaySelected.message }}</h2>

        <!-- 完整依赖链路，键盘用户也有 -->
        <div v-if="displaySelected.origin === 'transitive' && displaySelected.dependencyPath?.length" class="detail-block">
          <h3 class="block-title">{{ t('issue.dependencyChain') }}</h3>
          <div class="chain">
            <template v-for="(link, index) in displaySelected.dependencyPath" :key="index">
              <span v-if="index > 0" class="chain-arrow">›</span>
              <code class="mono chain-link">{{ link }}</code>
            </template>
          </div>
        </div>

        <div v-if="displaySelected.advice" class="detail-block">
          <h3 class="block-title">{{ t('issue.whyThisMatters') }}</h3>
          <p class="advice-text">{{ displaySelected.advice }}</p>
        </div>

        <FixSuggestion v-if="displaySelected.fix" :fix="displaySelected.fix" />

        <div class="detail-block">
          <h3 class="block-title">{{ t('issue.location') }}</h3>
          <div class="location-row">
            <code class="mono">
              {{ displaySelected.file || t('common.projectWide') }}<template v-if="displaySelected.line">:{{ displaySelected.line }}</template>
            </code>
            <el-button
              v-if="displaySelected.file"
              size="small"
              text
              :icon="'CopyDocument'"
              @click="copyLocation(displaySelected)"
            >
              {{ t('common.copy') }}
            </el-button>
          </div>
        </div>

        <div v-if="displaySelected.code" class="detail-block">
          <h3 class="block-title">{{ t('issue.source') }}</h3>
          <pre class="code mono">{{ displaySelected.code }}</pre>
        </div>

        <div class="detail-block">
          <h3 class="block-title">{{ t('issue.details') }}</h3>
          <dl class="meta">
            <dt>{{ t('issue.detected') }}</dt>
            <dd>{{ formatDate(displaySelected.createdAt) }}</dd>
            <dt>{{ t('issue.statusLabel') }}</dt>
            <dd>{{ statusLabel(displaySelected.status) }}</dd>
            <dt>{{ t('issue.fingerprint') }}</dt>
            <dd class="mono">{{ displaySelected.fingerprint }}</dd>
          </dl>
          <p class="muted footnote">{{ t('issue.fingerprintNote') }}</p>
        </div>

        <div class="detail-foot">
          <a
            v-if="displaySelected.docUrl"
            :href="displaySelected.docUrl"
            target="_blank"
            rel="noopener noreferrer"
          >
            {{ t('issue.ruleDocs') }}
          </a>
          <div class="detail-actions">
            <el-button v-if="displaySelected.status === 'open'" @click="ignore(displaySelected)">
              {{ t('issue.ignore') }}
            </el-button>
            <el-button
              v-if="displaySelected.status === 'open'"
              @click="setStatus(displaySelected, 'false_positive')"
            >
              {{ t('issue.falsePositive') }}
            </el-button>
            <el-button
              v-if="displaySelected.status !== 'open'"
              type="primary"
              @click="setStatus(displaySelected, 'open')"
            >
              {{ t('issue.reopen') }}
            </el-button>
          </div>
        </div>
      </div>
    </el-drawer>
  </div>
</template>

<style scoped>
.issue-table {
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-xs);
  align-items: center;
}

.search {
  flex: 1 1 260px;
  min-width: 200px;
}

.filter {
  width: 132px;
}

.summary {
  font: var(--t-body-sm);
}

.count {
  display: inline-block;
  min-height: 18px;
}

.table {
  width: 100%;
}

.message {
  line-height: 1.4;
}

.advice {
  font-size: 12px;
  margin-top: 2px;
}

.fix-badge {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 7px;
  border-radius: var(--r-pill);
  background: var(--sev-ok-bg);
  color: var(--sev-ok);
  font: var(--t-caption);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  vertical-align: 1px;
  white-space: nowrap;
}

.new-badge {
  display: inline-block;
  margin-right: 6px;
  padding: 1px 7px;
  border-radius: var(--r-pill);
  background: var(--sev-info-bg);
  color: var(--sev-info);
  font: var(--t-caption);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  vertical-align: 1px;
  white-space: nowrap;
}

.dependency-note {
  margin-bottom: var(--s-sm);
}

.chain {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.chain-link {
  font-size: 12px;
  padding: 2px 7px;
  border-radius: var(--r-sm);
  background: var(--surface-card);
}

.chain-arrow {
  color: var(--text-soft);
  font-size: 13px;
}

.origin-badge {
  display: inline-block;
  margin-right: 6px;
  padding: 1px 7px;
  border-radius: var(--r-pill);
  font: var(--t-caption);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.03em;
  vertical-align: 1px;
  white-space: nowrap;
  background: var(--surface-strong);
  color: var(--text-muted);
}

.origin-direct {
  background: var(--sev-warning-bg);
  color: var(--sev-warning);
}

.new-only {
  margin-left: 2px;
}

.location {
  cursor: copy;
  word-break: break-all;
}

.pager {
  align-self: flex-end;
  margin-top: 4px;
}

.detail {
  display: flex;
  flex-direction: column;
  gap: var(--s-lg);
  padding: 4px 2px 40px;
}

.detail-head {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  flex-wrap: wrap;
}

.rule {
  font-weight: 600;
}

.detail-message {
  font-size: 17px;
  line-height: 1.45;
  margin: 0;
}

.detail-block {
  display: flex;
  flex-direction: column;
  gap: var(--s-xxs);
}

.block-title {
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin: 0;
}

.advice-text {
  margin: 0;
  line-height: 1.6;
}

.location-row {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  justify-content: space-between;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--r-sm);
  padding: 8px 10px;
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
  grid-template-columns: 110px 1fr;
  gap: 4px 12px;
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

.footnote {
  font-size: 12px;
  margin: 4px 0 0;
}

/* 用 margin-left: auto 而非 space-between：无文档链接时操作按钮仍需靠右 */
.detail-foot {
  display: flex;
  align-items: center;
  gap: var(--s-sm);
  flex-wrap: wrap;
}

.detail-actions {
  display: flex;
  gap: var(--s-xs);
  flex-wrap: wrap;
  margin-left: auto;
}
</style>
