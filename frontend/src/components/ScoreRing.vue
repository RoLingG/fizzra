<script setup lang="ts">
/*
 * 整体评分：一个分数环加评级字母。
 *
 * 数字和环由同一条缓动的 rAF 循环推进，改时长时两者必须一起改，否则停不稳。
 * 动画只在挂载时播放一次，之后停在结束状态；prefers-reduced-motion 下直接落定。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { scoreGrade } from '@/api/format'

const props = defineProps<{
  /** 0–100，尚无任何分析时为 null */
  score: number | null
  /** 门禁线，在环上画成一小段刻度，0 表示未配置 */
  gate?: number
}>()

const { t } = useI18n()

/* 字母下方的刻度条，六格 */
const GRADE_SCALE = ['A', 'B', 'C', 'D', 'E', 'F'] as const

/* 色调分三档，与环的颜色一致，字母沿用仪表盘其它评分的同一组颜色 */
const toneOf = (score: number): 'good' | 'ok' | 'bad' =>
  score >= 85 ? 'good' : score >= 70 ? 'ok' : 'bad'

/* 128 的 viewBox 里 r = 55，周长取 2πr */
const C = 2 * Math.PI * 55

const display = ref('0.0')
const offset = ref(C)
const grade = ref<string>('')
const gradeTone = ref<'good' | 'ok' | 'bad'>('good')

const entered = ref(false)
const evacuated = ref(false)
/** F 级数字正在崩落时为 true */
const dropping = ref(false)
/** 每个数字的抛射距离、角度和延迟，按字符下标索引 */
const digitStyle = ref<Record<string, { dx: string; dr: string; delay: string }>>({})

const label = computed(() => t('dashboard.overallQuality'))
const desc = computed(() => t(`dashboard.grade.${grade.value.toLowerCase()}`))

const reduced =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-reduced-motion: reduce)')
    : null

/** 每次播放自增，异步回调据此判断自己是否已经过期 */
let token = 0
let raf = 0
const timers: number[] = []

function clearTimers() {
  for (const id of timers) window.clearTimeout(id)
  timers.length = 0
  cancelAnimationFrame(raf)
}

const easeOutCubic = (t: number) => 1 - Math.pow(1 - t, 3)

function finish(value: number) {
  display.value = value.toFixed(1)
  offset.value = C - (C * value) / 100
  grade.value = scoreGrade(value)
  gradeTone.value = toneOf(value)
  entered.value = true
  evacuated.value = false
  dropping.value = false
}

function play() {
  clearTimers()
  const mine = ++token
  const target = props.score

  if (target === null) {
    display.value = '—'
    offset.value = C
    grade.value = ''
    entered.value = false
    return
  }

  if (reduced?.matches) {
    finish(target)
    return
  }

  // 从空环和 0.0 开始往上数
  // 显示只到一位小数，`shown` 在文本没变时跳过写入，60fps 下大多数帧都属于这种
  display.value = '0.0'
  offset.value = C
  grade.value = ''
  entered.value = false
  evacuated.value = false
  dropping.value = false
  digitStyle.value = {}

  const start = performance.now()
  const dur = 1400
  let shown = '0.0'
  // TS 不会把外面 `target !== null` 的收窄带进闭包，这里显式绑成 number。
  const goal: number = target

  function tick(now: number) {
    if (mine !== token) return
    const p = Math.min(1, (now - start) / dur)
    const eased = easeOutCubic(p)
    const text = (goal * eased).toFixed(1)
    if (text !== shown) {
      shown = text
      display.value = text
    }
    offset.value = C - (C * goal * eased) / 100
    if (p < 1) raf = requestAnimationFrame(tick)
    else onCountDone(mine, goal)
  }
  raf = requestAnimationFrame(tick)
}

function onCountDone(mine: number, target: number) {
  gradeTone.value = toneOf(target)
  grade.value = scoreGrade(target)
  // 先摘掉再挂上入场类，字母的 CSS 入场动画才能重播
  // 不这么做的话只有元素第一次创建时播一次
  entered.value = false
  requestAnimationFrame(() => {
    if (mine !== token) return
    entered.value = true
  })

  // 只有 F 级会崩落：先让读者看清完整分数，再看着它散架
  if (grade.value === 'F') {
    timers.push(
      window.setTimeout(() => {
        if (mine === token) shatter(String(target.toFixed(1)))
      }, 1400),
    )
  }
}

