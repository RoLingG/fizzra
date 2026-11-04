<script setup lang="ts">
// 总览页：总分、项目分布与趋势
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

import { projectApi, reportError, systemApi } from '@/api/client'
import type { ProjectSummary } from '@/api/types'
import { scoreClass, statusTagType } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import ScoreRing from '@/components/ScoreRing.vue'
import StatTile from '@/components/StatTile.vue'
import SeverityBar from '@/components/SeverityBar.vue'

const TrendChart = defineAsyncComponent(() => import('@/components/TrendChart.vue'))

const router = useRouter()
const { t } = useI18n()

const { formatRelative } = useFormat()

const loading = ref(true)
const projects = ref<ProjectSummary[]>([])
// 0 表示未配置门禁线
const minScore = ref(0)

onMounted(async () => {
  try {
    projects.value = await projectApi.list()
  } catch (error) {
    reportError(error, t('errors.loadProjects'))
  } finally {
    loading.value = false
  }
  try {
    minScore.value = (await systemApi.info()).minScore
  } catch {
    // 门禁线是附加信息，取不到时静默跳过
  }
})

const analysed = computed(() => projects.value.filter((p) => p.latest))

const totalIssues = computed(() =>
  analysed.value.reduce(
    (acc, p) => {
      const a = p.latest
      if (!a) return acc
      acc.critical += a.criticalIssues
      acc.error += a.errorIssues
      acc.warning += a.warningIssues
      acc.info += a.infoIssues
      return acc
    },
    { critical: 0, error: 0, warning: 0, info: 0 } as Record<string, number>,
  ),
)

const issueCount = computed(() => Object.values(totalIssues.value).reduce((a, b) => a + b, 0))

// 只取各项目最新一次分析的平均分，与下方全历史统计口径不同
const averageScore = computed(() => {
  if (!analysed.value.length) return null
  return analysed.value.reduce((acc, p) => acc + (p.latest?.score ?? 0), 0) / analysed.value.length
})

const gatesPassing = computed(() => analysed.value.filter((p) => p.latest?.gateStatus === 'passed').length)

/* 评分卡右栏「得分最低」的五个项目，只看一眼，不做成第二张表 */
const lowestProjects = computed(() =>
  analysed.value
    .map((p) => ({ id: p.id, name: p.name, score: p.latest?.score ?? 0 }))
    .sort((a, b) => a.score - b.score)
    .slice(0, 5),
)

/* 同一栏里那条双色条的比例，没配置门禁线时无意义 */
const gateSplit = computed(() => {
  const total = analysed.value.length
  if (!total) return null
  return { passed: gatesPassing.value, failed: total - gatesPassing.value, total }
})

/* 摘要上方的一句话结论，阈值与 scoreGrade() 一致，措辞和评级字母不会打架 */
const headline = computed(() => {
  const score = averageScore.value
  if (score === null) return t('dashboard.headlineNone')
  if (score >= 85) return t('dashboard.headlineGood')
  if (score >= 70) return t('dashboard.headlineFair')
  return t('dashboard.headlinePoor')
})

// 汇总全部历史，与上方平均分的口径不同
const history = computed(() => {
  const stats = projects.value.map((p) => p.stats).filter(Boolean)
  const runs = stats.reduce((a, s) => a + s.runs, 0)
  const passed = stats.reduce((a, s) => a + s.passedRuns, 0)
  const scored = stats.filter((s) => s.runs > 0)

  return {
    best: scored.length ? Math.max(...scored.map((s) => s.bestScore)) : null,
    worst: scored.length ? Math.min(...scored.map((s) => s.worstScore)) : null,
    runs,
    passed,
  }
})

const criticalProjects = computed(() =>
  analysed.value
    .filter((p) => (p.latest?.criticalIssues ?? 0) > 0)
    .map((p) => ({ id: p.id, name: p.name, count: p.latest!.criticalIssues }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name)),
)

