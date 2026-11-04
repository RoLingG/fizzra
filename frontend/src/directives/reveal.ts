/*
 * `v-reveal`：demo 里的滚动渐现。
 *
 * 元素初始隐藏并下沉 26px，进入视口时淡入并上浮到位。从哪个方向离开视口，
 * 回来时就从哪个方向进场，从上方滚出去的回来时从上往下落，从下方出去的
 * 回来时从下往上浮。这样页面不会每次都重放同一个进场。
 *
 * 错峰由 CSS 负责：给组内元素写 `--i`（见 main.css 的 `.stagger-item`），
 * 按下标递增延迟。指令本身不接管错峰，所有挂 v-reveal 的地方节奏一致。
 *
 * 整页共用一个 IntersectionObserver。挂载即动画的做法会让所有元素同时触发，
 * 包括还在首屏之外的。
 */
import type { Directive } from 'vue'

const OFFSET = '0px 0px -12% 0px'

const DOWN = new WeakMap<HTMLElement, boolean>()

/* 每个元素的重播函数，供 `restartReveal` 调用 */
const REPLAY = new WeakMap<HTMLElement, () => void>()

/* 每个元素当前的轮次。快速连续切换时，上一轮排队的 rAF 会晚于新一轮的「移除」
 * 执行，把元素提前设为显示，新一轮就没有动画了。只有轮次仍然匹配才加回 */
const ROUND = new WeakMap<HTMLElement, number>()

let observer: IntersectionObserver | null = null

/* 把元素打回未显示状态，下一帧再触发入场。
 *
 * 用在标签页这种「内容一直在 DOM 里、只是被 v-show 藏起来」的场景：
 * 观察器早已判定它进入视口，切回来时不会再触发一次。
 *
 * 两步必须分在相邻两帧。同一个 tick 里「移除 revealed → 读 offsetWidth → 加回
 * revealed」只强制了布局，浏览器还没把 opacity: 0 画出来就把类加回去了，
 * 过渡没有起点，元素直接跳到终态。
 *
 * 首次切到某个面板时看起来是正常的，因为面板刚从 display: none 转为可见，
 * 浏览器本来就要重新布局一次；之后的切换面板已在布局树里，同步翻转就会失效。
 * 这也是「第一次有动效、之后没有」的来源。
 *
 * 放 rAF 里还有一层原因：rAF 回调在「样式 → 布局 → 绘制」之后执行，
 * 此时隐藏态已经落到屏幕上，改回 revealed 才是一次真实的变化。
 */
function replayNow(el: HTMLElement) {
  // 隐藏状态下移除没有意义：浏览器不为 display: none 的元素建立过渡
  if (!el.offsetParent) return

  /* 元素上可能还留着上一次没播完的过渡。直接改类的话，浏览器把新目标值插进已存在
   * 的那段过渡里，动画停在中间值不再前进，看起来就是「切回来没有动效」。
   *
   * 先关掉过渡、把类移除，让元素当场落回初始态（没有过渡就不会插值），
   * 下一帧再恢复过渡并加回 revealed，这一次才是干净的 0 → 1 */
  const targets = [el, ...el.querySelectorAll<HTMLElement>('.stagger-item')]
  for (const target of targets) target.style.transition = 'none'

  el.classList.remove('revealed')
  // 标签页切换是从下方浮入，沿用别处遗留下来的 is-down 会让它反着从上往下落
  el.classList.remove('is-down')

  const round = (ROUND.get(el) ?? 0) + 1
  ROUND.set(el, round)

  // 读一次布局，让「已移除 revealed」这一状态落到样式系统里，过渡才有起点
  void el.offsetWidth

  requestAnimationFrame(() => {
    if (!el.isConnected || ROUND.get(el) !== round) return
    // 清掉行内覆盖，让 CSS 里的过渡规则重新生效
    for (const target of targets) target.style.transition = ''
    // 强制一次样式结算，确保「过渡已恢复、仍处于隐藏态」这一帧成立
    void el.offsetWidth
    el.classList.add('revealed')
  })
}

/** 让一个挂了 v-reveal 的元素重新播放入场。元素没挂过指令时为空操作 */
export function restartReveal(el: Element | null | undefined): void {
  if (el instanceof HTMLElement) REPLAY.get(el)?.()
}

