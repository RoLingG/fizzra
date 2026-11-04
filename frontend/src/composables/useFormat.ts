import { useI18n } from 'vue-i18n'

import {
  formatAxisDate,
  formatDate,
  formatPercent,
  formatRelative,
  renderRelative,
  severityKey,
} from '@/api/format'

// 在 setup 中调用，读 locale.value 建立响应式依赖
export function useFormat() {
  const { t, te, locale } = useI18n()

  return {
    t,

    severityLabel: (severity: string) => t(`severity.${severityKey(severity)}`),

    // 无法识别的状态回退原始值
    statusLabel: (status: string) => {
      const raw = String(status).toLowerCase()
      const name = raw === 'false_positive' ? 'falsePositive' : raw
      const key = `status.${name}`
      return te(key) ? t(key) : String(status)
    },

    triggerLabel: (trigger: string) => {
      const map: Record<string, string> = {
        manual: 'project.triggerManual',
        cli: 'project.triggerCli',
        ci: 'project.triggerCi',
        schedule: 'project.triggerSchedule',
      }
      const key = map[String(trigger).toLowerCase()]
      return key ? t(key) : String(trigger)
    },

    analyzerLabel: (name: string) => {
      const key = `analyzer.${String(name).toLowerCase()}.name`
      return te(key) ? t(key) : String(name)
    },

    // JS/TS 用缩写以适配内联渲染，表单下拉框空间充足，仍用全称
    languageLabel: (language: string) => {
      if (language === 'javascript') return t('projectsView.languageShort_javascript')
      const key = `projectsView.language_${language}`
      return te(key) ? t(key) : String(language)
    },

    // 传入 locale.value 以便随语言更新
    formatDate: (value: string | null | undefined) => formatDate(value, locale.value),

    // 轴标签不走 locale，保持数字等宽
    formatAxisDate,

    formatRelative: (value: string | null | undefined) =>
      renderRelative(formatRelative(value), locale.value, {
        never: t('common.never'),
        justNow: t('common.justNow'),
      }),

    formatPercent,
  }
}