// 每个分支用完整句子而非拼接翻译碎片，中英文语序不同，拼接会读不通
const securityRisk = computed(() => {
  const { critical, error } = totalIssues.value

  if (critical > 0) {
    return {
      label: t('dashboard.security.highLabel'),
      headline: t('dashboard.security.highHeadline', { count: critical }),
      detail: t('dashboard.security.highDetail', { count: critical, errors: error }),
    }
  }
  if (error > 0) {
    return {
      label: t('dashboard.security.elevatedLabel'),
      headline: t('dashboard.security.elevatedHeadline', { count: error }),
      detail: t('dashboard.security.elevatedDetail', { count: error }),
    }
  }
  if (!analysed.value.length) {
    return {
      label: t('dashboard.security.unknownLabel'),
      headline: t('dashboard.security.unknownHeadline'),
      detail: t('dashboard.security.unknownDetail'),
    }
  }
  return {
    label: t('dashboard.security.lowLabel'),
    headline: t('dashboard.security.lowHeadline'),
    detail: t('dashboard.security.lowDetail'),
  }
})

// 排除没有覆盖率报告的项目而非记零，未测量不等于未覆盖
const coverage = computed(() => {
  const measured = analysed.value.map((p) => p.latest?.metrics?.coverage ?? 0).filter((c) => c > 0)

  if (!measured.length) {
    return {
      display: t('dashboard.coverageNotAvailable'),
      tone: '',
      hint: t('dashboard.coverageHint'),
    }
  }

  const mean = measured.reduce((a, b) => a + b, 0) / measured.length
  return {
    display: `${mean.toFixed(0)}%`,
    tone: mean >= 80 ? 'score-good' : mean >= 60 ? 'score-ok' : 'score-warn',
    hint: t('dashboard.coverageMeasured', {
      measured: measured.length,
      total: analysed.value.length,
    }),
  }
})

const criticalSummary = computed(() => {
  const offenders = analysed.value
    .filter((p) => (p.latest?.criticalIssues ?? 0) > 0)
    .map((p) => `${p.name} (${p.latest!.criticalIssues})`)

  if (!offenders.length) return ''
  return t('dashboard.criticalSummary', {
    count: totalIssues.value.critical,
    projects: offenders.join(', '),
  })
})

const needsAttention = computed(() =>
  [...analysed.value].sort((a, b) => (a.latest?.score ?? 0) - (b.latest?.score ?? 0)).slice(0, 5),
)

// 按分析时间戳分组，同一时间的多个项目合并为一个点
const aggregateTrend = computed(() => {
  const byDate = new Map<
    string,
    { score: number[]; total: number; critical: number; errors: number; warnings: number; gates: string[] }
  >()

  for (const project of analysed.value) {
    const latest = project.latest
    if (!latest) continue
    const key = latest.createdAt
    const entry = byDate.get(key) ?? { score: [], total: 0, critical: 0, errors: 0, warnings: 0, gates: [] }
    entry.score.push(latest.score)
    entry.total += latest.totalIssues
    entry.critical += latest.criticalIssues
    entry.errors += latest.errorIssues
    entry.warnings += latest.warningIssues
    entry.gates.push(latest.gateStatus)
    byDate.set(key, entry)
  }

  return [...byDate.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([at, entry]) => ({
      analysisId: at,
      at,
      score: entry.score.reduce((a, b) => a + b, 0) / entry.score.length,
      total: entry.total,
      critical: entry.critical,
      errors: entry.errors,
      warnings: entry.warnings,
      gateStatus: (entry.gates.every((g) => g === 'passed') ? 'passed' : 'failed') as
        | 'passed'
        | 'failed'
        | 'skipped',
    }))
})

const recent = computed(() =>
  analysed.value
    .map((p) => ({ project: p, analysis: p.latest! }))
    .sort((a, b) => new Date(b.analysis.createdAt).getTime() - new Date(a.analysis.createdAt).getTime())
    .slice(0, 8),
)

function openProject(id: string) {
  router.push({ name: 'project-detail', params: { id } })
}
</script>

