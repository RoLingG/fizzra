import { readonly, ref } from 'vue'

// 状态放模块作用域让所有调用方共享。副作用都在 document/localStorage 上，用不着 Pinia

export type ThemeChoice = 'light' | 'dark' | 'auto'

/** 须与 index.html 内联脚本的键一致 */
export const THEME_STORAGE_KEY = 'gg:theme'

const CHOICES: ThemeChoice[] = ['light', 'dark', 'auto']

const darkQuery = '(prefers-color-scheme: dark)'

function readStored(): ThemeChoice {
  try {
    const raw = localStorage.getItem(THEME_STORAGE_KEY)
    if (raw && (CHOICES as string[]).includes(raw)) return raw as ThemeChoice
  } catch {
// 私密模式下 localStorage 会抛错
  }
  return 'auto'
}

function systemPrefersDark(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia(darkQuery).matches
}

const choice = ref<ThemeChoice>(readStored())

/** ECharts 等读不了 CSS 变量的组件靠它知道何时重读调色板 */
const isDark = ref(choice.value === 'dark' || (choice.value === 'auto' && systemPrefersDark()))

/** 引擎没有 View Transition 时走降级路径 */
function supportsViewTransition(): boolean {
  return (
    typeof document !== 'undefined' &&
    typeof document.startViewTransition === 'function' &&
    // 用户要求减少动效时跳过，省掉抓页面快照的开销
    !window.matchMedia('(prefers-reduced-motion: reduce)').matches
  )
}

/** 应用并提交一次主题变更 */
function apply(next: ThemeChoice) {
  if (!supportsViewTransition()) {
    commit(next)
    return
  }

  // 过渡进行中时直接应用新选择
  if (document.documentElement.dataset.themeTransition === 'running') {
    commit(next)
    return
  }

  document.documentElement.dataset.themeTransition = 'running'
  const transition = document.startViewTransition(() => commit(next))
  // 过渡被跳过时 finished 会 reject，标志位照样要清
  transition.finished.finally(() => {
    delete document.documentElement.dataset.themeTransition
  })
}

/** 标签页图标跟随主题：日间墨底纸芯，夜间反过来
 *
 * index.html 的内联脚本在首屏前已经设过一次，这里负责覆盖用户切换主题的情况。
 * 找不到元素时静默跳过——测试环境里没有这段 DOM。 */
function syncFavicon(dark: boolean) {
  if (typeof document === 'undefined') return
  const icon = document.getElementById('favicon') as HTMLLinkElement | null
  if (icon) icon.href = dark ? '/webicon-night.png' : '/webicon-day.png'
}

function commit(next: ThemeChoice) {
  choice.value = next

  const root = document.documentElement
  const dark = next === 'dark' || (next === 'auto' && systemPrefersDark())

  // 移除属性表示跟随系统
  if (next === 'auto') {
    delete root.dataset.theme
  } else {
    root.dataset.theme = next
  }

  // 原生滚动条和表单控件读的是 colorScheme，不是 CSS 变量
  root.style.colorScheme = dark ? 'dark' : 'light'

  syncFavicon(dark)

  try {
    if (next === 'auto') {
      localStorage.removeItem(THEME_STORAGE_KEY)
    } else {
      localStorage.setItem(THEME_STORAGE_KEY, next)
    }
  } catch {
    // 存不进去（私密模式）也照常生效
  }

  isDark.value = dark
}

// 固定主题时 isDark 由 apply 直接设置，这里只负责 auto 实时跟随
if (typeof window !== 'undefined' && window.matchMedia) {
  window.matchMedia(darkQuery).addEventListener('change', () => {
    if (choice.value === 'auto') apply('auto')
  })
}

/** 修改主题的唯一入口 */
export function useTheme() {
  return {
    choice: readonly(choice),
    isDark: readonly(isDark),
    setTheme: apply,
  }
}
