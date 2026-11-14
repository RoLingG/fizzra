<script setup lang="ts">
// 项目详情：运行中的扫描用轮询刷新，比 WebSocket 更简单可靠
import { computed, defineAsyncComponent, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { analysisApi, projectApi, reportError, type ReportFormat } from '@/api/client'
import type {
  Analysis,
  BlockedAnalyzer,
  IssueStats,
  ProjectOverview,
  QualityGate,
  TrendPoint,
} from '@/api/types'
import { formatDuration, formatPercent, scoreClass, statusTagType } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { restartReveal } from '@/directives/reveal'
import { localizeAnalyzerSummary } from '@/locales/analyzerText'
import CodeBlock from '@/components/CodeBlock.vue'
import IssueTable from '@/components/IssueTable.vue'
import SeverityBar from '@/components/SeverityBar.vue'
import StatTile from '@/components/StatTile.vue'

const TrendChart = defineAsyncComponent(() => import('@/components/TrendChart.vue'))

const props = defineProps<{ id: string }>()

const router = useRouter()

const { t } = useI18n()

const { formatDate, formatRelative, languageLabel, triggerLabel } = useFormat()

const loading = ref(true)
const scanning = ref(false)

/** 预检弹窗状态及被阻塞的分析器 */
const preflightOpen = ref(false)
const preflightItems = ref<BlockedAnalyzer[]>([])
const overview = ref<ProjectOverview | null>(null)
const stats = ref<IssueStats | null>(null)
const activeTab = ref('issues')

/* 每个标签页的入场容器带 data-tab，切换时按名字取出来重播。
 *
 * el-tab-pane 默认非惰性渲染，所以四个面板从首次挂载起就都在 DOM 里，
 * 只是被 v-show 藏起来。IntersectionObserver 在页面加载时就判定它们进入了视口，
 * 切回某个标签时不会再触发一次入场。这里在切换时手动重播当前可见的那个。 */
watch(activeTab, async (name) => {
  /* 等 Vue 把新面板的 v-show 应用完，再触发动画。
   *
   * 必须等到面板真的可见。元素处在 display: none 时浏览器不建立过渡，
   * 这时移除 revealed 不会产生任何动画；等它变为可见，已有的 revealed 直接
   * 把 opacity 定在 1，切回来就看不到入场。
   *
   * 按 data-tab 从 DOM 里取，不能靠模板 ref 收集：多个同名 ref 只有在元素同时
   * 存在时才会被收集成数组，而 v-show 藏起来的面板不会留下 ref。 */
  await nextTick()
  const el = document.querySelector<HTMLElement>(`[data-tab="${name}"]`)
  if (!el) return

  // display 还没落定时（懒加载面板要把内容渲染出来才会可见）再等一帧
  if (!el.offsetParent) {
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()))
  }
  restartReveal(el)
})

// 跳过列表默认收起
const skipsOpen = ref(false)

const gateForm = reactive({
  minScore: 70,
  maxCritical: 0,
  maxHigh: 0,
  maxComplexity: 20,
  minCoverage: 0,
  failOnWarning: false,
  enabled: true,
})
const savingGate = ref(false)

const project = computed(() => overview.value?.project ?? null)
const latest = computed(() => overview.value?.latest ?? null)
// 只在上一次扫描失败、且没有更晚的成功记录时展示，否则用户看的是已经有结论的那次
const lastFailed = computed(() => {
  const failed = overview.value?.lastFailed
  if (!failed) return null
  const ok = overview.value?.latest
  if (ok && new Date(ok.createdAt) > new Date(failed.createdAt)) return null
  return failed
})
const history = computed(() => overview.value?.history ?? [])
const trend = computed<TrendPoint[]>(() => overview.value?.trend ?? [])
const metrics = computed(() => latest.value?.metrics ?? null)

/** 最新一次分析处于 pending 或 running 时，扫描尚未结束 */
const inFlight = computed(() => {
  const newest = history.value[0]
  return newest != null && (newest.status === 'pending' || newest.status === 'running')
})

/** 正在导出报告的扫描 id，用于只在该行显示加载状态 */
const exportingId = ref('')

/** 扫描是否仍在进行（pending 或 running） */
function isRunning(status: string): boolean {
  return status === 'pending' || status === 'running'
}

// 支持按任意一次历史扫描导出，而不仅是最新一次
async function exportReport(row: Analysis, format: ReportFormat = 'html') {
  if (!project.value || exportingId.value) return
  exportingId.value = row.id
  try {
    await analysisApi.downloadReport(project.value.id, row.id, format)
  } catch (error) {
    reportError(error, t('errors.exportReport'))
  } finally {
    exportingId.value = ''
  }
}

/** 历史行「不完整」标记的 tooltip 文案 */
function incompleteTooltip(row: Analysis): string {
  const names = row.failedAnalyzers ?? []
  if (!names.length) return t('project.incompleteTag')
  return t('project.incompleteTooltip', { analyzers: names.join(', ') })
}

let pollTimer: ReturnType<typeof setTimeout> | undefined
let pollAttempts = 0

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true
  try {
    const data = await projectApi.overview(props.id)
    overview.value = data

    if (data.latest) {
      stats.value = await analysisApi.stats(data.latest.id)
    }
    if (data.gate) {
      Object.assign(gateForm, {
        minScore: data.gate.minScore,
        maxCritical: data.gate.maxCritical,
        maxHigh: data.gate.maxHigh,
        maxComplexity: data.gate.maxComplexity,
        minCoverage: data.gate.minCoverage,
        failOnWarning: data.gate.failOnWarning,
        enabled: data.gate.enabled,
      })
    }
  } catch (error) {
    reportError(error, t('errors.loadProject'))
  } finally {
    loading.value = false
  }
}

// 扫描期间轮询，约 3 分钟后放弃
function schedulePoll() {
  clearTimeout(pollTimer)
  if (!inFlight.value || pollAttempts > 90) return

  pollAttempts++
  pollTimer = setTimeout(async () => {
    await load(false)
    if (inFlight.value) schedulePoll()
    else pollAttempts = 0
  }, 2000)
}

watch(inFlight, (running) => {
  if (running) {
    pollAttempts = 0
    schedulePoll()
  } else {
    clearTimeout(pollTimer)
  }
})

watch(
  () => props.id,
  () => {
    pollAttempts = 0
    load()
  },
)