<template>
  <div v-loading="loading" class="dashboard">
    <section v-if="!loading && !projects.length" class="card empty-band" v-reveal>
      <h1 class="display-md">{{ t('dashboard.emptyTitle') }}</h1>
      <p class="lead">{{ t('dashboard.emptyBody') }}</p>
      <pre class="code-block empty-code">fizzra scan ./your-project</pre>
      <el-button type="primary" size="large" @click="router.push({ name: 'projects' })">
        {{ t('dashboard.addProject') }}
      </el-button>
    </section>

    <template v-else-if="projects.length">
      <!-- 不加 v-reveal：评分区是页面的主角，应该一直在位、自己从 0 滚到目标值，
           而不是随区块一起淡入。demo 里这块也从不参与滚动渐现。 -->
      <section class="hero">
        <div class="hero-main card">
          <div class="hero-panel">
            <!-- 右半边的同心圆底纹，纯装饰，其余子元素压在它上面 -->
            <svg class="wave-rings" viewBox="0 0 600 400" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
              <g fill="none" stroke="currentColor" stroke-width="1">
                <circle cx="600" cy="200" r="60" />
                <circle cx="600" cy="200" r="120" />
                <circle cx="600" cy="200" r="180" />
                <circle cx="600" cy="200" r="240" />
                <circle cx="600" cy="200" r="300" />
              </g>
            </svg>

            <!-- 环 + 评级字母 + A–F 刻度：数字滚动、环推进、字母按档位入场 -->
            <ScoreRing :score="averageScore" :gate="minScore" />
            <div class="hero-detail">
              <h2 class="hero-heading">{{ headline }}</h2>
              <p class="hero-sub">
                <template v-if="averageScore === null">{{ t('dashboard.noAnalyses') }}</template>
                <template v-else>
                  {{
                    t('dashboard.heroSummary', {
                      count: analysed.length,
                      passing: gatesPassing,
                    })
                  }}
                </template>
              </p>

              <div class="stat-row">
                <div class="stat-cell">
                  <span class="stat-key">{{ t('dashboard.bandRuns') }}</span>
                  <span class="stat-val">{{ history.runs }}</span>
                </div>
                <div class="stat-cell">
                  <span class="stat-key">{{ t('dashboard.bandPassed') }}</span>
                  <span class="stat-val">{{ history.passed }}</span>
                </div>
                <div class="stat-cell">
                  <span class="stat-key">{{ t('dashboard.bandFailed') }}</span>
                  <span class="stat-val" :class="history.runs - history.passed > 0 ? 'score-warn' : ''">
                    {{ history.runs - history.passed }}
                  </span>
                </div>
              </div>
            </div>

            <!-- 第三栏：门禁通过比例和得分最低的项目，和 .hero-detail 并列，
                 让面板右半边有内容而不是一片留白 -->
            <aside class="hero-side">
              <div v-if="gateSplit && minScore > 0" class="side-block">
                <div class="side-head">
                  <span class="side-key">{{ t('dashboard.sideGateTitle') }}</span>
                  <span class="side-val">
                    {{ gateSplit.passed }}<span class="side-of">/{{ gateSplit.total }}</span>
                  </span>
                </div>
                <div class="side-bar" role="img" :aria-label="t('dashboard.sideGateTitle')">
                  <span
                    class="side-bar-pass"
                    :style="{ width: `${(gateSplit.passed / gateSplit.total) * 100}%` }"
                  />
                </div>
              </div>

              <div v-if="lowestProjects.length" class="side-block">
                <div class="side-head">
                  <span class="side-key">{{ t('dashboard.sideLowest') }}</span>
                  <el-button
                    text
                    size="small"
                    class="side-more"
                    @click="router.push({ name: 'projects' })"
                  >
                    {{ t('dashboard.sideAll') }}
                  </el-button>
                </div>
                <ul class="side-list">
                  <li
                    v-for="p in lowestProjects"
                    :key="p.id"
                    @click="openProject(p.id)"
                  >
                    <span class="side-name truncate">{{ p.name }}</span>
                    <span class="side-score" :class="scoreClass(p.score)">{{ p.score.toFixed(1) }}</span>
                  </li>
                </ul>
              </div>
            </aside>
          </div>

          <!-- 全历史刻度尺是辅助信息，放在面板下面 -->
          <div v-if="averageScore !== null" class="gauge">
            <div class="gauge-track">
              <div class="gauge-fill" :style="{ width: `${averageScore}%` }" />
              <div v-if="minScore > 0" class="gauge-gate" :style="{ left: `${minScore}%` }" />
              <span
                v-if="history.worst !== null"
                class="gauge-mark gauge-mark-worst"
                :style="{ left: `${history.worst}%` }"
              />
              <span class="gauge-mark gauge-mark-now" :style="{ left: `${averageScore}%` }" />
              <span
                v-if="history.best !== null"
                class="gauge-mark gauge-mark-best"
                :style="{ left: `${history.best}%` }"
              />
            </div>
            <div class="gauge-foot">
              <span class="gauge-foot-items">
                <span v-if="history.worst !== null">
                  <b>{{ t('dashboard.gaugeWorst') }}</b> {{ history.worst.toFixed(1) }}
                </span>
                <span><b>{{ t('dashboard.gaugeNow') }}</b> {{ averageScore.toFixed(1) }}</span>
                <span v-if="minScore > 0">
                  <b>{{ t('dashboard.gaugeLine') }}</b> {{ minScore }}
                </span>
                <span v-if="history.best !== null">
                  <b>{{ t('dashboard.gaugeBest') }}</b> {{ history.best.toFixed(1) }}
                </span>
              </span>
              <span class="gauge-foot-scale">0 → 100</span>
            </div>
          </div>
        </div>

        <div class="hero-card card card-invert">
          <span class="label hero-card-label">{{ securityRisk.label }}</span>
          <div class="display-sm">{{ securityRisk.headline }}</div>
          <p class="hero-card-detail">{{ securityRisk.detail }}</p>

          <div v-if="criticalProjects.length" class="risk-projects">
            <div class="risk-projects-head">
              <span>{{ t('dashboard.affectedProjects') }}</span>
              <span class="risk-projects-count">{{ criticalProjects.length }}</span>
            </div>
            <ul class="risk-list">
              <!-- 这里不加 stagger-item：hero 刻意不做整体入场，而错峰样式依赖
                   .reveal 祖先，挂上也永远不会触发 -->
              <li
                v-for="project in criticalProjects"
                :key="project.id"
                @click="openProject(project.id)"
              >
                <span class="truncate">{{ project.name }}</span>
                <b>{{ project.count }}</b>
              </li>
            </ul>
          </div>
        </div>
      </section>

      <!-- 严重问题提示：不用整片红底——在黑白页面上那是最重的色块，
           会抢走评分环的注意力。改成「一条红边 + 墨色正文」：
           颜色只留在左侧 3px 的线和标题前的图标上，页面其余部分仍是黑白。 -->
      <aside v-if="totalIssues.critical > 0" class="critical-note" v-reveal role="status">
        <span class="critical-note-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="16" height="16">
            <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M8 4.4v4.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.6" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <div class="critical-note-body">
          <p class="critical-note-title">{{ t('dashboard.criticalAlertTitle') }}</p>
          <p class="critical-note-text">{{ criticalSummary }}</p>
        </div>
      </aside>

      <section class="tiles" v-reveal>
        <StatTile
          :label="t('dashboard.statOpenIssues')"
          :value="issueCount"
          :hint="t('dashboard.statAcross', { count: analysed.length })"
        />
        <StatTile
          :label="t('dashboard.statCritical')"
          :value="totalIssues.critical"
          :tone="totalIssues.critical > 0 ? 'score-bad' : 'score-good'"
          :hint="t('dashboard.statRequiresAction')"
        />
        <StatTile
          :label="t('dashboard.statHigh')"
          :value="totalIssues.error"
          :tone="totalIssues.error > 0 ? 'score-warn' : 'score-good'"
          :hint="t('dashboard.statErrorLevel')"
        />
        <StatTile
          :label="t('dashboard.statProjects')"
          :value="projects.length"
          :hint="t('dashboard.statAnalysed', { count: analysed.length })"
        />
        <StatTile
          :label="t('dashboard.statCoverage')"
          :value="coverage.display"
          :tone="coverage.tone"
          :hint="coverage.hint"
        />
      </section>

      <section class="card section" v-reveal>
        <h2 class="section-title">{{ t('dashboard.sectionIssueMix') }}</h2>
        <p class="section-sub">{{ t('dashboard.sectionIssueMixSub') }}</p>
        <SeverityBar :counts="totalIssues" legend />
      </section>

      <section class="card section" v-reveal>
        <h2 class="section-title">{{ t('dashboard.sectionScoreHistory') }}</h2>
        <p class="section-sub">{{ t('dashboard.sectionScoreHistorySub') }}</p>
        <Suspense>
          <TrendChart :trend="aggregateTrend" />
          <template #fallback>
            <div class="chart-skeleton" />
          </template>
        </Suspense>
      </section>

      <div class="split">
        <section class="card section" v-reveal>
          <h2 class="section-title">{{ t('dashboard.sectionNeedsAttention') }}</h2>
          <p class="section-sub">{{ t('dashboard.sectionNeedsAttentionSub') }}</p>
          <ul class="plain-list">
            <li
              v-for="(project, i) in needsAttention"
              :key="project.id"
              class="plain-row clickable stagger-item"
              :style="{ '--i': i }"
              @click="openProject(project.id)"
            >
              <span class="sev-dot" :style="{ background: project.color }" />
              <span class="grow truncate">{{ project.name }}</span>
              <span class="score" :class="scoreClass(project.latest?.score ?? 0)">
                {{ project.latest?.score.toFixed(1) }}
              </span>
              <!-- 扫描进行中时没有门禁结果，显示 gate 键会渲染成未解析的 `gate.` -->
              <el-tag
                v-if="project.latest?.gateStatus"
                :type="statusTagType(project.latest.gateStatus)"
                size="small"
                effect="plain"
              >
                {{ t(`gate.${project.latest.gateStatus}`) }}
              </el-tag>
              <el-tag
                v-else-if="project.latest"
                :type="statusTagType(project.latest.status)"
                size="small"
                effect="plain"
              >
                {{ t(`status.${project.latest.status}`) }}
              </el-tag>
            </li>
          </ul>
        </section>

        <section class="card section" v-reveal>
          <h2 class="section-title">{{ t('dashboard.sectionRecent') }}</h2>
          <p class="section-sub">{{ t('dashboard.sectionRecentSub') }}</p>
          <ul class="plain-list">
            <li
              v-for="(item, i) in recent"
              :key="item.analysis.id"
              class="plain-row clickable stagger-item"
              :style="{ '--i': i }"
              @click="openProject(item.project.id)"
            >
              <div class="stack grow">
                <span class="truncate">{{ item.project.name }}</span>
                <span class="soft body-sm">{{ formatRelative(item.analysis.createdAt) }}</span>
              </div>
              <span class="score" :class="scoreClass(item.analysis.score)">
                {{ item.analysis.score.toFixed(1) }}
              </span>
              <span class="soft body-sm">
                {{ t('dashboard.issuesCount', item.analysis.totalIssues) }}
              </span>
            </li>
          </ul>
        </section>
      </div>
    </template>
  </div>
