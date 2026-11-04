/*
 * Audits Element Plus for dark-mode coverage.
 *
 * Element Plus drives nearly every component from its own `--el-*` variables,
 * and the defaults are hardcoded light values (`--el-bg-color: #fff`,
 * `--el-text-color-primary: #303133`, `--el-fill-color: #f0f2f5`). A component
 * whose colours come from an un-overridden variable keeps its light styling on
 * the dark theme — which is how the pagination's page buttons ended up as light
 * grey squares on a dark page.
 *
 * Two things make this easy to get wrong, and both are handled here:
 *
 *  1. Many variables are declared on the COMPONENT selector (`.el-pagination`),
 *     not in `:root`. A scan that only reads `:root` reports a false all-clear.
 *  2. Variables that reference another `--el-*` are indirection, not leaves —
 *     the question is whether the value they resolve to is themed.
 *
 * The check is a translation-layer test: it resolves each variable down to its
 * final value and requires that value to be either one of our tokens or a
 * colour we set ourselves.
 *
 * Run with `node scripts/check-theme-coverage.mjs`.
 */
import { readFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

const FRONTEND = resolve(import.meta.dirname, '..')
const epCss = readFileSync(join(FRONTEND, 'node_modules/element-plus/dist/index.css'), 'utf8')
const ourCss =
  readFileSync(join(FRONTEND, 'src/styles/main.css'), 'utf8') +
  readFileSync(join(FRONTEND, 'src/styles/tokens.css'), 'utf8')

/** Variables that affect theming. Sizes, radii and fonts are omitted. */
const COLOR_HINT = /(bg|background|fill|text|color|border-color)/
/** A literal colour, as opposed to `var(...)` indirection. */
const LITERAL_COLOR = /#|rgb|hsl|\bwhite\b|\bblack\b/
/** A reference to one of our own tokens rather than an Element Plus variable. */
const OUR_TOKEN = /var\(--(?!el-)/

/**
 * Collects `--el-x: value` declarations from the whole stylesheet, not just
 * `:root`, keyed by the selector they were declared on.
 */
function collectElVars(css) {
  const declarations = new Map()
  for (const block of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const [, selector, body] = block
    for (const m of body.matchAll(/(--el-[a-z0-9-]+)\s*:\s*([^;]+);/g)) {
      const name = m[1]
      const value = m[2].trim()
      if (!declarations.has(name)) declarations.set(name, [])
      declarations.get(name).push({ selector: selector.trim(), value })
    }
  }
  return declarations
}

const epVars = collectElVars(epCss)
const ourVars = collectElVars(ourCss)

/**
 * Resolves a value through `var(--el-*)` chains to whatever the project would
 * actually paint.
 *
 * Returns 'ours' when the chain terminates in one of our tokens, 'literal' when
 * it terminates in a hardcoded colour (the failure case), and 'unresolved' when
 * the chain cannot be followed.
 */
function resolveOrigin(name, seen = new Set()) {
  if (seen.has(name)) return 'unresolved'
  seen.add(name)

  // Our own declaration wins: it is what the browser applies.
  const ours = ourVars.get(name)
  if (ours) {
    const last = ours[ours.length - 1].value
    if (OUR_TOKEN.test(last)) return 'ours'
    if (LITERAL_COLOR.test(last)) return 'ours' // we chose it deliberately
    const inner = /var\((--el-[a-z0-9-]+)\)/.exec(last)
    if (inner) return resolveOrigin(inner[1], seen)
    return 'ours'
  }

  const theirs = epVars.get(name)
  if (!theirs) return 'unresolved'
  const last = theirs[theirs.length - 1].value
  if (OUR_TOKEN.test(last)) return 'ours'
  const inner = /var\((--el-[a-z0-9-]+)\)/.exec(last)
  if (inner) return resolveOrigin(inner[1], seen)
  if (LITERAL_COLOR.test(last)) return 'literal'
  return 'unresolved'
}

/*
 * The variables the components in use actually read. Taken from the component
 * selectors in Element Plus's own stylesheet, so the audit follows the library
 * rather than a hand-maintained list that drifts.
 */
const COMPONENTS = {
  pagination: '.el-pagination',
  alert: '.el-alert',
  switch: '.el-switch',
  checkbox: '.el-checkbox',
  'radio-button': '.el-radio-button',
  'input-number': '.el-input-number',
  empty: '.el-empty',
  result: '.el-result',
  badge: '.el-badge',
  'color-picker': '.el-color-picker',
  popper: '.el-popper',
  'select-dropdown': '.el-select-dropdown',
  'form-item': '.el-form-item',
  tabs: '.el-tabs',
  dialog: '.el-dialog',
  drawer: '.el-drawer',
}

console.log('Element Plus dark-mode coverage\n')

/*
 * The base translation layer.
 *
 * Element Plus components mostly read these rather than declaring their own
 * colours, so if one of them falls back to the library default, every component
 * built on it is unthemed at once. That is exactly how the pagination buttons
 * broke: they read `--el-fill-color`, which was never mapped, so they stayed
 * light grey on the dark theme.
 *
 * Checked as a set rather than per-component because the failure is global, and
 * a per-component audit would report it dozens of times without naming the cause.
 */
const BASE_VARS = [
  '--el-bg-color',
  '--el-bg-color-page',
  '--el-bg-color-overlay',
  '--el-fill-color',
  '--el-fill-color-light',
  '--el-fill-color-lighter',
  '--el-fill-color-blank',
  '--el-fill-color-dark',
  '--el-text-color-primary',
  '--el-text-color-regular',
  '--el-text-color-secondary',
  '--el-text-color-placeholder',
  '--el-border-color',
  '--el-border-color-light',
  '--el-border-color-lighter',
  '--el-color-white',
  '--el-disabled-bg-color',
  '--el-color-primary',
]

const baseGaps = BASE_VARS.filter((v) => resolveOrigin(v) !== 'ours')
if (baseGaps.length === 0) {
  console.log(`ok    base translation layer (${BASE_VARS.length} variables)`)
} else {
  console.log('GAP   base translation layer')
  for (const v of baseGaps) {
    const decls = epVars.get(v) ?? []
    const fallback = decls.length ? decls[decls.length - 1].value : '(not declared by Element Plus)'
    console.log(`        ${v} resolves to ${fallback}, not one of our tokens`)
  }
}

const gaps = [...baseGaps]
for (const [component, selector] of Object.entries(COMPONENTS)) {
  // Variables declared on this component's own selector.
  const owned = [...epVars.entries()]
    .filter(([, decls]) => decls.some((d) => d.selector.startsWith(selector)))
    .map(([name]) => name)
    .filter((name) => COLOR_HINT.test(name))

  const unthemed = owned.filter((name) => resolveOrigin(name) === 'literal')

  if (unthemed.length === 0) {
    console.log(`ok    ${component} (${owned.length} colour vars)`)
    continue
  }

  console.log(`GAP   ${component}`)
  for (const name of unthemed) {
    const decls = epVars.get(name)
    console.log(`        ${name} = ${decls[decls.length - 1].value}`)
  }
  gaps.push(...unthemed)
}

console.log(
  gaps.length === 0
    ? '\nAll audited components resolve to themed colours.'
    : `\n${gaps.length} unthemed colour variable(s) across ${new Set(gaps).size} variable(s).`,
)

process.exit(gaps.length === 0 ? 0 : 1)
