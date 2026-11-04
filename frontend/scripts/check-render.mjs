/*
 * Renders the built dashboard in a simulated browser and asserts the two
 * features actually work: the theme switch applies its attribute, and the
 * language switch changes visible text.
 *
 * `smoke-bundle.mjs` proves the bundle boots; this proves it does something.
 * A missing translation or a broken theme selector is invisible to the build but
 * obvious to a user, so it is checked here rather than left to manual QA.
 *
 * Each case runs in a **child process**. ES modules are cached per specifier per
 * process, so booting the app twice in one process would reuse the first boot's
 * module instances — and with them the first boot's localStorage reads, Vue app
 * and router. A fresh process is the only way to simulate a fresh page load
 * without instrumenting the bundle.
 *
 * Run with `node scripts/check-render.mjs` after `npm run build`.
 */
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const SELF = fileURLToPath(import.meta.url)

/**
 * Parent mode: run each case in a child and aggregate the results.
 *
 * A case is described by the theme and locale seeded into localStorage before
 * the bundle loads, plus a reader that pulls one value out of the rendered
 * document.
 *
 * Cases sharing a `seed` are collected into one child process, because booting
 * the app is the expensive part (a full jsdom + bundle evaluation takes a
 * couple of seconds) while reading a value out of it is free. Each distinct
 * seed therefore costs exactly one boot.
 */
