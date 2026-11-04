/*
 * 卡片海浪：移植自 demo/ui-redesign 终版的悬停动效。
 *
 * 三层深浅不同的灰圆从指针处涌出，第四层是卡片自身的底色，从同一个圆心追上
 * 并把前三层盖掉，浪退去，卡片回到原本的样子。指针每进入一次卡片播放一轮，
 * 离开再进来会重新播放。
 *
 * 之所以不用 CSS 过渡：四层需要各自的延迟、各自的最终半径，还要在相邻卡片间
 * 共用同一个"水面"计时。motion-v 的 `animate`（底层是 WAAPI）三样都能做，
 * 不必自己维护 rAF 循环。
 *
 * 之所以把"水面"放在模块级变量而不是组件内的 composable：它是整页共享的。
 * 指针沿一行卡片移动时应该读作一条连续的浪，所以下一张卡接续上一张的进度，
 * 而不是从零开始。这份状态没法留在某个组件里。
 */
import { animate } from 'motion-v'

/* motion-v 从 framer-motion/dom 转出了 `animate`，但没有转出它的返回类型
 * 直接依赖 motion-dom 会把版本钉在一个内部包上，这里用 ReturnType 取同一个类型 */
type Playback = ReturnType<typeof animate>

export interface WaveLayer {
  /** 这一层开始扩张前的等待秒数 */
  delay: number
  /** 从开始到铺满所需的秒数 */
  duration: number
  /** 最终旋转角，各层不同，波浪形的边缘才不会对齐 */
  rotate: number
}

/* 四层几乎同时起跑（间隔 120ms），速度略有差异
 * 结果是三四圈大小相近的环互相追逐，像涟漪，而不是一块灰忽然铺满卡片。
 * 最后一层是卡片底色，用时最短，在最后一段追上并把灰环盖平。
 *
 * 差异必须小但非零：完全同速会得到完美同心圆，也就是一整块纯色。 */
export const WAVE_SPECS: WaveLayer[] = [
  { delay: 0, duration: 2.25, rotate: 16 },
  { delay: 0.12, duration: 2.2, rotate: -13 },
  { delay: 0.24, duration: 2.15, rotate: 9 },
  { delay: 0.36, duration: 2.05, rotate: 3 },
]

export const WAVE_LAYERS = WAVE_SPECS.length

/* 最终直径 = 卡片短边 × 这个系数。这是绝对值而不是下限
 * 圆的边缘允许露出在卡片里，正是这点让它读起来像波浪。
 * 若按"盖住远角"来确定尺寸，每张卡都会变成一大块看不出形状的圆。 */
export const WAVE_DIAMETER = 2.0

/* 一轮浪在最长那层走完 85% 之前算作"仍然可见"
 * 之后半径已到最终尺寸的九成，圆大半在卡片之外。过了这个点再共享进度，
 * 新卡的各层会全部呈已完成状态，等于没有动画。 */
const VISIBLE_SHARE = 0.85

const LONGEST = Math.max(...WAVE_SPECS.map((s) => s.delay + s.duration))
export const WAVE_SHARE_MS = LONGEST * VISIBLE_SHARE * 1000

/** 与 demo 的 cubic-bezier(.16,.84,.24,1) 同一族 */
const EASE = [0.16, 0.84, 0.24, 1] as const

/** 上面这条缓动在 0→1 区间几乎等同 easeOutExpo
 *  接续一轮浪时用它反推某一层此刻应处的半径 */
function easeOutExpo(t: number): number {
  return t >= 1 ? 1 : 1 - Math.pow(2, -10 * t)
}

interface WaveBox {
  el: HTMLDivElement
  layers: HTMLSpanElement[]
  runs: Playback[]
  played: boolean
  /* 这一轮跑完后摘掉 will-change 的计时器，0 表示没有在排 */
  settleTimer: number
}

const boxes = new WeakMap<HTMLElement, WaveBox>()

/* 当前"水面"：这一轮浪开始的时间，整页只有一个
 * 下一张卡只在 WAVE_SHARE_MS 之内读它，超出则重新起浪 */
let waterStartedAt = 0

const reducedMotion =
  typeof window !== 'undefined' && window.matchMedia
    ? window.matchMedia('(prefers-reduced-motion: reduce)')
    : null

export function getWaveBox(card: HTMLElement): WaveBox | undefined {
  return boxes.get(card)
}

function stopAll(box: WaveBox) {
  for (const run of box.runs) {
    try {
      run.stop()
    } catch {
      /* 已经结束 */
    }
  }
  box.runs = []
  if (box.settleTimer) {
    clearTimeout(box.settleTimer)
    box.settleTimer = 0
  }
  for (const layer of box.layers) layer.classList.remove('is-running')
}