// 缺工具时先预检，由用户决定安装还是仍然扫描。预检失败不阻断扫描
async function startScan() {
  if (scanning.value) return
  scanning.value = true
  try {
    let blocked: BlockedAnalyzer[] = []
    try {
      blocked = (await projectApi.preflight(props.id)).blocked ?? []
    } catch {
      // 预检仅为提示，不是前置条件，失败时直接扫描
    }

    if (blocked.length) {
      preflightItems.value = blocked
      preflightOpen.value = true
      return // 由弹窗中的按钮继续
    }

    await runScan()
  } catch (error) {
    reportError(error, t('errors.startScan'))
  } finally {
    scanning.value = false
  }
}

/** 真正提交一次扫描，在预检通过或用户选择「仍然扫描」后调用 */
async function runScan() {
  scanning.value = true
  try {
    await analysisApi.start(props.id)
    ElMessage.success(t('project.scanQueued'))
    await load(false)
  } catch (error) {
    reportError(error, t('errors.startScan'))
  } finally {
    scanning.value = false
  }
}

/** 用户在预检弹窗中选择了「仍然扫描」 */
function scanAnyway() {
  preflightOpen.value = false
  void runScan()
}

// 使用目录中的短标签。目录缺少条目时返回 undefined，模板据此整句省略
function blockedSummary(name: string): string | undefined {
  return localizeAnalyzerSummary(name)
}

/** 复制安装命令到剪贴板 */
async function copyCommand(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(t('common.copied'))
  } catch {
    reportError(null, t('errors.copyFailed'))
  }
}

// InstallHint 可能包含补充说明，只取首行即实际安装命令
function firstCommandLine(hint: string | undefined): string {
  if (!hint) return ''
  return hint.split('\n')[0].trim()
}

async function saveGate() {
  savingGate.value = true
  try {
    await projectApi.saveGate(props.id, gateForm as Partial<QualityGate>)
    ElMessage.success(t('project.gateSaved'))
    await load(false)
  } catch (error) {
    reportError(error, t('errors.saveGate'))
  } finally {
    savingGate.value = false
  }
}

// 维度名取自翻译目录，权重需与 api/format.ts 中的评分算法保持一致
const scoreDimensions = computed(() => {
  const a = latest.value
  if (!a) return []
  return [
    {
      name: t('quality.security'),
      weight: 0.3,
      detail: t('project.hintCriticalError', {
        critical: a.criticalIssues,
        errors: a.errorIssues,
      }),
    },
    {
      name: t('quality.bugs'),
      weight: 0.3,
      detail: t('dashboard.issuesCount', a.warningIssues),
    },
    {
      name: t('quality.maintainability'),
      weight: 0.2,
      detail: `${formatPercent(metrics.value?.duplication ?? 0)} ${t('project.mDuplication').toLowerCase()}`,
    },
    {
      name: t('quality.complexity'),
      weight: 0.12,
      detail: `avg ${(metrics.value?.avgComplexity ?? 0).toFixed(1)}, worst ${metrics.value?.maxComplexity ?? 0}`,
    },
    {
      name: t('quality.coverage'),
      weight: 0.08,
      detail: metrics.value?.coverage
        ? formatPercent(metrics.value.coverage)
        : t('quality.notMeasured'),
    },
  ]
})

/** 复杂度最高的函数 */
const complexFunctions = computed(() => metrics.value?.complexFunctions ?? [])
const longFunctions = computed(() => metrics.value?.longFunctions ?? [])

/** 最大的文件 */
const largestFiles = computed(() => metrics.value?.largestFiles ?? [])

/** 代码、注释、空行在总行数中的百分比，用于规模卡片的比例条 */
const mixPercent = computed(() => {
  const m = metrics.value
  const total = m ? m.lines : 0
  if (!m || total <= 0) {
    return { code: 0, comment: 0, blank: 0 }
  }
  const code = (m.codeLines / total) * 100
  const comment = (m.commentLines / total) * 100
  return { code, comment, blank: Math.max(0, 100 - code - comment) }
})

/** 把数值换算成进度条宽度，超出上限时截到 100 */
function meterWidth(value: number, max: number): number {
  if (!max || value <= 0) {
    return 0
  }
  return Math.min(100, (value / max) * 100)
}

onMounted(async () => {
  await load()
  if (inFlight.value) schedulePoll()
})

onBeforeUnmount(() => clearTimeout(pollTimer))
</script>