const CASES = [
  // --- theme: a stored explicit choice must land before anything renders -----
  {
    name: 'stored dark applies data-theme',
    seed: { theme: 'dark' },
    read: (w) => w.document.documentElement.dataset.theme,
    want: 'dark',
  },
  {
    name: 'stored dark sets color-scheme',
    seed: { theme: 'dark' },
    read: (w) => w.document.documentElement.style.colorScheme,
    want: 'dark',
  },
  {
    name: 'stored light applies data-theme',
    seed: { theme: 'light' },
    read: (w) => w.document.documentElement.dataset.theme,
    want: 'light',
  },
  {
    name: 'stored light sets color-scheme',
    seed: { theme: 'light' },
    read: (w) => w.document.documentElement.style.colorScheme,
    want: 'light',
  },
  {
    // No stored key is the follow-the-system state; the attribute must stay
    // absent so the media query owns the decision.
    name: 'no stored choice leaves data-theme unset',
    seed: {},
    read: (w) => w.document.documentElement.dataset.theme,
    want: undefined,
  },

  // --- english ---------------------------------------------------------------
  {
    name: 'en: html lang',
    seed: { locale: 'en' },
    read: (w) => w.document.documentElement.lang,
    want: 'en',
  },
  {
    name: 'en: nav renders Dashboard',
    seed: { locale: 'en' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('>Dashboard<'),
    want: true,
  },

  // --- chinese ---------------------------------------------------------------
  {
    name: 'zh: html lang',
    seed: { locale: 'zh' },
    read: (w) => w.document.documentElement.lang,
    want: 'zh',
  },
  {
    name: 'zh: nav renders 总览',
    seed: { locale: 'zh' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('总览'),
    want: true,
  },
  {
    name: 'zh: nav renders 项目',
    seed: { locale: 'zh' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('项目'),
    want: true,
  },
  {
    name: 'zh: nav renders 规则',
    seed: { locale: 'zh' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('规则'),
    want: true,
  },
  {
    name: 'zh: no English nav label leaks',
    seed: { locale: 'zh' },
    read: (w) =>
      ['>Dashboard<', '>Projects<', '>Rules<', '>System<'].some((s) =>
        w.document.getElementById('app').innerHTML.includes(s),
      ),
    want: false,
  },

  // --- shell, under both preferences at once --------------------------------
  {
    name: 'brand wordmark stays English',
    seed: { locale: 'zh', theme: 'dark' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('Fizzra'),
    want: true,
  },
  {
    name: 'theme control present',
    seed: { locale: 'zh', theme: 'dark' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('el-radio-group'),
    want: true,
  },
  {
    name: 'language select present',
    seed: { locale: 'zh', theme: 'dark' },
    read: (w) => w.document.getElementById('app').innerHTML.includes('el-select'),
    want: true,
  },
  {
    name: 'theme and locale compose without conflict',
    seed: { locale: 'zh', theme: 'dark' },
    read: (w) =>
      w.document.documentElement.dataset.theme === 'dark' &&
      w.document.documentElement.lang === 'zh' &&
      w.document.getElementById('app').innerHTML.includes('总览'),
    want: true,
  },
]

/** Child mode: boot once and print the requested values as JSON. */
async function child(seed) {
  const { readFileSync } = await import('node:fs')
  const { join, resolve } = await import('node:path')
  const { JSDOM } = await import('jsdom')

  const DIST = resolve(import.meta.dirname, '../../backend/internal/web/dist')
  const html = readFileSync(join(DIST, 'index.html'), 'utf8')

  /*
   * The inline anti-FOUC script in <head> is part of what is under test: it is
   * what puts `data-theme` and `lang` on the element before first paint. jsdom
   * only executes inline scripts under `dangerously`, but that mode also makes
   * it fetch and run the bundle itself — which would double-execute the app and
   * fight the manual import below.
   *
   * So the head script is run in isolation against a bare document, then the
   * bundle is imported once. The seeding has to happen before the script runs,
   * which is why the window is created first and the script evaluated by hand.
   */
  const seedValues = {
    'gg:theme': seed.theme,
    'gg:locale': seed.locale,
  }

  // Build the document with the head script stripped, so only our manual
  // evaluation of it takes effect.
  const withoutScript = html.replace(/<script>[\s\S]*?<\/script>/, '')

  const dom = new JSDOM(withoutScript, {
    url: 'http://127.0.0.1/',
    runScripts: 'outside-only',
    pretendToBeVisual: true,
  })
  const { window } = dom

  // Seed preferences, then run the real head script body against this window.
  for (const [key, value] of Object.entries(seedValues)) {
    if (value !== undefined) window.localStorage.setItem(key, value)
  }
  const headScript = /<script>([\s\S]*?)<\/script>/.exec(html)?.[1]
  if (headScript) {
    window.eval(headScript)
  }

  window.matchMedia ??= (q) => ({
    matches: false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })
  window.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  window.IntersectionObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  window.scrollTo ??= () => {}
  window.requestAnimationFrame ??= (cb) => setTimeout(() => cb(Date.now()), 16)
  window.cancelAnimationFrame ??= (id) => clearTimeout(id)
  window.fetch ??= async () =>
    new Response(JSON.stringify({ data: null }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })

  const GLOBALS = [
    'window', 'document', 'navigator', 'location', 'history', 'screen',
    'HTMLElement', 'Element', 'Node', 'SVGElement', 'Event', 'CustomEvent',
    'MutationObserver', 'getComputedStyle', 'requestAnimationFrame',
    'cancelAnimationFrame', 'matchMedia', 'fetch', 'Response', 'Request',
    'Headers', 'ResizeObserver', 'IntersectionObserver', 'localStorage',
  ]
  for (const key of GLOBALS) {
    if (!(key in window)) continue
    try {
      Object.defineProperty(globalThis, key, {
        value: window[key],
        writable: true,
        configurable: true,
      })
    } catch {
      /* getter-only global; the bundle works without it */
    }
  }

  const entry = /assets\/(index-[A-Za-z0-9_-]+\.js)/.exec(html)?.[1]
  if (!entry) throw new Error('no entry chunk in index.html')
  await import('file://' + join(DIST, 'assets', entry))
  await new Promise((r) => setTimeout(r, 300))

  const out = {}
  for (const [i, c] of CASES.entries()) {
    if (JSON.stringify(c.seed) !== JSON.stringify(seed)) continue
    out[i] = c.read(window)
  }
  console.log('__RESULT__' + JSON.stringify(out))
}

// ---------------------------------------------------------------------------

const seedArg = process.argv[2]
if (seedArg) {
  await child(JSON.parse(seedArg))
  process.exit(0)
}

// Aggregate: one child per distinct seed.
const seeds = [...new Set(CASES.map((c) => JSON.stringify(c.seed)))].map((s) => JSON.parse(s))
const results = {}

for (const seed of seeds) {
  const proc = spawnSync(process.execPath, [SELF, JSON.stringify(seed)], { encoding: 'utf8' })
  const line = (proc.stdout || '').split('\n').find((l) => l.startsWith('__RESULT__'))
  if (!line) {
    console.error(`FAIL  child boot failed for seed ${JSON.stringify(seed)}`)
    console.error((proc.stderr || proc.stdout || '').split('\n').slice(-6).join('\n'))
    process.exit(1)
  }
  Object.assign(results, JSON.parse(line.slice('__RESULT__'.length)))
}

let failures = 0
for (const [i, c] of CASES.entries()) {
  const got = results[i]
  const ok = got === c.want
  if (!ok) failures++
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${c.name}`)
  if (!ok) console.log(`        got:  ${got}\n        want: ${c.want}`)
}

console.log(`\n${failures === 0 ? 'ALL CHECKS PASSED' : `${failures} FAILURE(S)`}`)
process.exit(failures === 0 ? 0 : 1)