/** 每个数字有各自的抛射角度和 80ms 错峰，否则会整齐地一起下坠
 *  左右交替取值，这里的随机数只影响一次崩落，不参与任何计算 */
function shatter(text: string) {
  const next: Record<string, { dx: string; dr: string; delay: string }> = {}
  const list = [...text]
  // 只有数字会掉，小数点原地不动
  // 所以错峰序号只为数字递增，判断“全部落地”的时刻也只数数字
  let n = 0
  list.forEach((ch, i) => {
    if (ch === '.') return
    // 左右交错，每个字掉的方向和角度都不同，才不像整齐下坠
    const dir = n % 2 === 0 ? -1 : 1
    next[i] = {
      dx: `${dir * (8 + Math.random() * 18)}px`,
      dr: `${dir * (28 + Math.random() * 50)}deg`,
      delay: `${n * 80}ms`,
    }
    n++
  })
  digitStyle.value = next
  dropping.value = true
  // 会动的都已经落地，只剩小数点。
  // 这里不能清 `dropping`：清掉会移除动画类，`animation-fill-mode` 的结束态随之失效，
  // 数字会弹回原位。结束状态由 `evacuated` 表示。
  timers.push(
    window.setTimeout(
      () => {
        evacuated.value = true
      },
      Math.max(0, n - 1) * 80 + 1100,
    ),
  )
}

/** 单个数字的内联样式。
 *  必须是 CSS 字符串而不是对象，对象字面量里的自定义属性 IDE 追溯不到使用处，会报无法解析。 */
function digitCss(i: number): string | undefined {
  const s = digitStyle.value[i]
  if (!s) return undefined
  return `--dx: ${s.dx}; --dr: ${s.dr}; animation-delay: ${s.delay};`
}

const chars = computed(() => [...display.value])

/* 动画只在组件挂载时播放一次。
 *
 * 不能监听 `props.score`：仪表盘轮询时浮点平均值会重新算出来，重播会让
 * F 级数字“掉完又长回来”。分数真正变化时伴随一次路由切换，那时自然重新挂载。
 * `gate` 同样不触发重播，它来自第二个较慢的请求。 */
onMounted(play)
onBeforeUnmount(clearTimers)
</script>

<template>
  <div class="score-ring" :class="`tone-${gradeTone}`">
    <div class="ring-wrap">
      <svg class="ring-svg" viewBox="0 0 128 128" aria-hidden="true">
        <circle class="ring-track" cx="64" cy="64" r="55" fill="none" stroke-width="7" />
        <circle
          class="ring-fill"
          cx="64"
          cy="64"
          r="55"
          fill="none"
          stroke-width="7"
          :stroke-dasharray="C.toFixed(2)"
          :stroke-dashoffset="offset.toFixed(2)"
        />
      </svg>
      <!-- 门禁刻度。整个 SVG 已经旋转了 -90° 从 12 点起画，
           所以这里的坐标已是「0 度 = 正上方」，不能再减 90° -->
      <svg v-if="gate && gate > 0" class="ring-gate" viewBox="0 0 128 128" aria-hidden="true">
        <line
          :x1="64 + 51 * Math.cos((gate * Math.PI) / 180)"
          :y1="64 + 51 * Math.sin((gate * Math.PI) / 180)"
          :x2="64 + 59 * Math.cos((gate * Math.PI) / 180)"
          :y2="64 + 59 * Math.sin((gate * Math.PI) / 180)"
          stroke-width="2"
        />
      </svg>
      <div class="ring-center">
        <!-- 数字掉光之后屏幕上只剩小数点，读屏不应该再念已经掉掉的分数 -->
        <div
          class="ring-value"
          :class="{ evacuated }"
          :aria-label="evacuated ? t('dashboard.scoreUnavailable') : display"
        >
          <!-- 静止状态就是一个文本节点。
               只有 F 级崩落时才拆成 span，否则小数点会一直受 `.sep` 的宽度和外边距影响。 -->
          <template v-if="!dropping">{{ display }}</template>
          <template v-else>
            <!-- 掉落的 span 用 `opacity: 0` 留在 DOM 里，不能用 v-show。
                 `display: none` 会把它们移出布局，小数点成为唯一子元素并重新居中。 -->
            <span
              v-for="(ch, i) in chars"
              :key="i"
              :class="ch === '.' ? 'sep' : 'drop dropping'"
              :style="ch === '.' ? undefined : digitCss(i)"
              >{{ ch }}</span
            >
          </template>
        </div>
        <div class="ring-label">{{ label }}</div>
      </div>
    </div>

    <div class="grade-block">
      <div class="grade-line" :aria-live="'polite'">
        <!-- 换了 key，字母变化时入场动画才会重播 -->
        <span
          v-if="grade"
          :key="grade + String(entered)"
          class="grade"
          :class="[`grade-${grade.toLowerCase()}`, { 'grade-enter': entered }]"
          :data-tone="gradeTone"
          :aria-label="`${t('dashboard.gradeLabel')} ${grade}`"
          >{{ grade }}</span
        >
        <span v-if="grade" class="grade-desc">{{ desc }}</span>
      </div>
      <!-- A–F 刻度条，`on` 类标出当前档位 -->
      <div class="grade-scale" aria-hidden="true">
        <span
          v-for="g in GRADE_SCALE"
          :key="g"
          :class="{ on: g === grade }"
          >{{ g }}</span
        >
      </div>
    </div>
  </div>
