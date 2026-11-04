import { createI18n } from 'vue-i18n'

import en from './en'
import zh from './zh'

// 实例放这里以断开 client.ts → i18n → App.vue → client.ts 的导入循环

export type Locale = 'en' | 'zh'

// 必须与 index.html 内联脚本读取的键一致
export const LOCALE_STORAGE_KEY = 'gg:locale'

export const LOCALES: Locale[] = ['en', 'zh']

function isLocale(value: unknown): value is Locale {
  return value === 'en' || value === 'zh'
}

export function pickInitialLocale(): Locale {
  try {
    const stored = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (isLocale(stored)) return stored
  } catch {
    // 私密模式下 localStorage 会抛错，落到浏览器偏好
  }

  if (typeof navigator !== 'undefined' && navigator.language) {
    return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
  }
  return 'en'
}

export const i18n = createI18n({
  // legacy 模式下 locale 不是 ref，直接赋值不触发重渲染
  legacy: false,
  globalInjection: true,
  locale: pickInitialLocale(),
  // 缺译文时回退英文
  fallbackLocale: 'en',
  messages: { en, zh },
})

// 须写 .value
export function setLocale(next: Locale) {
  i18n.global.locale.value = next
  document.documentElement.lang = next

  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, next)
  } catch {}
}
