import { ref } from 'vue'
import type { Easing } from 'motion-v'

// 全站动效的唯一来源：时长、缓动、位移都从这里取，页面里不再各写一套

const reducedMotionQuery = '(prefers-reduced-motion: reduce)'

function prefersReduced(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia(reducedMotionQuery).matches
}

// 响应式而非一次性读取：系统设置中途改变时页面应当立刻跟进
export const reduceMotion = ref(prefersReduced())

if (typeof window !== 'undefined' && window.matchMedia) {
  const mq = window.matchMedia(reducedMotionQuery)
  const sync = () => {
    reduceMotion.value = mq.matches
  }
  // addEventListener 在旧 Safari 上不存在，退回到已废弃的 addListener
  if (mq.addEventListener) mq.addEventListener('change', sync)
  else mq.addListener(sync)
}

// 克制为主，位移不超过 16px，时长不超过 0.5s

/** 四参数形式需要元组类型，普通 number[] 过不了类型检查 */
export const EASE_OUT: Easing = [0.22, 1, 0.36, 1] as const

// 页面切换只做淡入，路由 key 变化时 motion-v 换掉整个元素。
// 不再带 y 位移，页面里的区块各自有 v-reveal 的 26px 上浮，
// 外层再位移一次，两层 transform 叠加后首屏位移会超过 30px。
// 不做退场同理，mode="wait" 要等退场完成回调才挂载新页面，回调一旦不来页面就空着。
//
// 滚动渐现、列表错峰、卡片海浪都不在这里，前两者是 CSS（.reveal / .stagger-item），
// 后者是 useCardWave.ts 的 WAAPI，同一棵子树只由一套系统写 transform。
export const pageVariants = {
  enter: { opacity: 0 },
  center: { opacity: 1, transition: { duration: 0.4, ease: EASE_OUT } },
}