</template>

<style scoped>
.dashboard {
  display: flex;
  flex-direction: column;
  gap: var(--s-lg);
}

.empty-band {
  padding: var(--s-xxl) var(--s-xl);
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--s-md);
}

.empty-band h1 {
  margin: 0;
}

.lead {
  margin: 0;
  max-width: 52ch;
  color: var(--text-body);
}

.empty-code {
  background: var(--surface);
  color: var(--text-body);
  max-width: 100%;
}

.hero {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--s-xl);
  align-items: stretch;
  padding: var(--s-lg) 0 var(--s-sm);
}

/* 评分卡：和右侧强调卡同一套外壳（白底、细描边、柔和投影）。
   里面再嵌一块浅一档的面板托住评分、评级和摘要——demo 的做法：
   分数是主角，所以它坐在一块被围起来的面上，而不是和页面融成一片。 */
.hero-main {
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
  padding: var(--s-md);
  border-radius: var(--r-lg);
  transition: box-shadow 0.4s var(--ease-out);
}

.hero-panel {
  position: relative;
  overflow: hidden;
  display: grid;
  /* 评分环 | 结论与统计 | 右栏 */
  grid-template-columns: auto minmax(0, 1.1fr) minmax(0, 1fr);
  align-items: center;
  gap: var(--s-xl);
  padding: var(--s-lg);
  min-width: 0;
  /* 撑满卡片里除刻度尺之外的剩余高度：右侧强调卡更高，网格会把这张卡拉到
     同样高，面板不跟着长高就会在底部留一大块空白 */
  flex: 1 1 auto;
  border-radius: var(--r-md);
  background: var(--bg-sunken);
  border: 1px solid var(--border);
}