/* 一轮浪走完之后摘掉 will-change。
 *
 * 这个属性让浏览器为每个层单独建一张合成层，静态挂着会一直占显卡内存；
 * 只在动画期间存在的收益是一样的。留一点余量再摘，避免最后一帧掉回 CPU 合成 */
function scheduleSettle(box: WaveBox) {
  if (box.settleTimer) clearTimeout(box.settleTimer)
  box.settleTimer = window.setTimeout(() => {
    box.settleTimer = 0
    for (const layer of box.layers) layer.classList.remove('is-running')
  }, LONGEST * 1000 + 200)
}

function hide(box: WaveBox) {
  stopAll(box)
  for (const layer of box.layers) {
    layer.style.visibility = 'hidden'
    layer.style.opacity = '0'
  }
}

/**
 * 给一张卡片挂上四层海浪。
 * 可以重复调用：每个元素只创建一次容器，重渲染后继续复用。
 */
export function attachWave(card: HTMLElement): void {
  if (boxes.has(card)) return

  const el = document.createElement('div')
  el.className = 'card-wave'
  el.setAttribute('aria-hidden', 'true')
  const layers: HTMLSpanElement[] = []
  for (let i = 0; i < WAVE_LAYERS; i++) {
    el.appendChild(document.createElement('span'))
    layers.push(el.lastElementChild as HTMLSpanElement)
  }
  card.prepend(el)

  boxes.set(card, { el, layers, runs: [], played: false, settleTimer: 0 })
}

/**
 * 在 `event`（卡片内的一个指针位置）处播放一轮浪。
 * 用户偏好减少动效时直接返回，此时由 main.css 的 `.card:hover` 浅色高亮作为降级。
 */
export function playWave(card: HTMLElement, event: { clientX: number; clientY: number }): void {
  const box = boxes.get(card)
  if (!box || reducedMotion?.matches) return
  if (box.played) return

  const rect = card.getBoundingClientRect()
  /* 隐藏标签页里的卡片量出来是 0×0，照常播放等于白建四层、白跑一轮动画。
   * 太扁的元素（表格行、提示条）也不播：浪在里面一闪而过，观感像抖动 */
  if (rect.width < 80 || rect.height < 80) return
  box.played = true

  const x = event.clientX - rect.left
  const y = event.clientY - rect.top
  const shortSide = Math.min(rect.width, rect.height)

  for (const layer of box.layers) {
    layer.style.width = `${shortSide}px`
    layer.style.height = `${shortSide}px`
    layer.style.left = `${x}px`
    layer.style.top = `${y}px`
  }

  const now = performance.now()
  const inherited = waterStartedAt && now - waterStartedAt < WAVE_SHARE_MS ? now - waterStartedAt : 0
  if (!inherited) waterStartedAt = now

  stopAll(box)
  const total = LONGEST * VISIBLE_SHARE * 1000

  /* will-change 只在动画期间挂上，跑完由 scheduleSettle 摘掉 */
  for (const layer of box.layers) layer.classList.add('is-running')
  scheduleSettle(box)

  WAVE_SPECS.forEach((spec, i) => {
    const layer = box.layers[i]
    layer.style.visibility = 'visible'

    const skipped = Math.min(inherited, total) / 1000
    const local = skipped - spec.delay
    const done = local >= spec.duration

    if (done) {
      layer.style.transform = `scale(${WAVE_DIAMETER}) rotate(${spec.rotate}deg)`
      layer.style.opacity = '1'
      return
    }

    // 接续一轮浪时从上一张卡已经走到的半径开始，而不是从零
    // 否则每跨一张卡，圆都会从头重长一遍
    const from =
      local > 0 ? 0.03 + (WAVE_DIAMETER - 0.03) * easeOutExpo(local / spec.duration) : 0.03

    box.runs.push(
      animate(
        layer,
        { scale: [from, WAVE_DIAMETER], rotate: [0, spec.rotate] },
        {
          duration: spec.duration - Math.max(0, local),
          delay: Math.max(0, spec.delay * 1000 - skipped * 1000) / 1000,
          ease: EASE,
        },
      ),
      animate(layer, { opacity: [0, 1] }, { duration: 0.22 }),
    )
  })
}

/**
 * 指针离开这张卡：清掉"已播放"标记并隐藏四层圆。
 * 只在离开时调用，悬停期间不动它——悬停中清掉的话，指针一经过子元素
 * 就会把整轮浪重新播一遍。
 */
export function settleWave(card: HTMLElement): void {
  const box = boxes.get(card)
  if (!box) return
  box.played = false
  hide(box)
}

/** 指针进入时播放，离开时收起 */
export function onWaveEnter(card: HTMLElement, event: PointerEvent): void {
  playWave(card, event)
}