<template>
  <div v-loading="loading" class="detail">
    <template v-if="project">
      <!-- 与 IssueDetailView 的面包屑保持一致 -->
      <nav class="crumbs">
        <el-button text :icon="'ArrowLeft'" @click="router.push({ name: 'projects' })">
          {{ t('project.backToProjects') }}
        </el-button>
      </nav>

      <header class="hero" v-reveal>
        <div class="hero-main">
          <div class="title-row">
            <span class="title-dot" :style="{ background: project.color }" />
            <h1 class="display-sm title-name">{{ project.name }}</h1>
            <!-- 仅展示语言：kind 对非 Go 项目无意义，wails 与 Go 并列易被误读为另一门语言 -->
            <span class="lang-name">{{ languageLabel(project.language) }}</span>
            <el-tag v-if="inFlight" type="warning" size="small" effect="dark" class="scanning-tag">
              {{ t('project.scanning') }}
            </el-tag>
          </div>
          <div class="meta muted">
            <span v-if="project.repoUrl" class="projectURL">{{ project.repoUrl }}</span>
            <span v-if="project.localPath" class="projectURL">{{ project.localPath }}</span>
            <!-- 本地项目可以没有分支，仓库项目一定有 -->
            <span v-if="project.branch">
              {{ t('project.branch') }} <code class="mono">{{ project.branch }}</code>
            </span>
            <span v-if="latest">{{ t('project.scannedAt', { when: formatRelative(latest.createdAt) }) }}</span>
          </div>
        </div>
        <div class="hero-actions">
          <el-button :icon="'Refresh'" :loading="scanning" @click="startScan">
            {{ scanning ? t('project.queuing') : t('project.scanNow') }}
          </el-button>
        </div>
      </header>

      <!-- 扫描失败排在一切之前：下面所有面板显示的都是「没有数据」，不先说明
           原因的话读者会以为项目从来没扫过 -->
      <aside v-if="lastFailed" class="notice notice-danger" v-reveal>
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M5.6 5.6l4.8 4.8M10.4 5.6l-4.8 4.8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">{{ t('project.scanFailedTitle') }}</p>
          <pre class="notice-error">{{ lastFailed.error }}</pre>
          <p class="notice-sub">{{ t('project.scanFailedHint') }}</p>
        </div>
      </aside>

      <aside v-if="latest?.error" class="notice notice-info" v-reveal>
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M8 4.4v4.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.6" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">{{ latest.error }}</p>
        </div>
      </aside>

      <!-- 不完整的扫描提示排在门禁结果之前：评分不可信时不应引导读者按门禁结论行动 -->
      <aside v-if="latest?.noAnalyzerRan" class="notice notice-warn" v-reveal>
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <path d="M8 1.8 15 14H1z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
            <path d="M8 6.2v3.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.7" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">{{ t('project.noAnalyzerRan') }}</p>
          <p class="notice-text">{{ t('project.noAnalyzerRanSub') }}</p>
          <!-- 这些是跳过的分析器，不是失败的 -->
          <ul v-if="latest.skipReasons?.length" class="incomplete-list">
            <li v-for="(reason, index) in latest.skipReasons" :key="index" class="mono">
              {{ reason }}
            </li>
          </ul>
        </div>
      </aside>

      <aside v-else-if="latest?.incomplete" class="notice notice-warn" v-reveal>
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <path d="M8 1.8 15 14H1z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
            <path d="M8 6.2v3.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.7" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">
            {{ t('project.incomplete', { count: (latest.failedAnalyzers ?? []).length }) }}
          </p>
          <p class="notice-text">{{ t('project.incompleteSub') }}</p>
          <ul class="incomplete-list">
            <li
              v-for="(analyzer, index) in latest.failedAnalyzers ?? []"
              :key="analyzer"
              class="incomplete-item"
            >
              <span class="mono incomplete-name">{{ analyzer }}</span>
              <!-- 原因数组与失败分析器数组按下标一一对应，需做越界保护 -->
              <span v-if="latest.failureReasons?.[index]" class="muted incomplete-reason">
                {{ latest.failureReasons[index] }}
              </span>
            </li>
          </ul>
        </div>
      </aside>

      <!-- 跳过列表默认收起，只显示标题内的数量 -->
      <aside
        v-if="!latest?.noAnalyzerRan && latest?.skippedAnalyzers?.length"
        class="notice notice-info"
        v-reveal
      >
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M8 4.4v4.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.6" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">
            {{ t('project.skipped', { count: (latest.skippedAnalyzers ?? []).length }) }}
          </p>
          <div class="skips-head">
            <p class="notice-text">{{ t('project.skippedSub') }}</p>
            <button
              type="button"
              class="skips-toggle"
              :aria-expanded="skipsOpen"
              @click="skipsOpen = !skipsOpen"
            >
              <span>{{ skipsOpen ? t('project.skipsHide') : t('project.skipsShow') }}</span>
              <svg class="skips-chevron" :class="{ open: skipsOpen }" viewBox="0 0 16 16" aria-hidden="true">
                <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </button>
          </div>

          <!-- grid-template-rows 0fr -> 1fr 可过渡到实际内容高度，无需预设 max-height -->
          <div class="skips-collapse" :class="{ open: skipsOpen }">
            <div class="skips-collapse-inner">
              <div class="skips-body">
                <ul class="incomplete-list">
                  <li
                    v-for="(analyzer, index) in latest.skippedAnalyzers ?? []"
                    :key="analyzer"
                    class="incomplete-item"
                  >
                    <span class="mono incomplete-name">{{ analyzer }}</span>
                    <!-- 与失败原因相同：按下标对应，需越界保护 -->
                    <span v-if="latest.skipReasons?.[index]" class="muted incomplete-reason">
                      {{ latest.skipReasons[index] }}
                    </span>
                  </li>
                </ul>
                <!-- 安装命令已去重：多个分析器可能共用同一个二进制 -->
                <p v-if="latest.skippedTools?.length" class="notice-text mono">
                  {{ t('project.skippedTools', { tools: (latest.skippedTools ?? []).join(', ') }) }}
                </p>
              </div>
            </div>
          </div>
        </div>
      </aside>

      <aside v-if="latest && latest.gateStatus === 'failed'" class="notice notice-danger" v-reveal>
        <span class="notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M5.6 5.6l4.8 4.8M10.4 5.6l-4.8 4.8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          </svg>
        </span>
        <div class="notice-body">
          <p class="notice-title">{{ t('project.gateFailed') }}</p>
          <p class="notice-text">{{ latest.gateReason }}</p>
        </div>
      </aside>

      <section class="tiles" v-reveal>
        <StatTile
          :label="t('project.statScore')"
          :value="latest ? latest.score.toFixed(1) : t('common.none')"
          :unit="latest ? t('project.unitOutOfHundred') : ''"
          :tone="latest ? scoreClass(latest.score) : ''"
          :hint="
            latest && latest.gateStatus
              ? t('project.hintGate', { status: t(`gate.${latest.gateStatus}`) })
              : t('project.notScanned')
          "
        />
        <StatTile
          :label="t('project.statIssues')"
          :value="latest?.totalIssues ?? t('common.none')"
          :hint="
            latest
              ? t('project.hintCriticalError', {
                  critical: latest.criticalIssues,
                  errors: latest.errorIssues,
                })
              : ''
          "
        />
        <StatTile
          :label="t('project.statCode')"
          :value="metrics?.codeLines ? metrics.codeLines.toLocaleString() : t('common.none')"
          :hint="
            metrics
              ? t('project.hintFilesFunctions', {
                  files: metrics.files,
                  functions: metrics.functions,
                })
              : ''
          "
        />
        <StatTile
          :label="t('project.statComplexity')"
          :value="metrics ? metrics.avgComplexity.toFixed(1) : t('common.none')"
          :hint="metrics ? t('project.hintWorst', { value: metrics.maxComplexity }) : ''"
        />
        <StatTile
          :label="t('project.statDuplication')"
          :value="metrics ? formatPercent(metrics.duplication) : t('common.none')"
          :hint="
            metrics?.duplicateBlocks?.length
              ? t('project.hintCloneGroups', { count: metrics.duplicateBlocks.length })
              : t('project.hintNoClones')
          "
        />
        <StatTile
          :label="t('project.statCoverage')"
          :value="metrics?.coverage ? formatPercent(metrics.coverage) : t('common.notAvailable')"
          :hint="metrics ? t('project.hintTestFiles', { count: metrics.testFiles }) : ''"
        />
      </section>

      <!-- el-tabs 自己不挂 v-reveal：它挂载时要测量 tab bar 宽度和面板尺寸，
           在 translateY 的状态下测量会算错布局。下面是每个标签页各自的入场容器，
           切换标签时由 watch(activeTab) 调用 restartReveal 重播。 -->
      <el-tabs v-model="activeTab" class="tabs">
        <el-tab-pane name="issues">
          <template #label>
            {{ t('project.tabIssues') }}
            <el-badge v-if="latest?.totalIssues" :value="latest.totalIssues" type="info" class="tab-badge" />
          </template>
          <div data-tab="issues" v-reveal>
            <section v-if="latest" class="card panel">
              <IssueTable
                :analysis-id="latest.id"
                :analyzers="latest.analyzers ?? undefined"
              />
            </section>
            <el-empty v-else :description="t('project.runScanToSeeIssues')" />
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('project.tabOverview')" name="overview">
          <!-- 卡片按 DOM 顺序错峰出现，时序写在 `.stagger-item` 的 CSS 里，
               不再和 v-reveal 各写一套 transform -->
          <div data-tab="overview" class="overview triple" v-reveal>
            <section class="card panel stagger-item" :style="{ '--i': 0 }">
              <h3 class="section-title">{{ t('project.scoreContribution') }}</h3>
              <p class="muted sub">{{ t('project.scoreContributionSub') }}</p>
              <div class="card-scroll">
                <table class="breakdown">
                  <thead>
                    <tr>
                      <th>{{ t('project.colDimension') }}</th>
                      <th>{{ t('project.colWeight') }}</th>
                      <th>{{ t('project.colDetail') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="dim in scoreDimensions" :key="dim.name">
                      <td>{{ dim.name }}</td>
                      <td>{{ (dim.weight * 100).toFixed(0) }}%</td>
                      <td class="muted">{{ dim.detail }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>

            <section v-if="stats" class="card panel stagger-item" :style="{ '--i': 1 }">
              <h3 class="section-title">{{ t('project.issueDistribution') }}</h3>
              <SeverityBar :counts="stats.bySeverity" legend />
              <h4 class="sub-title">{{ t('project.byAnalyzer') }}</h4>
              <!-- 分析器数量随项目增长，列表单独滚动，柱状图保持可见 -->
              <div class="card-scroll">
                <ul class="analyzer-list">
                  <li v-for="(count, name) in stats.byAnalyzer" :key="name">
                    <span class="mono">{{ name }}</span>
                    <strong>{{ count }}</strong>
                  </li>
                </ul>
              </div>
            </section>

            <section
              v-if="stats?.topRules?.length"
              class="card panel stagger-item"
              :style="{ '--i': 2 }"
            >
              <h3 class="section-title">{{ t('project.mostFrequentRules') }}</h3>
              <div class="card-scroll">
                <table class="breakdown">
                  <thead>
                    <tr>
                      <th>{{ t('issue.colRule') }}</th>
                      <th>{{ t('issue.colAnalyzer') }}</th>
                      <th>{{ t('project.colCount') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="rule in stats.topRules.slice(0, 12)" :key="`${rule.analyzer}-${rule.rule}`">
                      <td>{{ rule.rule }}</td>
                      <td class="muted">{{ rule.analyzer }}</td>
                      <td>{{ rule.count }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>

            <!-- 两张图的标题由 TrendChart 自带，外层不可再加标题 -->
            <section class="card panel wide trend-panel stagger-item" :style="{ '--i': 3 }">
              <Suspense>
                <TrendChart :trend="trend" :min-score="overview?.gate?.minScore" />
                <template #fallback><div class="skeleton" /></template>
              </Suspense>
            </section>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('project.tabMetrics')" name="metrics">
          <el-empty v-if="!metrics || !metrics.files" :description="t('project.noMetrics')" />
          <div v-else data-tab="metrics" class="overview" v-reveal>
            <section class="card panel stagger-item" :style="{ '--i': 0 }">
              <h3 class="section-title">{{ t('project.size') }}</h3>
              <div class="metric-lead">
                <div class="metric-lead-main">
                  <strong>{{ metrics.codeLines.toLocaleString() }}</strong>
                  <span>{{ t('project.mCodeLines') }}</span>
                </div>
                <div class="metric-lead-side">
                  <div>
                    <strong>{{ metrics.files.toLocaleString() }}</strong>
                    <span>{{ t('project.mFiles') }}</span>
                  </div>
                  <div>
                    <strong>{{ metrics.functions.toLocaleString() }}</strong>
                    <span>{{ t('project.mFunctions') }}</span>
                  </div>
                </div>
              </div>

              <!-- 代码、注释、空行三段占比，用一条横向比例条呈现总量构成 -->
              <div class="line-mix">
                <div class="line-mix-bar" role="img" :aria-label="t('project.mTotalLines')">
                  <span class="mix-code" :style="{ width: mixPercent.code + '%' }" />
                  <span class="mix-comment" :style="{ width: mixPercent.comment + '%' }" />
                  <span class="mix-blank" :style="{ width: mixPercent.blank + '%' }" />
                </div>
                <ul class="line-mix-legend">
                  <li>
                    <i class="dot dot-code" />
                    {{ t('project.mCodeLines') }}
                    <strong>{{ metrics.codeLines.toLocaleString() }}</strong>
                  </li>
                  <li>
                    <i class="dot dot-comment" />
                    {{ t('project.mCommentLines') }}
                    <strong>{{ metrics.commentLines.toLocaleString() }}</strong>
                  </li>
                  <li>
                    <i class="dot dot-blank" />
                    {{ t('project.mBlankLines') }}
                    <strong>{{ metrics.blankLines.toLocaleString() }}</strong>
                  </li>
                </ul>
              </div>

              <div class="metric-foot">
                <dl class="kv compact">
                  <dt>{{ t('project.mTotalLines') }}</dt>
                  <dd>{{ metrics.lines.toLocaleString() }}</dd>
                  <dt>{{ t('project.mTestFiles') }}</dt>
                  <dd>{{ metrics.testFiles.toLocaleString() }}</dd>
                  <dt>{{ t('project.mTestFunctions') }}</dt>
                  <dd>{{ metrics.testFunctions.toLocaleString() }}</dd>
                </dl>
              </div>
            </section>

            <section class="card panel stagger-item" :style="{ '--i': 1 }">
              <h3 class="section-title">{{ t('project.mComplexity') }}</h3>
              <div class="metric-lead">
                <div class="metric-lead-main">
                  <strong>{{ metrics.avgComplexity.toFixed(1) }}</strong>
                  <span>{{ t('project.mAverage') }}</span>
                </div>
                <div class="metric-lead-side">
                  <div>
                    <strong>{{ metrics.maxComplexity }}</strong>
                    <span>{{ t('project.mWorst') }}</span>
                  </div>
                  <div>
                    <strong>{{ formatPercent(metrics.duplication) }}</strong>
                    <span>{{ t('project.mDuplication') }}</span>
                  </div>
                </div>
              </div>

              <!-- 重复度按 25% 的天花板画条，与评分的可维护性维度同一口径 -->
              <div class="meter-row">
                <span class="meter-label">{{ t('project.mDuplication') }}</span>
                <span class="meter-track">
                  <span
                    class="meter-fill"
                    :class="metrics.duplication > 10 ? 'is-warn' : ''"
                    :style="{ width: meterWidth(metrics.duplication, 25) + '%' }"
                  />
                </span>
                <span class="meter-value">{{ formatPercent(metrics.duplication) }}</span>
              </div>

              <div class="meter-row">
                <span class="meter-label">{{ t('project.mCoverage') }}</span>
                <span class="meter-track">
                  <span
                    class="meter-fill"
                    :class="metrics.coverage > 0 ? 'is-ok' : ''"
                    :style="{ width: meterWidth(metrics.coverage, 100) + '%' }"
                  />
                </span>
                <span class="meter-value">
                  {{ metrics.coverage ? formatPercent(metrics.coverage) : t('quality.notMeasured') }}
                </span>
              </div>

              <p v-if="!metrics.coverage" class="muted sub">{{ t('project.coverageNote') }}</p>
            </section>

            <section v-if="largestFiles.length" class="card panel wide stagger-item" :style="{ '--i': 2 }">
              <h3 class="section-title">{{ t('project.largestFiles') }}</h3>
              <p class="muted sub">{{ t('project.largestFilesSub') }}</p>
              <table class="breakdown">
                <thead>
                  <tr>
                    <th>{{ t('project.colFile') }}</th>
                    <th>{{ t('project.colLines') }}</th>
                    <th>{{ t('project.colCode') }}</th>
                    <th>{{ t('project.colKind') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="f in largestFiles" :key="f.file">
                    <td class="mono">{{ f.file }}</td>
                    <td>{{ f.lines.toLocaleString() }}</td>
                    <td>{{ f.code.toLocaleString() }}</td>
                    <td>
                      <el-tag v-if="f.isTest" size="small" effect="plain">
                        {{ t('project.kindTest') }}
                      </el-tag>
                      <span v-else class="muted">{{ t('project.kindSource') }}</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section v-if="complexFunctions.length" class="card panel wide stagger-item" :style="{ '--i': 3 }">
              <h3 class="section-title">{{ t('project.mostComplex') }}</h3>
              <table class="breakdown">
                <thead>
                  <tr>
                    <th>{{ t('project.colFunction') }}</th>
                    <th>{{ t('project.colLocation') }}</th>
                    <th>{{ t('project.colComplexity') }}</th>
                    <th>{{ t('project.colLines') }}</th>
                    <th>{{ t('project.colParams') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="fn in complexFunctions" :key="`${fn.file}:${fn.line}:${fn.name}`">
                    <td class="mono">{{ fn.receiver ? `${fn.receiver}.` : '' }}{{ fn.name }}</td>
                    <td class="mono muted">{{ fn.file }}:{{ fn.line }}</td>
                    <td>{{ fn.complexity }}</td>
                    <td>{{ fn.length }}</td>
                    <td>{{ fn.params }}</td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section v-if="longFunctions.length" class="card panel wide stagger-item" :style="{ '--i': 4 }">
              <h3 class="section-title">{{ t('project.longestFunctions') }}</h3>
              <table class="breakdown">
                <thead>
                  <tr>
                    <th>{{ t('project.colFunction') }}</th>
                    <th>{{ t('project.colLocation') }}</th>
                    <th>{{ t('project.colLines') }}</th>
                    <th>{{ t('project.colComplexity') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="fn in longFunctions" :key="`${fn.file}:${fn.line}:${fn.name}`">
                    <td class="mono">{{ fn.receiver ? `${fn.receiver}.` : '' }}{{ fn.name }}</td>
                    <td class="mono muted">{{ fn.file }}:{{ fn.line }}</td>
                    <td>{{ fn.length }}</td>
                    <td>{{ fn.complexity }}</td>
                  </tr>
                </tbody>
              </table>
            </section>

            <section
              v-if="metrics.duplicateBlocks?.length"
              class="card panel wide stagger-item"
              :style="{ '--i': 5 }"
            >
              <h3 class="section-title">{{ t('project.duplicatedBlocks') }}</h3>
              <p class="muted sub">
                {{ t('project.duplicatedBlocksSub', { lines: metrics.duplicateBlocks[0]?.lines ?? 12 }) }}
              </p>
              <div v-for="(block, index) in metrics.duplicateBlocks.slice(0, 8)" :key="index" class="clone">
                <div class="clone-head">
                  <strong>{{ t('project.linesCount', { count: block.lines }) }}</strong>
                  <span class="muted">{{ (block.files ?? []).join(' · ') }}</span>
                </div>
                <CodeBlock class="clone-body" :code="block.snippet" :path="block.files?.[0]" />
              </div>
            </section>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('project.tabHistory')" name="history">
          <div data-tab="history" v-reveal>
          <section class="card panel stagger-item" :style="{ '--i': 0 }">
            <el-table
              :data="history"
              row-key="id"
              size="small"
              :empty-text="t('project.historyEmpty')"
            >
              <el-table-column :label="t('project.colStarted')" width="180">
                <template #default="{ row }">{{ formatDate(row.createdAt) }}</template>
              </el-table-column>
              <el-table-column :label="t('project.colStatus')" width="150">
                <template #default="{ row }">
                  <el-tag :type="statusTagType(row.status)" size="small" effect="plain">
                    {{ t(`status.${row.status}`) }}
                  </el-tag>
                  <!-- 丢了分析器的运行仍报 success，需要额外标记区分 -->
                  <el-tooltip
                    v-if="row.incomplete"
                    placement="top"
                    :content="incompleteTooltip(row)"
                  >
                    <el-tag type="warning" size="small" effect="plain" class="incomplete-tag">
                      {{ t('project.incompleteTag') }}
                    </el-tag>
                  </el-tooltip>
                </template>
              </el-table-column>
              <el-table-column :label="t('project.colTrigger')" width="92">
                <template #default="{ row }">{{ triggerLabel(row.trigger) }}</template>
              </el-table-column>
              <el-table-column :label="t('project.colScore')" width="88">
                <template #default="{ row }">
                  <span :class="scoreClass(row.score)">{{ row.score.toFixed(1) }}</span>
                </template>
              </el-table-column>
              <el-table-column :label="t('project.colIssues')" width="148">
                <template #default="{ row }">
                  <span class="counts">
                    <span class="sev sev-critical">{{ row.criticalIssues }}</span>
                    <span class="sev sev-error">{{ row.errorIssues }}</span>
                    <span class="sev sev-warning">{{ row.warningIssues }}</span>
                    <span class="sev sev-info">{{ row.infoIssues }}</span>
                  </span>
                </template>
              </el-table-column>
              <!-- 没有 gateStatus 时显示扫描状态 -->
              <el-table-column :label="t('project.colGate')" width="94">
                <template #default="{ row }">
                  <el-tag
                    v-if="row.gateStatus"
                    :type="statusTagType(row.gateStatus)"
                    size="small"
                    effect="plain"
                  >
                    {{ t(`gate.${row.gateStatus}`) }}
                  </el-tag>
                  <el-tag v-else :type="statusTagType(row.status)" size="small" effect="plain">
                    {{ t(`status.${row.status}`) }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column :label="t('project.colDuration')" width="96">
                <template #default="{ row }">{{ formatDuration(row.durationMs) }}</template>
              </el-table-column>
              <el-table-column :label="t('project.colAnalyzers')" min-width="160">
                <template #default="{ row }">
                  <!-- 没有分析器时用 info 标签，单显示「—」易被误解为渲染失败 -->
                  <span v-if="row.analyzers?.length" class="analyzer-tags">
                    <el-tag
                      v-for="analyzer in row.analyzers"
                      :key="analyzer"
                      size="small"
                      effect="plain"
                      class="analyzer-tag"
                    >
                      {{ analyzer }}
                    </el-tag>
                  </span>
                  <el-tag v-else size="small" effect="plain" type="info" class="analyzer-tag">
                    {{ t('project.analyzersNone') }}
                  </el-tag>
                </template>
              </el-table-column>
              <!-- 报告是单次扫描的快照，故按行导出 -->
              <el-table-column :label="t('project.colExport')" width="136" align="right">
                <template #default="{ row }">
                  <!-- 运行中的扫描无法导出（结果为半成品），禁用并提示原因 -->
                  <el-tooltip
                    :disabled="!isRunning(row.status)"
                    placement="top"
                    :content="t('project.exportRunning')"
                  >
                    <span>
                      <!-- split-button：左键直接导出 HTML，右侧为格式菜单 -->
                      <el-dropdown
                        split-button
                        size="small"
                        :disabled="isRunning(row.status)"
                        :loading="exportingId === row.id"
                        @click="exportReport(row, 'html')"
                        @command="(fmt: ReportFormat) => exportReport(row, fmt)"
                      >
                        {{ t('project.exportReport') }}
                        <template #dropdown>
                          <el-dropdown-menu>
                            <el-dropdown-item command="html">{{ t('project.exportHtml') }}</el-dropdown-item>
                            <el-dropdown-item command="json">{{ t('project.exportJson') }}</el-dropdown-item>
                            <el-dropdown-item command="sarif">{{ t('project.exportSarif') }}</el-dropdown-item>
                          </el-dropdown-menu>
                        </template>
                      </el-dropdown>
                    </span>
                  </el-tooltip>
                </template>
              </el-table-column>
            </el-table>
          </section>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('project.tabSettings')" name="settings">
          <div data-tab="settings" v-reveal>
          <section class="card panel settings stagger-item" :style="{ '--i': 0 }">
            <h3 class="section-title">{{ t('project.gateHeading') }}</h3>
            <p class="muted sub">{{ t('project.gateSub') }}</p>

            <el-form label-position="top" class="gate-form">
              <el-form-item :label="t('project.gateEnabled')">
                <el-switch v-model="gateForm.enabled" />
              </el-form-item>

              <el-form-item :label="t('project.gateMinScore')">
                <el-input-number v-model="gateForm.minScore" :min="0" :max="100" :step="5" />
                <div class="field-hint muted">{{ t('project.gateHintMinScore') }}</div>
              </el-form-item>

              <el-form-item :label="t('project.gateMaxCritical')">
                <el-input-number v-model="gateForm.maxCritical" :min="0" />
              </el-form-item>

              <el-form-item :label="t('project.gateMaxHigh')">
                <el-input-number v-model="gateForm.maxHigh" :min="0" />
              </el-form-item>

              <el-form-item :label="t('project.gateMaxComplexity')">
                <el-input-number v-model="gateForm.maxComplexity" :min="0" />
                <div class="field-hint muted">{{ t('project.gateHintZeroDisables') }}</div>
              </el-form-item>

              <el-form-item :label="t('project.gateMinCoverage')">
                <el-input-number v-model="gateForm.minCoverage" :min="0" :max="100" />
                <div class="field-hint muted">{{ t('project.gateHintCoverage') }}</div>
              </el-form-item>

              <el-form-item :label="t('project.gateFailOnWarnings')">
                <el-switch v-model="gateForm.failOnWarning" />
                <div class="field-hint muted">{{ t('project.gateHintFailOnWarnings') }}</div>
              </el-form-item>

              <el-button type="primary" :loading="savingGate" @click="saveGate">
                {{ t('project.gateSave') }}
              </el-button>
            </el-form>
          </section>
          </div>
        </el-tab-pane>
      </el-tabs>
    </template>

    <el-empty v-else-if="!loading" :description="t('project.notFound')" />

    <!-- 自建弹窗而非 ElMessageBox：后者仅支持两个按钮，无法容纳警示、卡片和命令块 -->
    <el-dialog
      v-model="preflightOpen"
      :title="t('project.preflightTitle')"
      width="520px"
      class="dialog-centered"
      :close-on-click-modal="false"
    >
      <div class="preflight">
        <aside class="notice notice-warn">
          <span class="notice-mark" aria-hidden="true">
            <svg viewBox="0 0 16 16" width="16" height="16">
              <path d="M8 1.8 15 14H1z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
              <path d="M8 6.2v3.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
              <circle cx="8" cy="11.7" r="0.95" fill="currentColor" />
            </svg>
          </span>
          <div class="notice-body">
            <p class="notice-title">
              {{ t('project.preflightLead', { count: preflightItems.length }) }}
            </p>
          </div>
        </aside>

        <div class="pf-item" v-for="item in preflightItems" :key="item.name">
          <div class="pf-top">
            <!-- 分析器短标签取自本地目录，跟随界面语言。缺条目时整句省略 -->
            <span class="pf-name">{{ item.name }}</span>
            <span v-if="blockedSummary(item.name)" class="pf-what">{{ blockedSummary(item.name) }}</span>
            <!-- 可执行文件名不翻译，它就是用户要安装的目标 -->
            <span v-if="item.missingTools?.length" class="pf-tool mono">
              {{ item.missingTools.join(', ') }}
            </span>
          </div>
          <div v-if="firstCommandLine(item.installHint)" class="pf-cmd">
            <code class="mono">{{ firstCommandLine(item.installHint) }}</code>
            <el-button size="small" text :icon="'CopyDocument'" @click="copyCommand(firstCommandLine(item.installHint))">
              {{ t('common.copy') }}
            </el-button>
          </div>
        </div>
      </div>

      <template #footer>
        <el-button @click="preflightOpen = false">{{ t('project.preflightCancel') }}</el-button>
        <el-button @click="scanAnyway">{{ t('project.preflightScanAnyway') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
}

.crumbs {
  display: flex;
  gap: var(--s-xxs);
  margin-bottom: -6px;
}

.hero {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--s-md);
}

.hero-main {
  min-width: 0;
}

.hero-actions {
  margin-top: 10px;
  flex: 0 0 auto;
}

.title-row {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  flex-wrap: wrap;
}

.title-dot {
  margin-top: 10px;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex: 0 0 auto;
}

.title-name {
  margin: 0;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-md);
  margin-top: 15px;
  font-size: 12px;
}

/* .note 和 .incomplete-body 随 el-alert 一起移除
 * 提示条现在用全局的 .notice，正文类名是 .notice-text */
.skips-head {
  display: flex;
  align-items: baseline;
  gap: var(--s-sm);
}

.skips-head .notice-text {
  flex: 1 1 auto;
}

/* 原生 button 而非 el-button：后者 hover 时会填一块实色背景，
   在浅灰的提示条上很突兀。这里用 currentColor 的低比例混合，只轻微加深 */
.skips-toggle {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  flex: none;
  padding: 2px 6px;
  border: 0;
  border-radius: var(--r-sm);
  background: transparent;
  color: inherit;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
  transition: background-color 0.15s ease;
}

.skips-toggle:hover {
  background-color: color-mix(in srgb, currentColor 8%, transparent);
}

.skips-toggle:focus-visible {
  outline: 2px solid currentColor;
  outline-offset: 1px;
}

.skips-chevron {
  width: 14px;
  height: 14px;
  transition: transform 0.2s ease;
}

.skips-chevron.open {
  transform: rotate(180deg);
}

/* 收起/展开区域，grid 行高 0fr -> 1fr 过渡到内容高度
 * 内层须 overflow: hidden，否则收起时内容溢出 */
.skips-collapse {
  display: grid;
  grid-template-rows: 0fr;
  transition: grid-template-rows 0.22s ease;
}

.skips-collapse.open {
  grid-template-rows: 1fr;
}

.skips-collapse-inner {
  overflow: hidden;
}

/* 上内边距不能放在外层，会被 0fr 一起收掉 */
.skips-body {
  max-height: 20rem;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding-right: 6px;
  padding-top: 2px;
}

/* 用户偏好减少动效时禁用折叠动画 */
@media (prefers-reduced-motion: reduce) {
  .skips-collapse,
  .skips-chevron,
  .skips-toggle {
    transition: none;
  }
}

.incomplete-list {
  margin: 6px 0 0;
  padding-left: 18px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.incomplete-list li {
  font-size: 12px;
  line-height: 1.5;
}

.incomplete-item {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: baseline;
}

.incomplete-name {
  font-weight: 600;
}

.incomplete-reason {
  font-size: 11.5px;
  /* 失败原因可能多行，pre-line 保留其中的换行 */
  white-space: pre-line;
  overflow-wrap: anywhere;
}

.incomplete-tag {
  margin-left: 6px;
  font-size: 10px;
}

.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: var(--s-sm);
}

.tabs {
  margin-top: 2px;
}

.tab-badge {
  margin-left: 6px;
}

.panel {
  padding: var(--s-lg);
}

.panel.wide {
  grid-column: 1 / -1;
}

.trend-panel {
  padding-top: var(--s-md);
}

.overview {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--s-md);
}

/* 概览页顶部三张表并排，趋势图等宽幅卡片仍占整行 */
.overview.triple {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

/* 没有统计数据时只剩评分构成一张，让它独占一行而不是挤在三分之一处 */
.overview.triple > .stagger-item:only-child {
  grid-column: 1 / -1;
}

.overview .full {
  grid-column: 1 / -1;
}

/* 概览页三张表统一限高。卡片定高，主体内容撑满剩余空间并自行滚动，
   某一列条目变多时不会把整行网格一起拉高 */
.overview.triple > .stagger-item:not(.wide) {
  height: 310px;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.card-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  /* 给滚动条留出位置，表格右缘不会和卡片内边距贴死 */
  padding-right: var(--s-xxs);
}

/* 滚动时表头留在顶部，读者仍能对上是哪一列 */
.card-scroll thead th {
  position: sticky;
  top: 0;
  z-index: 1;
  background: var(--surface);
}

.sub {
  font-size: 12px;
  margin: 0 0 12px;
}

.sub-title {
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.03em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin: 18px 0 6px;
}

.breakdown,
.kv {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.breakdown th {
  text-align: left;
  font: var(--t-caption-up);
  letter-spacing: var(--ls-caption-up);
  text-transform: uppercase;
  color: var(--text-muted);
  padding: var(--s-xxs) var(--s-xs) 6px 0;
  border-bottom: 1px solid var(--border);
}

/* 三列布局下表格变窄，长规则名和路径要能换行而不是把表格撑出卡片 */
.breakdown td {
  padding: 7px var(--s-xs) 7px 0;
  border-bottom: 1px solid var(--border-soft);
  overflow-wrap: anywhere;
}

.breakdown tr:last-child td {
  border-bottom: none;
}

/* 数值列的数据右对齐，表头不走这个类，保持统一居左 */
.breakdown td.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}

.kv {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 6px 16px;
  margin: 0;
}

.kv dt {
  color: var(--text-muted);
}

.kv dd {
  margin: 0;
  text-align: right;
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

/* 紧凑版 kv 用于卡片底部，字号和行距收小一档 */
.kv.compact {
  font-size: 12.5px;
  gap: 4px 16px;
}

.kv.compact dd {
  font-weight: 500;
  color: var(--text-body);
}

/* 度量卡片的主数字区：左侧最大的一个数，右侧两项次要指标 */
.metric-lead {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--s-md);
  padding-bottom: var(--s-sm);
  border-bottom: 1px solid var(--border-soft);
}

.metric-lead-main {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.metric-lead-main strong {
  font: var(--t-display-sm);
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
  line-height: 1.1;
  color: var(--text);
}

.metric-lead-main span,
.metric-lead-side span {
  font-size: 12px;
  color: var(--text-muted);
}

.metric-lead-side {
  display: flex;
  gap: var(--s-lg);
  text-align: right;
}

.metric-lead-side > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.metric-lead-side strong {
  font-size: 17px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}

/* 总行数的构成：一条比例条加图例 */
.line-mix {
  padding: var(--s-sm) 0;
  border-bottom: 1px solid var(--border-soft);
}

.line-mix-bar {
  display: flex;
  height: 6px;
  border-radius: 3px;
  overflow: hidden;
  background: var(--border);
}

.line-mix-bar > span {
  display: block;
  height: 100%;
}

.mix-code {
  background: var(--text);
}

.mix-comment {
  background: var(--text-soft);
}

/* 空行段比轨道底色再深一档，否则和背景融成一片 */
.mix-blank {
  background: var(--line-strong);
}

.line-mix-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 4px var(--s-md);
  margin: var(--s-xs) 0 0;
  padding: 0;
  list-style: none;
  font-size: 12px;
  color: var(--text-muted);
}

.line-mix-legend li {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.line-mix-legend strong {
  color: var(--text-body);
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex: none;
}

.dot-code {
  background: var(--text);
}

.dot-comment {
  background: var(--text-soft);
}

.dot-blank {
  background: var(--line-strong);
}

.metric-foot {
  padding-top: var(--s-sm);
}

/* 单值进度条，用于重复度和覆盖率 */
.meter-row {
  display: grid;
  grid-template-columns: 64px 1fr auto;
  align-items: center;
  gap: var(--s-sm);
  padding: 6px 0;
  font-size: 12.5px;
}

.meter-label {
  color: var(--text-muted);
}

.meter-track {
  height: 4px;
  border-radius: 2px;
  background: var(--border);
  box-shadow: inset 0 0 0 1px var(--line-strong);
  overflow: hidden;
}

.meter-fill {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--text-soft);
  transition: width 0.4s ease;
}

.meter-fill.is-warn {
  background: var(--warn);
}

.meter-fill.is-ok {
  background: var(--ok);
}

.meter-value {
  font-variant-numeric: tabular-nums;
  color: var(--text-body);
  min-width: 56px;
  text-align: right;
}

.analyzer-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
}

.analyzer-list li {
  display: flex;
  justify-content: space-between;
  padding: 5px 0;
  border-bottom: 1px solid var(--border);
  font-size: 13px;
}

.analyzer-list li:last-child {
  border-bottom: none;
}

.counts {
  display: flex;
  gap: var(--s-xxs);
}

.analyzer-tags {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--s-xxs);
}

.analyzer-tag {
  font-size: 10px;
}

/* 列表视图中的同名样式，此处重新定义 */
.lang-name {
  margin-top: 10px;
  margin-left: 10px;
  font-size: 13px;
}

/* 与语言标签一起下沉，左边距已由 title-row 的 gap 提供 */
.scanning-tag {
  margin-top: 10px;
}

.clone {
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  margin-bottom: 10px;
  overflow: hidden;
}

.clone-head {
  display: flex;
  justify-content: space-between;
  gap: var(--s-sm);
  padding: 7px 10px;
  background: var(--bg);
  font-size: 12px;
  border-bottom: 1px solid var(--border);
}

.clone-body {
  margin: 0;
  padding: 10px;
  font-size: 11.5px;
  line-height: 1.5;
  overflow-x: auto;
  white-space: pre;
  max-height: 220px;
}

.skeleton {
  height: 220px;
  border-radius: var(--r-md);
  background: var(--bg);
}

.settings {
  max-width: 620px;
}

.gate-form {
  margin-top: 8px;
}

/* 用 column-gap 而非提示文字的 margin：提示换行后 margin 会残留为缩进 */
:deep(.el-form-item__content) {
  column-gap: 8px;
}

.field-hint {
  font-size: 11px;
  line-height: 1.4;
}

/* 中等宽度下三列过于拥挤，退回两列 */
@media (max-width: 1200px) {
  .overview.triple {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 900px) {
  .overview,
  .overview.triple {
    grid-template-columns: 1fr;
  }
  .head {
    flex-direction: column;
  }
}

.preflight {
  display: flex;
  flex-direction: column;
  gap: var(--s-sm);
}

.pf-item {
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  padding: var(--s-sm) var(--s-md);
}

.pf-top {
  display: flex;
  align-items: baseline;
  gap: var(--s-xs);
  flex-wrap: wrap;
}

.pf-name {
  font: var(--t-title-sm);
  color: var(--text);
}

.pf-what {
  font: var(--t-body-sm);
  font-size: 13px;
  color: var(--text-muted);
}

.pf-tool {
  margin-left: auto;
  font-size: 11px;
  color: var(--text-soft);
  background: var(--surface-soft);
  padding: 1px 6px;
  border-radius: var(--r-xs);
}

.pf-cmd {
  margin-top: var(--s-xs);
  display: flex;
  align-items: stretch;
  gap: var(--s-xs);
}

.pf-cmd code {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  line-height: 1.5;
  padding: 7px 10px;
  border-radius: var(--r-sm);
  background: var(--surface-soft);
  border: 1px solid var(--border);
  color: var(--text-body);
  /* 不换行，放不下时横向滚动：折行会让一条命令看起来像两条 */
  overflow-x: auto;
  white-space: pre;

  scrollbar-width: thin;
  scrollbar-color: #8f8878 transparent;
}
.pf-cmd code::-webkit-scrollbar { height: 6px; }
.pf-cmd code::-webkit-scrollbar-track { background: transparent; }
.pf-cmd code::-webkit-scrollbar-button { display: none; width: 0; height: 0; }
.pf-cmd code::-webkit-scrollbar-thumb { background: #8f8878; border-radius: 999px; }
</style>