/* 面板右半边的底纹，只是质感，不承载信息。
   用 slice 保证任何高度下圆都是正圆，不会被拉成椭圆。
   左边淡出：右栏的分隔线在那里，圆环全强度穿过会像把线截断了。 */
.wave-rings {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 56%;
  height: 100%;
  color: var(--border);
  opacity: 0.85;
  pointer-events: none;
  z-index: 0;
  -webkit-mask-image: linear-gradient(to right, transparent, #000 22%);
  mask-image: linear-gradient(to right, transparent, #000 22%);
}

/* 除底纹外的内容都压在它上面 */
.hero-panel > :not(.wave-rings) {
  position: relative;
  z-index: 1;
}

/* 右栏：两块小信息，用一条竖线和中间栏分开 */
.hero-side {
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
  min-width: 0;
  padding-left: var(--s-lg);
  border-left: 1px solid var(--border);
}

.side-block {
  min-width: 0;
}

.side-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--s-xs);
  margin-bottom: 6px;
}

.side-key {
  font-size: 11px;
  letter-spacing: 0.04em;
  color: var(--text-soft);
  white-space: nowrap;
}

.side-val {
  font-size: 15px;
  font-weight: 640;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.side-of {
  margin-left: 1px;
  font-size: 11px;
  font-weight: 500;
  color: var(--text-soft);
}

/* 双色条：通过的部分是绿色，其余留轨道色 */
.side-bar {
  position: relative;
  height: 6px;
  border-radius: var(--r-pill);
  overflow: hidden;
  background: var(--surface-strong);
}

.side-bar-pass {
  display: block;
  height: 100%;
  background: var(--sev-ok);
  border-radius: inherit;
  transition: width 0.4s var(--ease-out);
}

.side-more {
  font-size: 11px;
  padding: 0;
  height: auto;
  color: var(--text-muted);
}

.side-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
}