export function onWaveLeave(card: HTMLElement): void {
  settleWave(card)
}

/* ---------------------------------------------------------------------------
 * 全局委托
 *
 * 不在每个视图里给卡片打标记（三十多个元素，下一个新页面很容易漏），
 * 而是等指针第一次进入匹配 CARD_SELECTOR 的元素时才挂上四层圆。
 * 整个功能只由 document 上一个监听器负责，异步数据渲染出来的卡片也不需要额外接线。
 *
 * 选择器刻意收得很窄。一轮浪要占用四层合成层，
 * 放在五十像素高的表格行里只会是一闪，观感像抖动，
 * 所以行用的是"抽屉抽出"那套 hover（见 main.css）。
 * ------------------------------------------------------------------------- */
export const CARD_SELECTOR = '.card'

const attached = new WeakSet<HTMLElement>()

/** 立即挂上四层圆，并在元素整个生命周期内保持挂载 */
export function ensureWave(card: HTMLElement): void {
  if (attached.has(card)) return

  /* 尺寸不够的卡片不挂四层 DOM。这里量的是当前可见性，
   * 隐藏标签页里的卡片要等它真正显示出来、指针再进入一次才会挂上 */
  const rect = card.getBoundingClientRect()
  if (rect.width < 80 || rect.height < 80) return

  attached.add(card)
  card.classList.add('card-wave-host')
  attachWave(card)
}

let installed = false

/* 指针当前所在的卡片。`pointermove` 只在指针位于这张卡上时移动圆心，
 * 否则拖过整个页面时会不断往已经离开的卡片写样式 */
let hoveredCard: HTMLElement | null = null

/* 指针平移时 pointermove 最多每像素触发一次
 * 合并到一帧只写一次样式，否则快速划过时每层两个属性会累计成上百次写入 */
let originFrame = 0
let pendingOrigin: { card: HTMLElement; clientX: number; clientY: number } | null = null

export function installCardWaves(): void {
  if (installed || typeof document === 'undefined') return
  installed = true

  document.addEventListener(
    'pointerover',
    (event) => {
      const target = event.target as Element | null
      const card = target?.closest?.(CARD_SELECTOR) as HTMLElement | null
      if (!card) return
      ensureWave(card)
      // 进入子元素会再次触发 pointerover
      // playWave 自己的 `played` 标记会让重复调用变成空操作，不需要重新挂载
      if (card.contains(event.relatedTarget as Node | null)) return
      hoveredCard = card
      playWave(card, event)
    },
    { passive: true },
  )

  document.addEventListener(
    'pointerout',
    (event) => {
      const target = event.target as Element | null
      const card = target?.closest?.(CARD_SELECTOR) as HTMLElement | null
      if (!card || !attached.has(card)) return
      // 在子元素之间移动不算离开卡片
      if (card.contains(event.relatedTarget as Node | null)) return
      if (hoveredCard === card) hoveredCard = null
      settleWave(card)
    },
    { passive: true },
  )

  /* 指针在已经起浪的卡片内移动：换圆心，保半径
   *
   * 浪从指针进入卡片的位置长出。没有这个监听器，指针横穿卡片时环会钉在
   * 最初的进入点上，看起来像"浪不理会鼠标"。这里只改 left/top，
   * 正在跑的 scale 动画不动，所以环保持当前进度，只是跟着走。 */
  document.addEventListener(
    'pointermove',
    (event) => {
      const card = hoveredCard
      // 路由切换时卡片整个被卸掉，不会派发 pointerout，hoveredCard 会指向
      // 已经脱离文档的节点。留着它只会往不可见的元素写样式，这里顺手清掉
      if (!card) return
      if (!card.isConnected) {
        hoveredCard = null
        return
      }
      const box = boxes.get(card)
      if (!box || reducedMotion?.matches) return

      /* 坐标先攒着，测量和写入都留到 rAF 里做。
       * 在事件回调里调 getBoundingClientRect 会在有待处理样式时强制同步布局，
       * 而 rAF 里读、写聚在一起，浏览器只用算一次 */
      pendingOrigin = { card, clientX: event.clientX, clientY: event.clientY }
      if (originFrame) return
      originFrame = requestAnimationFrame(() => {
        originFrame = 0
        const next = pendingOrigin
        pendingOrigin = null
        // 事件到这一帧之间，指针可能已经离开，或者卡片已随路由切换被替换
        if (!next || next.card !== hoveredCard || !boxes.has(next.card)) return
        const rect = next.card.getBoundingClientRect()
        const left = `${next.clientX - rect.left}px`
        const top = `${next.clientY - rect.top}px`
        for (const layer of boxes.get(next.card)!.layers) {
          layer.style.left = left
          layer.style.top = top
        }
      })
    },
    { passive: true },
  )
}