</template>

<style scoped>
.score-ring {
  /* 整块的尺寸只由这一个变量决定：环、分数、字母都读它
     调整卡片饱满程度时只改这一处，不用同时追三个数字 */
  --ring: 152px;
  display: flex;
  align-items: center;
  gap: 26px;
  min-width: 0;
}

.ring-wrap {
  position: relative;
  width: var(--ring);
  height: var(--ring);
  flex: 0 0 auto;
}

.ring-svg,
.ring-gate {
  position: absolute;
  inset: 0;
  width: var(--ring);
  height: var(--ring);
  /* 旋转 -90°，让进度从 12 点方向开始，而不是 3 点 */
  transform: rotate(-90deg);
}

.ring-track {
  stroke: var(--line);
}

/* 按色调上色而不是统一用墨色，字母用的是同一组三种颜色
   这样环和评级读起来是一件事 */
.ring-fill {
  stroke: var(--ok);
  stroke-linecap: round;
  /* 进度由 JS 逐帧写入，这里加过渡会和它打架 */
  transition: none;
}

.tone-ok .ring-fill {
  stroke: var(--warn);
}
.tone-bad .ring-fill {
  stroke: var(--danger);
}

.ring-gate line {
  stroke: var(--text);
  opacity: 0.45;
}

.ring-center {
  position: absolute;
  inset: 0;
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 1px;
}

.ring-value {
  display: block;
  /* 高度和基线固定，F 级掉光后留下的那一个小数点就落在原本的位置上
     数字消失时不会有任何位移 */
  min-height: 40px;
  font-size: 40px;
  font-weight: 660;
  letter-spacing: -0.04em;
  line-height: 40px;
  color: var(--text);
  font-variant-numeric: tabular-nums;
  text-align: center;
}

.ring-label {
  margin-top: 5px;
  font-size: 11px;
  letter-spacing: 0.06em;
  color: var(--text-soft);
  text-align: center;
}

/* --- F 级崩落的数字 ------------------------------------------------------ */
.ring-value span {
  display: inline-block;
  will-change: transform, opacity;
}
.ring-value .sep {
  width: 0.12em;
  margin-right: 0.12em;
}

/* 崩落关键帧读取的两个属性的默认值。
   必须在这里声明：IDE 按“是否有声明处”判断自定义属性，写成关键帧里的
   var() 兜底会被报成无法解析。运行时 JS 会按数字逐个覆盖。 */
.ring-value .drop {
  --dx: 6px;
  --dr: 38deg;
}

