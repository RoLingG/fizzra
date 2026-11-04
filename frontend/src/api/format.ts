import type { GateStatus, ProjectKind, Severity } from '@/api/types'

// 纯展示函数，不依赖 i18n，需要翻译请用 useFormat

export function severityClass(severity: Severity | string): string {
  return `sev sev-${String(severity).toLowerCase()}`
}

export function severityDotClass(severity: Severity | string): string {
  return `sev-dot sev-${String(severity).toLowerCase()}`
}

export const SEVERITY_ORDER: Severity[] = ['critical', 'error', 'warning', 'info']

// 项目的框架名，只认 wails
export function projectFramework(kind: string | undefined): ProjectKind {
  return kind === 'wails' ? 'wails' : ''
}

export function severityKey(severity: Severity | string): Severity {
  const key = String(severity).toLowerCase()
  return SEVERITY_ORDER.includes(key as Severity) ? (key as Severity) : 'info'
}

export function severityColorVar(severity: Severity | string): string {
  return `var(--sev-${severityKey(severity)})`
}

/** 六档 A–F，阈值须与 Go 侧 quality.Grade 一致 */
export function scoreGrade(score: number): string {
  if (score >= 90) return 'A'
  if (score >= 80) return 'B'
  if (score >= 70) return 'C'
  if (score >= 60) return 'D'
  if (score >= 50) return 'E'
  return 'F'
}

export function scoreClass(score: number): string {
  if (score >= 85) return 'score-good'
  if (score >= 70) return 'score-ok'
  if (score >= 50) return 'score-warn'
  return 'score-bad'
}

export function gateTagType(status: GateStatus | string): 'success' | 'danger' | 'info' {
  switch (status) {
    case 'passed':
      return 'success'
    case 'failed':
      return 'danger'
    default:
      return 'info'
  }
}

export function statusTagType(status: string): 'success' | 'danger' | 'info' | 'warning' {
  switch (status) {
    case 'success':
      return 'success'
    case 'failed':
      return 'danger'
    case 'running':
      return 'warning'
    default:
      return 'info'
  }
}

export function intlTag(locale: string): string {
  return locale === 'zh' ? 'zh-CN' : 'en-US'
}

export function formatDate(value: string | null | undefined, locale = 'en'): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(intlTag(locale), {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// 不用 formatDate：带年份和月份名放上轴会互相越界，数字形式各语言等宽，切语言不重排
export function formatAxisDate(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// 返回结构化结果，措辞由调用方决定
export function formatRelative(
  value: string | null | undefined,
): { kind: 'never' } | { kind: 'justNow' } | { kind: 'duration'; value: number; unit: Intl.RelativeTimeFormatUnit } {
  if (!value) return { kind: 'never' }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return { kind: 'never' }

  const seconds = Math.round((Date.now() - date.getTime()) / 1000)
  if (seconds < 45) return { kind: 'justNow' }

  const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
    ['second', 60],
    ['minute', 60],
    ['hour', 24],
    ['day', 7],
    ['week', 4.35],
    ['month', 12],
    ['year', Number.POSITIVE_INFINITY],
  ]

  let value_ = -seconds
  for (const [unit, divisor] of units) {
    if (Math.abs(value_) < divisor) {
      return { kind: 'duration', value: Math.round(value_), unit }
    }
    value_ = value_ / divisor
  }
  return { kind: 'duration', value: Math.round(value_), unit: 'year' }
}

export function renderRelative(
  rel: ReturnType<typeof formatRelative>,
  locale: string,
  words: { never: string; justNow: string },
): string {
  switch (rel.kind) {
    case 'never':
      return words.never
    case 'justNow':
      return words.justNow
    default:
      return new Intl.RelativeTimeFormat(intlTag(locale), { numeric: 'auto' }).format(
        rel.value,
        rel.unit,
      )
  }
}

export function formatDuration(ms: number): string {
  if (!ms || ms < 0) return '—'
  if (ms < 1000) return `${ms} ms`
  const seconds = ms / 1000
  if (seconds < 60) return `${seconds.toFixed(1)} s`
  const minutes = Math.floor(seconds / 60)
  const rest = Math.round(seconds % 60)
  return `${minutes}m ${rest}s`
}

export function formatPercent(value: number, digits = 1): string {
  if (!Number.isFinite(value)) return '—'
  return `${value.toFixed(digits)}%`
}

export function truncate(value: string, max = 120): string {
  if (!value) return ''
  if (value.length <= max) return value
  return `${value.slice(0, max - 1)}…`
}

export function fileDir(path: string): string {
  const idx = path.lastIndexOf('/')
  return idx > 0 ? path.slice(0, idx) : '.'
}

export function fileBase(path: string): string {
  const idx = path.lastIndexOf('/')
  return idx >= 0 ? path.slice(idx + 1) : path
}

/** 需要安全上下文（https/localhost） */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // 需要安全上下文
    return false
  }
}