.side-list li {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--s-xs);
  padding: 3px 0;
  font-size: 12.5px;
  cursor: pointer;
  color: var(--text-body);
}

.side-list li:hover {
  color: var(--text);
}

.side-name {
  min-width: 0;
}

.side-score {
  font-weight: 620;
  font-variant-numeric: tabular-nums;
}

.hero-panel > .hero-detail {
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
}

/* 一句话结论，压过旁边的说明。demo 的 h2 放在这一格的最上方。 */
.hero-heading {
  margin: 0 0 4px;
  font: var(--t-title-md);
  font-size: 19px;
  font-weight: 620;
  letter-spacing: -0.02em;
  color: var(--text);
}

.hero-sub {
  margin: 0 0 var(--s-md);
  font: var(--t-body-sm);
  font-size: 13px;
  color: var(--text-muted);
  max-width: 44ch;
  line-height: 1.45;
}

/* 三个数字排在同一基线上，间距拉开，评分环变大后这一行有空间 */
.stat-row {
  display: flex;
  gap: var(--s-xl);
}

.stat-cell {
  display: flex;
  flex-direction: column;
  gap: 1px;
  min-width: 0;
}

.stat-key {
  font-size: 11.5px;
  letter-spacing: 0.04em;
  color: var(--text-soft);
  white-space: nowrap;
}