.ring-value span.drop.dropping {
  animation: digit-drop 1.1s cubic-bezier(0.5, 0, 0.75, 0) both;
}
@keyframes digit-drop {
  0% {
    transform: none;
    opacity: 1;
  }
  22% {
    transform: translate(2px, -4px) rotate(7deg) scale(1.06);
  }
  100% {
    transform: translate(var(--dx), 48px) rotate(var(--dr)) scale(0.9);
    opacity: 0;
  }
}

/* 数字掉光后小数点留在原位：它仍是同一行里居中的一个内联 span，
   `.sep` 保留着旁边还有十分位时的宽度和外边距。
   去掉这个外边距会让孤零零的小数点偏移。 */

/* --- 评级字母 ------------------------------------------------------------ */
/* 环和字母之间的竖线用 `--border` 而不是 `--line`
   后者是表格行之间那条几乎看不见的发丝线，1px 摆在 40px 的数字旁边会彻底消失 */
.grade-block {
  align-self: stretch;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding-left: 24px;
  border-left: 1px solid var(--border);
}

.grade-line {
  display: flex;
  align-items: center;
  gap: 10px;
  /* 高度固定，字母变化时这一块不会跳 */
  height: 52px;
}

.grade {
  display: inline-block;
  min-width: 1ch;
  font-size: 52px;
  font-weight: 700;
  line-height: 1;
  letter-spacing: -0.04em;
  transform-origin: 60% 70%;
  color: var(--text-soft);
}

.grade[data-tone='good'] {
  color: var(--ok);
}
.grade[data-tone='ok'] {
  color: var(--warn);
}
.grade[data-tone='bad'] {
  color: var(--danger);
}

.grade-desc {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-muted);
  white-space: nowrap;
}

/* 字母下方的刻度条，六格
 * 业务上的分级更细，带加号的档位与它的基础字母共用一格 */
.grade-scale {
  display: flex;
  gap: 2px;
  margin-top: 2px;
}

/* 每格下面的短横线。`--line`（#ededed）是表格行之间的发丝线，
 * 放在 #fafafa 的面板上完全看不出来。`--line-strong` 深一档，
 * 才呈现为六段清晰的小横线。当前档位的横线变为墨色。 */
.grade-scale span {
  width: 15px;
  padding-bottom: 3px;
  border-bottom: 2px solid var(--line-strong);
  text-align: center;
  font-size: 10px;
  font-weight: 600;
  color: var(--text-soft);
  transition:
    color 0.35s var(--ease-out),
    border-color 0.35s var(--ease-out);
}

.grade-scale span.on {
  color: var(--text);
  border-bottom-color: var(--text);
}

/* 各档位各自的入场方式：分数越高越利落，F 级是碎裂 */
.grade-enter.grade-a,
.grade-enter.grade-b {
  animation: grade-pop 0.7s var(--ease-soft) both;
}
.grade-enter.grade-c,
.grade-enter.grade-d,
.grade-enter.grade-e {
  animation: grade-tilt 0.8s var(--ease-out) both;
}
.grade-enter.grade-f {
  animation: grade-shatter 1s var(--ease-out) both;
}

@keyframes grade-pop {
  0% {
    transform: scale(0.4) rotate(-8deg);
    opacity: 0;
  }
  60% {
    transform: scale(1.14) rotate(2deg);
    opacity: 1;
  }
  100% {
    transform: scale(1) rotate(0);
  }
}
@keyframes grade-tilt {
  0% {
    transform: translateY(10px) rotate(6deg);
    opacity: 0;
  }
  100% {
    transform: none;
    opacity: 1;
  }
}
@keyframes grade-shatter {
  0% {
    transform: scale(1.3);
    opacity: 0;
    filter: blur(4px);
  }
  35% {
    transform: scale(0.92);
    opacity: 1;
    filter: blur(0);
  }
  50% {
    transform: scale(1.06) rotate(-3deg);
  }
  100% {
    transform: none;
  }
}

@media (prefers-reduced-motion: reduce) {
  .grade-enter {
    animation: none;
  }
  .ring-value span.drop.dropping {
    animation: none;
  }
}
</style>
