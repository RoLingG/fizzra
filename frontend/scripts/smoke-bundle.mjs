/*
 * Loads the built dashboard bundle in a simulated browser and reports whether it
 * mounted.
 *
 * This exists because a chunking mistake produces a module-initialisation error
 * that is invisible to static inspection: Rollup will happily emit a circular
 * chunk graph, and the failure only appears when the browser evaluates the
 * chunks in dependency order. Grepping the bundle cannot tell you the app boots;
 * running it can.
 *
 * Run with `node scripts/smoke-bundle.mjs` after `npm run build`.
 */
import { readFileSync, readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { JSDOM } from 'jsdom'

const DIST = resolve(import.meta.dirname, '../../backend/internal/web/dist')

const html = readFileSync(join(DIST, 'index.html'), 'utf8')
const dom = new JSDOM(html, {
  url: 'http://127.0.0.1/',
  runScripts: 'outside-only',
  pretendToBeVisual: true,
})

const { window } = dom

// jsdom lacks these; ECharts and Element Plus touch them on load.
window.matchMedia ??= (query) => ({
  matches: false,
  media: query,
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

// The app calls /api/v1/system/info on mount. There is no server here, and
// letting the real fetch run produces an unhandled rejection that masks the
// thing under test. A stub that returns an empty envelope is enough: the point
// is whether the module graph evaluates and Vue mounts, not whether the API
// answers.
window.fetch ??= async () =>
  new Response(JSON.stringify({ data: null }), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })

const errors = []

// Mirror the module graph by loading the entry and letting it pull its
// dependencies. jsdom does not implement ESM, so the chunks are evaluated by
// Node with the window globals injected — which is what actually exercises the
// import order that breaks.
const assets = readdirSync(join(DIST, 'assets')).filter((f) => f.endsWith('.js'))
const entryFile = /assets\/(index-[A-Za-z0-9_-]+\.js)/.exec(html)?.[1]

if (!entryFile) {
  console.error('FAIL: no entry chunk found in index.html')
  process.exit(1)
}

const GLOBALS = [
  'window',
  'document',
  'navigator',
  'location',
  // vue-router reads these for its history mode; jsdom exposes them on the
  // window but Node has no global of its own.
  //
  // `performance` is deliberately NOT taken from jsdom: its accessor recurses
  // when installed as a Node global, overflowing the stack. Node's own
  // performance object is API-compatible and already present.
  'history',
  'screen',
  'HTMLElement',
  'Element',
  'Node',
  'SVGElement',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'matchMedia',
  'fetch',
  'Response',
  'Request',
  'Headers',
  'ResizeObserver',
  'IntersectionObserver',
  'localStorage',
]

for (const key of GLOBALS) {
  if (!(key in window)) continue
  // Some of these (navigator) are getter-only on modern Node, so a plain
  // assignment throws. Redefining is fine here — this is a throwaway sandbox.
  try {
    Object.defineProperty(globalThis, key, {
      value: window[key],
      writable: true,
      configurable: true,
    })
  } catch {
    // A truly non-configurable global is not worth failing the smoke test over;
    // the bundle will surface a real problem as its own error if it needs it.
  }
}

// Import the entry chunk. Its own `import` statements resolve the rest of the
// graph, so a circular chunk dependency surfaces here as a ReferenceError or a
// "not a function" at evaluation time.
try {
  await import('file://' + join(DIST, 'assets', entryFile))
  // Give Vue's mount a tick.
  await new Promise((r) => setTimeout(r, 200))
} catch (error) {
  errors.push(error)
}

const app = window.document.getElementById('app')
const mounted = Boolean(app && app.innerHTML.trim().length > 0)

console.log(`entry chunk:  ${entryFile}`)
console.log(`chunks:       ${assets.length}`)
console.log(`#app mounted: ${mounted}`)
if (mounted) console.log(`#app content: ${app.innerHTML.slice(0, 120).replace(/\s+/g, ' ')}`)

if (errors.length) {
  console.error('\nFAIL: the bundle threw during evaluation')
  for (const e of errors) console.error('  ' + (e.stack ?? e.message).split('\n').slice(0, 4).join('\n  '))
  process.exit(1)
}

if (!mounted) {
  console.error('\nFAIL: the bundle evaluated but never mounted anything into #app')
  process.exit(1)
}

console.log('\nOK: bundle evaluates and mounts')
