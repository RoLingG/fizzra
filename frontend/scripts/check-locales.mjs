/*
 * A check of the message catalogues, run with `npm run check:locales`.
 *
 * Verifies the things `vue-tsc` cannot: that plural forms resolve, that named
 * interpolation produces the expected sentence in both languages, that every
 * built-in rule slug and analyzer has an entry, and that the two catalogues
 * cover exactly the same keys.
 *
 * `vue-tsc` already catches a missing key, but only in one direction and only as
 * a type error; this asserts the rendered output, which is where a malformed
 * plural or a mismatched placeholder actually shows up.
 *
 * The TypeScript sources are loaded through Vite's SSR transform rather than a
 * node loader, so the check adds no dependency beyond the toolchain already
 * present.
 */
import { createServer } from 'vite'
import { createI18n } from 'vue-i18n'

const server = await createServer({
  server: { middlewareMode: true },
  appType: 'custom',
  logLevel: 'error',
})

let en, zh
try {
  ;({ default: en } = await server.ssrLoadModule('/src/locales/en.ts'))
  ;({ default: zh } = await server.ssrLoadModule('/src/locales/zh.ts'))
} finally {
  await server.close()
}

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: { en, zh },
})

const { t, te } = i18n.global

let failures = 0
function check(label, actual, expected) {
  const ok = actual === expected
  if (!ok) failures++
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${label}`)
  if (!ok) {
    console.log(`        got:  ${actual}`)
    console.log(`        want: ${expected}`)
  }
}

console.log('--- plural resolution (en) ---')
check('1 analyzer', t('app.analyzersUnavailable', 1), '1 analyzer unavailable')
check('3 analyzers', t('app.analyzersUnavailable', 3), '3 analyzers unavailable')
check('0 analyzers', t('app.analyzersUnavailable', 0), 'no analyzers unavailable')
check('1 issue', t('dashboard.issuesCount', 1), '1 issue')
check('5 issues', t('dashboard.issuesCount', 5), '5 issues')

console.log('\n--- named interpolation (en) ---')
check(
  'heroSummary',
  t('dashboard.heroSummary', { count: 4, passing: 3 }),
  'Mean across 4 analysed projects. 3 of 4 passing the quality gate.',
)
check(
  'security highDetail',
  t('dashboard.security.highDetail', { count: 2, errors: 5 }),
  '2 critical and 5 high-severity findings need attention before the next release.',
)
check(
  'deleteBody',
  t('projectsView.deleteBody', { name: 'my-app' }),
  'This deletes "my-app" along with its analysis history and issues. The repository itself is not touched.',
)

console.log('\n--- zh rendering ---')
i18n.global.locale.value = 'zh'
check('nav.dashboard', t('nav.dashboard'), '总览')
check('severity.critical', t('severity.critical'), '严重')
check('rule no-panic message', t('rule.no-panic.message'), '在初始化之外调用了 panic()')
check('rule command-injection advice', t('rule.command-injection.advice'), '应传递独立的参数，绝不要把输入交给 sh -c。')
check(
  'heroSummary zh',
  t('dashboard.heroSummary', { count: 4, passing: 3 }),
  '4 个已分析项目的平均分。3 / 4 通过质量门禁。',
)
check(
  'deleteBody zh',
  t('projectsView.deleteBody', { name: 'my-app' }),
  '将删除「my-app」及其分析历史与问题记录。仓库本身不受影响。',
)
check('gate.passed', t('gate.passed'), '通过')
check('status.falsePositive', t('status.falsePositive'), '误报')

console.log('\n--- fallback behaviour ---')
// An unknown wire value must render as itself, never as a key path. The UI
// guards these with `te()`, so assert the keys that guard depends on exist and
// that an invented one does not.
check('status.falsePositive exists', te('status.falsePositive'), true)
check('unknown status does not', te('status.banana'), false)
check('unknown rule does not', te('rule.no-such-rule.message'), false)
i18n.global.locale.value = 'en'

console.log('\n--- catalogue completeness ---')
// Every built-in rule slug from backend/internal/rule/builtin.go must have an
// entry, or it silently renders English in the Chinese UI.
const SLUGS = [
  'no-panic', 'defer-in-loop', 'context-background-in-request-path',
  'goroutine-captures-loopvar', 'mutex-by-value', 'sleep-in-loop', 'discarded-error',
  'error-shadowed-by-nil', 'command-injection', 'weak-random', 'broken-hash',
  'insecure-tls', 'hardcoded-credential', 'sql-injection', 'unsafe-file-permissions',
  'unsafe-pointer', 'wails-events-emit-in-loop', 'wails-dialog-unchecked',
  'wails-browser-open-url', 'wails-frontend-hardcoded-path',
  'wails-wailsjson-legacy-fields', 'unresolved-marker', 'commented-out-code',
  'debug-output-left',
]
const missing = SLUGS.filter((s) => !te(`rule.${s}.message`))
failures += missing.length ? 1 : 0
console.log(`${missing.length === 0 ? 'ok  ' : 'FAIL'}  ${SLUGS.length} rule slugs covered`)
if (missing.length) console.log(`        missing: ${missing.join(', ')}`)

// Every analyzer the backend registers, so a new one cannot ship without a
// translated description.
//
// This list used to hold nine names and omitted oxlint, ruff, clippy, rustfmt,
// cargo-audit, tsc and biome — which is how biome and tsc shipped with no entry
// at all while the check still printed "ok". The count it prints is only
// meaningful if the list is complete, so keep it in step with
// backend/internal/analyzer/registry.go.
const ANALYZERS = [
  'custom', 'metrics', 'wails',
  'gofmt', 'go-vet', 'golangci', 'staticcheck', 'gosec', 'govulncheck',
  'ruff', 'oxlint', 'tsc', 'biome', 'clippy', 'rustfmt', 'cargo-audit',
]
const missingAnalyzers = ANALYZERS.filter((a) => !te(`analyzer.${a}.description`))
failures += missingAnalyzers.length ? 1 : 0
console.log(`${missingAnalyzers.length === 0 ? 'ok  ' : 'FAIL'}  ${ANALYZERS.length} analyzer descriptions covered`)
if (missingAnalyzers.length) console.log(`        missing: ${missingAnalyzers.join(', ')}`)

console.log('\n--- en/zh key parity ---')
function flatten(obj, prefix = '') {
  return Object.entries(obj).flatMap(([k, v]) =>
    typeof v === 'object' && v !== null ? flatten(v, `${prefix}${k}.`) : [`${prefix}${k}`],
  )
}
const enKeys = new Set(flatten(en))
const zhKeys = new Set(flatten(zh))
const onlyEn = [...enKeys].filter((k) => !zhKeys.has(k))
const onlyZh = [...zhKeys].filter((k) => !enKeys.has(k))
failures += onlyEn.length || onlyZh.length ? 1 : 0
console.log(
  `${onlyEn.length === 0 && onlyZh.length === 0 ? 'ok  ' : 'FAIL'}  ${enKeys.size} en keys / ${zhKeys.size} zh keys`,
)
if (onlyEn.length) console.log(`        only in en: ${onlyEn.slice(0, 10).join(', ')}`)
if (onlyZh.length) console.log(`        only in zh: ${onlyZh.slice(0, 10).join(', ')}`)

console.log(`\n${failures === 0 ? 'ALL CHECKS PASSED' : `${failures} FAILURE(S)`}`)
process.exit(failures === 0 ? 0 : 1)