.stat-val {
  font-size: 23px;
  font-weight: 640;
  letter-spacing: -0.03em;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

/* 全历史刻度尺是辅助信息，间距交给卡片自己的 gap，不再额外加外边距 */
.gauge {
  margin: 0 var(--s-sm) var(--s-xxs);
}

.gauge-track {
  position: relative;
  height: 8px;
  border-radius: var(--r-pill);
  background: var(--surface-card);
}

.gauge-fill {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: var(--r-pill);
  background: var(--sev-warning);
}

.gauge-gate {
  position: absolute;
  top: -5px;
  bottom: -5px;
  width: 2px;
  background: var(--text);
  opacity: 0.5;
}

.gauge-mark {
  position: absolute;
  top: 50%;
  width: 13px;
  height: 13px;
  border-radius: 50%;
  transform: translate(-50%, -50%);
  border: 2px solid var(--bg);
}

.gauge-mark-now {
  background: var(--sev-warning);
}
.gauge-mark-best {
  background: var(--sev-ok);
}
.gauge-mark-worst {
  background: var(--sev-critical);
}

.gauge-foot {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--s-md);
  margin-top: 8px;
  font: var(--t-caption);
  font-size: 11px;
  color: var(--text-soft);
}

.gauge-foot-items {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-sm);
}

.gauge-foot b {
  font-weight: 600;
  color: var(--text-muted);
}

.gauge-foot-scale {
  flex: none;
}

/* 涉及项目放在一块凹槽里：比卡底低一档，顶部一道内阴影表示压进去 */
.risk-projects {
  margin-top: var(--s-sm);
  padding: 0 var(--s-sm);
  border-radius: var(--r-md);
  background: var(--bg-sunken);
  border: 1px solid var(--border);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.04);
}

.risk-projects-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--s-xs);
  margin-bottom: 2px;
  padding-top: var(--s-sm);
  font: var(--t-caption-up);
  font-size: 11px;
  letter-spacing: var(--ls-caption-up);
  text-transform: uppercase;
  color: var(--text-muted);
}

.risk-projects-count {
  font-variant-numeric: tabular-nums;
}

.risk-list {
  height: 110px;
  overflow-y: auto;
  overscroll-behavior: contain;
  margin: 0;
  padding: 0 10px 0 0;
  list-style: none;
}

.risk-list li {
  display: flex;
  justify-content: space-between;
  gap: var(--s-xs);
  height: 22px;
  line-height: 22px;
  font: var(--t-body-sm);
  font-size: 13px;
  cursor: pointer;
  opacity: 0.92;
}

.risk-list li:hover {
  opacity: 1;
}

.risk-list li b {
  flex: none;
  font-variant-numeric: tabular-nums;
}

.risk-list::-webkit-scrollbar {
  width: 6px;
}

.risk-list::-webkit-scrollbar-track {
  background: transparent;
}

.risk-list::-webkit-scrollbar-thumb {
  background: var(--line-strong);
  border-radius: var(--r-pill);
}

.risk-list::-webkit-scrollbar-thumb:hover {
  background: var(--text-soft);
}

/* 强调卡：白底和其它卡一样，靠更重的投影和描边抬起来，不靠上色。
   单色方案里没有彩色状态卡，含义由标签和数字承担 */
.hero-card {
  width: 300px;
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
  padding: var(--s-lg);
  background: var(--surface);
  border-color: var(--card-line);
  box-shadow: var(--shadow-raised);
}

