<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

import type { TrendPoint } from '@/api/types'
import { severityColorVar } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { useTheme } from '@/composables/useTheme'

echarts.use([
  LineChart,
  BarChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  MarkLineComponent,
  CanvasRenderer,
])

const props = defineProps<{
  trend: TrendPoint[]
  minScore?: number
}>()

const scoreEl = ref<HTMLDivElement | null>(null)
const mixEl = ref<HTMLDivElement | null>(null)

let scoreChart: echarts.ECharts | null = null
let mixChart: echarts.ECharts | null = null
let observer: ResizeObserver | null = null

const hasTrend = computed(() => props.trend.length > 0)

const { isDark } = useTheme()

// 响应式读取 locale，语言切换时触发重绘
const { t, severityLabel, formatDate, formatAxisDate } = useFormat()

// 等 DOM 稳定后再重建图表
function rerender() {
  nextTick(render)
}

// 主题变化时同步重绘，不能推迟到 nextTick
function renderNow() {
  render()
}

function cssVar(name: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

// 把 var(--x) 解析成真实颜色值
function resolveCssColor(value: string, fallback: string): string {
  if (typeof window === 'undefined') return fallback
  const match = /^var\((--[a-z0-9-]+)\)$/i.exec(value.trim())
  if (!match) return value
  return cssVar(match[1], fallback)
}

function textColor() {
  return cssVar('--text-muted', '#6a6a6a')
}

function gridColor() {
  return cssVar('--border', '#e5e5e5')
}

function scoreLineColor() {
  return cssVar('--text', '#0a0a0a')
}

function buildScoreOption(): echarts.EChartsCoreOption {
  const points = props.trend

  return {
    animationDuration: 400,
    grid: { left: 44, right: 16, top: 16, bottom: 28, containLabel: false },
    tooltip: {
      trigger: 'axis',
      confine: true,
      formatter: (params: unknown) => {
        const list = params as Array<{ dataIndex: number }>
        const point = points[list[0]?.dataIndex ?? 0]
        if (!point) return ''
        return [
          `<strong>${formatDate(point.at)}</strong>`,
          t('chart.tooltipScore', { score: point.score.toFixed(1) }),
          t('chart.tooltipIssues', {
            total: point.total,
            critical: point.critical,
            errors: point.errors,
          }),
          // 空的 gateStatus 会插出未解析的 `gate.` 键
          ...(point.gateStatus ? [t('chart.tooltipGate', { status: t(`gate.${point.gateStatus}`) })] : []),
        ].join('<br/>')
      },
    },
    xAxis: {
      type: 'category',
      data: points.map((p) => formatAxisDate(p.at)),
      axisLine: { lineStyle: { color: gridColor() } },
      axisLabel: { color: textColor(), fontSize: 11, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      min: 0,
      max: 100,
      splitLine: { lineStyle: { color: gridColor() } },
      axisLabel: { color: textColor(), fontSize: 11 },
    },
    series: [
      {
        name: t('chart.seriesScore'),
        type: 'line',
        smooth: true,
        symbol: 'circle',
        symbolSize: 7,
        data: points.map((p) => p.score),
        lineStyle: { width: 2, color: scoreLineColor() },
        itemStyle: { color: scoreLineColor() },
        areaStyle: { color: scoreLineColor(), opacity: 0.08 },
        markLine:
          props.minScore && props.minScore > 0
            ? {
                silent: true,
                symbol: 'none',
                label: {
                  formatter: t('chart.gateMarkLine', { score: props.minScore }),
                  color: textColor(),
                  fontSize: 10,
                  position: 'insideEndTop',
                },
                lineStyle: { color: cssVar('--sev-warning', '#cb8a15'), type: 'dashed', width: 1 },
                data: [{ yAxis: props.minScore }],
              }
            : undefined,
      },
    ],
  }
}

function buildMixOption(): echarts.EChartsCoreOption {
  const points = props.trend

  return {
    animationDuration: 400,
    // 底部留出图例与轴标签的空间
    grid: { left: 44, right: 16, top: 16, bottom: 52, containLabel: false },
    tooltip: {
      trigger: 'axis',
      confine: true,
      // 透明阴影保留吸附又不遮挡数据
      axisPointer: { type: 'shadow', shadowStyle: { color: 'transparent' } },
      formatter: (params: unknown) => {
        const list = params as Array<{ dataIndex: number; seriesName: string; value: number }>
        const point = points[list[0]?.dataIndex ?? 0]
        if (!point) return ''
        const rows = list
          .filter((p) => p.value > 0)
          .map((p) => `${p.seriesName}: <strong>${p.value}</strong>`)
          .join('<br/>')
        return `<strong>${formatDate(point.at)}</strong><br/>${rows || t('chart.noIssues')}`
      },
    },
    xAxis: {
      type: 'category',
      data: points.map((p) => formatAxisDate(p.at)),
      axisLine: { lineStyle: { color: gridColor() } },
      axisLabel: { color: textColor(), fontSize: 11, hideOverlap: true },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      minInterval: 1,
      splitLine: { lineStyle: { color: gridColor() } },
      axisLabel: { color: textColor(), fontSize: 11 },
    },
    legend: {
      bottom: 0,
      itemWidth: 10,
      itemHeight: 10,
      // 加大图例间距
      itemGap: 14,
      textStyle: { color: textColor(), fontSize: 11 },
    },
    series: [
      stackSeries(severityLabel('critical'), points.map((p) => p.critical), barColor('critical')),
      stackSeries(severityLabel('error'), points.map((p) => p.errors), barColor('error')),
      stackSeries(severityLabel('warning'), points.map((p) => p.warnings), barColor('warning')),
    ],
    // 关闭悬停状态动画
    stateAnimation: { duration: 0 },
  }
}

function barColor(severity: string): string {
  const token = severityColorVar(severity)
  return resolveCssColor(token, cssVar('--sev-info', '#1d4ed8'))
}

function stackSeries(name: string, data: number[], color: string) {
  return {
    name,
    type: 'bar' as const,
    stack: 'issues',
    data,
    itemStyle: { color },
    barMaxWidth: 26,
    emphasis: { focus: 'none' as const },
  }
}

function render() {
  if (scoreEl.value && hasTrend.value) {
    scoreChart ??= echarts.init(scoreEl.value)
    scoreChart.setOption(buildScoreOption(), true)
  }
  if (mixEl.value && hasTrend.value) {
    mixChart ??= echarts.init(mixEl.value)
    mixChart.setOption(buildMixOption(), true)
  }
}

function resize() {
  scoreChart?.resize()
  mixChart?.resize()
}

onMounted(() => {
  render()

  // 共用 observer，可捕捉容器尺寸变化
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(() => resize())
    if (scoreEl.value) observer.observe(scoreEl.value)
    if (mixEl.value) observer.observe(mixEl.value)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  scoreChart?.dispose()
  mixChart?.dispose()
  scoreChart = null
  mixChart = null
})

// 主题变化时同步重绘
watch(isDark, renderNow, { flush: 'sync' })

// 语言变化时重建 option
watch(
  () => formatDate(props.trend[0]?.at ?? null),
  rerender,
)

watch(() => props.trend, render, { deep: true })

defineExpose({ resize })
</script>

<template>
  <div v-if="hasTrend" class="charts">
    <section>
      <h3 class="section-title">{{ t('chart.scoreHistory') }}</h3>
      <p class="hint muted">{{ t('chart.completedAnalyses', trend.length) }}</p>
      <div ref="scoreEl" class="chart" />
    </section>

    <section>
      <h3 class="section-title">{{ t('chart.issueMix') }}</h3>
      <p class="hint muted">{{ t('chart.issueMixSub') }}</p>
      <div ref="mixEl" class="chart" />
    </section>
  </div>

  <el-empty v-else :description="t('chart.noAnalyses')" />
</template>

<style scoped>
.charts {
  display: grid;
  grid-template-columns: 1fr;
  gap: 18px;
}

.charts .section-title {
  font: var(--t-title-sm);
  margin: 0;
  color: var(--text);
}

.hint {
  font-size: 12px;
  color: var(--text-muted);
  margin: 2px 0 10px;
}

.chart {
  width: 100%;
  height: 220px;
}

@media (min-width: 1100px) {
  .charts {
    grid-template-columns: 1fr 1fr;
  }
}
</style>
