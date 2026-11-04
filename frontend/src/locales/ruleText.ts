import { i18n } from './index'

// 这里只翻展示层，Message 参与指纹计算不能改
// 没有条目时回退服务端原文

export interface TranslatableIssueText {
  rule?: string
  message?: string
  advice?: string
  fix?: { before?: string; after?: string; note?: string; language?: string }
}

// 外部工具分析器透传的是原样输出，只翻规则引擎和 Go/Wails 检查自撰的消息
function hasAuthoredMessage(analyzer: string | undefined): boolean {
  const a = String(analyzer ?? '').toLowerCase()
  return a === 'custom' || a === 'wails' || a === 'metrics'
}

function lookup(rule: string, field: 'message' | 'advice' | 'fixNote'): string | undefined {
  const key = `rule.${rule}.${field}`
  // 先用 te 检查，否则会渲染出键路径
  return i18n.global.te(key) ? i18n.global.t(key) : undefined
}

// 没译文的字段保留服务端值。fix 的 before/after 绝不能翻译，翻译过的 Go 注释粘不回编辑器
export function localizeIssueText<T>(issue: T, analyzer?: string): T & TranslatableIssueText {
  const rule = (issue as TranslatableIssueText).rule
  if (!rule) return issue as T & TranslatableIssueText

  const source = issue as TranslatableIssueText
  const out = { ...source } as TranslatableIssueText

  if (source.fix) {
    const note = lookup(rule, 'fixNote')
    if (note) out.fix = { ...source.fix, note }
  }

  const advice = lookup(rule, 'advice')
  if (advice) out.advice = advice

  if (hasAuthoredMessage(analyzer)) {
    const message = lookup(rule, 'message')
    if (message) out.message = message
  }

  return out as T & TranslatableIssueText
}

export interface TranslatableRuleText {
  name?: string
  message?: string
  advice?: string
  fix?: { before?: string; after?: string; note?: string; language?: string }
}

export function localizeRuleText<T>(rule: T): T & TranslatableRuleText {
  const name = (rule as TranslatableRuleText).name
  if (!name) return rule as T & TranslatableRuleText

  const source = rule as TranslatableRuleText
  const out = { ...source } as TranslatableRuleText

  const message = lookup(name, 'message')
  if (message) out.message = message

  const advice = lookup(name, 'advice')
  if (advice) out.advice = advice

  if (source.fix) {
    const note = lookup(name, 'fixNote')
    if (note) out.fix = { ...source.fix, note }
  }

  return out as T & TranslatableRuleText
}