.hero-card:hover {
  box-shadow: var(--shadow-raised-hover);
}

.hero-card-label {
  color: var(--text-muted);
}

.hero-card-detail {
  margin: 0;
  font: var(--t-body-sm);
  color: var(--text-muted);
  line-height: 1.5;
}

.risk-list li:hover {
  background: var(--surface);
}

.risk-list::-webkit-scrollbar-thumb {
  background: var(--line-strong);
}

.risk-list::-webkit-scrollbar-thumb:hover {
  background: var(--text-soft);
}

.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: var(--s-md);
}

.section {
  padding: var(--s-lg);
}

/* 严重问题提示。整片红底在黑白页面上是最重的色块，会和右侧强调卡、评分环
   抢注意力；这里只把颜色留在左侧一条 3px 的红线和图标上，文字用墨色。
   阅读顺序仍是「先看到线，再读结论」——但页面主色保持黑白。 */
.critical-note {
  display: flex;
  align-items: flex-start;
  gap: var(--s-sm);
  padding: var(--s-sm) var(--s-md) var(--s-sm) var(--s-md);
  border-left: 3px solid var(--sev-critical);
  border-radius: 0 var(--r-md) var(--r-md) 0;
  background: var(--bg-sunken);
}

.critical-note-mark {
  flex: 0 0 auto;
  display: flex;
  margin-top: 1px;
  color: var(--sev-critical);
}

.critical-note-body {
  min-width: 0;
}

.critical-note-title {
  margin: 0 0 2px;
  font: var(--t-title-sm);
  font-weight: 600;
  color: var(--text);
}

.critical-note-text {
  margin: 0;
  font: var(--t-body-sm);
  color: var(--text-muted);
  line-height: 1.55;
}



/* .section-title / .section-sub 定义在 main.css，全站共用一套尺度 */

.split {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--s-md);
}

.plain-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.plain-row {
  display: flex;
  align-items: center;
  gap: var(--s-sm);
  padding: 10px 0;
  border-bottom: 1px solid var(--border-soft);
}

.plain-row:last-child {
  border-bottom: none;
}

.clickable {
  cursor: pointer;
  margin: 0 calc(var(--s-xs) * -1);
  padding-left: var(--s-xs);
  padding-right: var(--s-xs);
  border-radius: var(--r-sm);
}

.clickable:hover {
  background: var(--surface-soft);
}

.grow {
  flex: 1 1 auto;
  min-width: 0;
}

.truncate {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  /* flex 子项默认 min-width: auto，长项目名会把整行撑宽而不是收成省略号 */
  min-width: 0;
}

.score {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.chart-skeleton {
  height: 220px;
  border-radius: var(--r-md);
  background: var(--surface-soft);
}

@media (max-width: 1000px) {
  .hero {
    grid-template-columns: 1fr;
    gap: var(--s-lg);
    align-items: start;
  }
  .hero-card {
    width: 100%;
  }
  .split {
    grid-template-columns: 1fr;
  }
}

/* 三栏在窄屏会挤到 200px 以下，项目名截成空白，改为两行：环和结论并排，右栏落到下面 */
@media (max-width: 1180px) {
  .hero-panel {
    grid-template-columns: auto minmax(0, 1fr);
    gap: var(--s-lg);
  }
  .hero-side {
    grid-column: 1 / -1;
    flex-direction: row;
    align-items: center;
    gap: var(--s-xl);
    padding-left: 0;
    padding-top: var(--s-md);
    border-left: none;
    border-top: 1px solid var(--border);
  }
  .hero-side > .side-block {
    flex: 1 1 0;
  }
}

/* 更窄时整体单列堆叠 */
@media (max-width: 720px) {
  .hero-panel {
    grid-template-columns: 1fr;
    justify-items: start;
  }
  .hero-side {
    flex-direction: column;
    align-items: stretch;
    gap: var(--s-md);
  }
}
</style>