function getObserver(): IntersectionObserver | null {
  if (typeof IntersectionObserver === 'undefined') return null
  if (observer) return observer

  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        const el = entry.target as HTMLElement
        if (entry.isIntersecting) {
          el.classList.add('revealed')
          el.classList.remove('is-down')
          continue
        }
        // 只有元素完全离开视口才处理。仅凭 -12% 的 rootMargin，
        // 靠近底部的元素会在用户滚动几个像素时反复进出观察范围，闪个不停
        //
        // 被 v-show 藏起来的面板量出来全是 0，会被误判成「从上方离开」并打上
        // is-down，切回来时整个面板就从上往下落。隐藏元素不参与这个判断
        if (entry.boundingClientRect.width === 0 && entry.boundingClientRect.height === 0) {
          continue
        }
        const rect = entry.boundingClientRect
        const goneAbove = rect.bottom <= 1
        const goneBelow = rect.top >= window.innerHeight - 1
        if (!goneAbove && !goneBelow) continue

        el.classList.toggle('is-down', goneAbove)
        el.classList.remove('revealed')
      }
    },
    /* threshold 取 0：元素露出一点就算进入。
     *
     * 原来用的是 0.12，对高度超过视口的长区块不成立——它永远达不到 12% 可见，
     * 于是整块停在不显示状态。rootMargin 仍然提前 12% 触发，滚到之前就应该开始播放，
     * 动画因此不会挤在滚动的那几帧里 */
    { threshold: 0, rootMargin: OFFSET },
  )

  return observer
}

export const reveal: Directive<HTMLElement> = {
  mounted(el) {
    el.classList.add('reveal')
    DOWN.set(el, false)

    const io = getObserver()
    if (!io) {
      // 没有 IntersectionObserver（很旧的浏览器，或测试环境里的 jsdom）时直接显示
      el.classList.add('revealed')
      // 没有观察器就不再监听滚动，replay 仍然可用
      REPLAY.set(el, () => replayNow(el))
      return
    }

    /* 首屏元素不等观察器回调。
     *
     * IntersectionObserver 的回调是异步的，最快也要等到下一帧。在这之前元素一直是
     * `opacity: 0`。如果回调晚了一两帧，用户看到的就是「先露出来，再淡入」，
     * 切页时尤其明显，因为页面本身的入场动画是同步开始的，两套节奏对不上。
     *
     * 下面几个 API 各自的作用：
     *
     *   getBoundingClientRect()  返回元素在屏幕上的实际位置。top / bottom 都相对视口，
     *                            top 为 0 就是视口最上沿。它会先强制浏览器完成一次布局，
     *                            所以拿到的一定是最终位置。
     *   window.innerHeight       视口高度。乘 0.88 是为了和观察器的 rootMargin -12%
     *                            对齐：观察器认为「元素顶部进入视口下方 12% 以内」才算可见，
     *                            两处判定口径一致，元素才不会一会儿显示一会儿收回。
     *   requestAnimationFrame()  把回调安排到下一帧渲染之前。浏览器每帧的顺序是
     *                            「算样式 → 布局 → 绘制 → 执行 rAF 回调」，
     *                            所以回调执行时，上一帧的 opacity: 0 已经真实生效。
     *   element.isConnected     元素是否还在文档里。等了一帧之后 Vue 可能已经把它卸载了。
     *
     * 为什么必须等一帧、不能当场加类：如果在同一个 tick 里先加 `reveal`（opacity: 0）
     * 再加 `revealed`（opacity: 1），浏览器只在最后一次算样式，看到的是「1 → 1」，
     * 没有任何变化，CSS transition 根本不会跑，元素会直接出现。
     * 等一帧让浏览器先把 0 画出来，之后改成 1 才是一次真实的变化，过渡才有起点。 */
    const rect = el.getBoundingClientRect()
    const inViewport = rect.top < window.innerHeight * 0.88 && rect.bottom > 0
    if (inViewport) {
      requestAnimationFrame(() => {
        if (el.isConnected) el.classList.add('revealed')
      })
    }

    io.observe(el)
    REPLAY.set(el, () => replayNow(el))
  },

  unmounted(el) {
    observer?.unobserve(el)
    DOWN.delete(el)
    REPLAY.delete(el)
    ROUND.delete(el)
  },
}
