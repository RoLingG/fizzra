import { i18n } from './index'

export interface TranslatableAnalyzerText {
  name?: string
  description?: string
  options?: Array<{ key?: string; description?: string }> | null
}

function lookup(name: string): string | undefined {
  const key = `analyzer.${name.toLowerCase()}.description`
  return i18n.global.te(key) ? i18n.global.t(key) : undefined
}

function lookupSummary(name: string): string | undefined {
  const key = `analyzer.${name.toLowerCase()}.summary`
  return i18n.global.te(key) ? i18n.global.t(key) : undefined
}

// 没有条目时返回 undefined，调用方据此省略
export function localizeAnalyzerSummary(name: string): string | undefined {
  return name ? lookupSummary(name) : undefined
}

function lookupOption(analyzerName: string, optionKey: string): string | undefined {
  if (!optionKey) return undefined
  const key = `analyzer.${analyzerName.toLowerCase()}.option.${optionKey}.description`
  return i18n.global.te(key) ? i18n.global.t(key) : undefined
}

export function localizeAnalyzerDescription<T>(analyzer: T): T {
  const name = (analyzer as TranslatableAnalyzerText).name
  if (!name) return analyzer

  const localized = lookup(name)
  if (!localized) return analyzer

  return { ...(analyzer as object), description: localized } as T
}

export function localizeAnalyzerOptions<T>(analyzer: T): T {
  const source = analyzer as TranslatableAnalyzerText
  if (!source.name || !source.options?.length) return analyzer

  const options = source.options.map((option) => {
    const localized = lookupOption(source.name as string, option.key ?? '')
    return localized ? { ...option, description: localized } : option
  })

  return { ...(analyzer as object), options } as T
}
