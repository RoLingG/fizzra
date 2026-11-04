/*
 * Verifies the *deployed* dashboard: the one the running binary serves.
 *
 * The other two checks work on the build output on disk:
 *
 *   smoke-bundle.mjs  — the chunks evaluate and the app mounts
 *   check-render.mjs  — theme and language actually take effect
 *
 * This one covers what neither can: that the binary embedded a COMPLETE dist.
 * A stale or partial embed still returns 200 for index.html, and the app on
 * disk can be perfectly healthy while the copy inside the executable is missing
 * a lazily-loaded route — the failure would only appear when a user clicked a
 * nav tab in the real UI. So this enumerates every asset the browser will ever
 * request and confirms the server can answer for each.
 *
 * Usage: node scripts/check-served.mjs [baseURL]
 *        node scripts/check-served.mjs http://127.0.0.1:9090
 */
import { readdirSync } from 'node:fs'
import { join, resolve } from 'node:path'

const BASE = process.argv[2] ?? 'http://127.0.0.1:8080'
const DIST = resolve(import.meta.dirname, '../../backend/internal/web/dist')

let failures = 0
function check(name, ok, detail) {
  if (!ok) failures++
  console.log(`${ok ? 'ok  ' : 'FAIL'}  ${name}`)
  if (!ok && detail) console.log(`        ${detail}`)
}

/** Paths that are expected to 404 rather than be embedded. */
const served = async (path) => {
  const res = await fetch(BASE + path)
  return res.ok
}

// ---------------------------------------------------------------------------
// The server is up and serving a dashboard
// ---------------------------------------------------------------------------

let html
try {
  const res = await fetch(BASE + '/')
  check('index.html is served', res.ok, `status ${res.status}`)
  html = await res.text()
} catch (error) {
  console.error(`FAIL  cannot reach ${BASE} — is \`fizzra serve\` running?`)
  console.error(`      ${error.message}`)
  process.exit(1)
}

check('index.html is the dashboard, not the build-me page', html.includes('<div id="app">'))
check(
  'anti-FOUC script is inline in <head>',
  /<head>[\s\S]*?<script>[\s\S]*?gg:theme/.test(html),
  'the inline theme/locale script is missing from the served head',
)

const entryFile = /assets\/(index-[A-Za-z0-9_-]+\.js)/.exec(html)?.[1]
if (!entryFile) {
  console.error('FAIL  no entry chunk referenced by index.html')
  process.exit(1)
}

// ---------------------------------------------------------------------------
// Every asset the browser will request is answerable
// ---------------------------------------------------------------------------

const referenced = [...new Set([...html.matchAll(/\/assets\/[A-Za-z0-9_.-]+/g)].map((m) => m[0]))]
const missing = []
for (const path of referenced) {
  if (!(await served(path))) missing.push(path)
}
check(`all ${referenced.length} entry assets are reachable`, missing.length === 0, missing.join(', '))

/*
 * Route views are `import()`ed on navigation, so they appear in neither
 * index.html nor the static import graph. Vite records them in `__vite__mapDeps`
 * — including the per-route CSS — and each chunk that uses dynamic imports
 * carries its OWN array, so they must be collected from every served chunk.
 *
 * The scan is a worklist, not a single pass: a route chunk's mapDeps names
 * further chunks, and those may declare their own. ProjectDetailView pulls in
 * TrendChart and the ECharts `charts` chunk this way — one level deeper than the
 * entry, and invisible to a scan that only looked at entry-referenced files.
 */
const lazyAssets = []
const scanned = new Set()
const scanQueue = [...referenced.filter((p) => p.endsWith('.js')), '/assets/' + entryFile]

while (scanQueue.length) {
  const path = scanQueue.shift()
  if (scanned.has(path)) continue
  scanned.add(path)

  const res = await fetch(BASE + path)
  if (!res.ok) continue
  const body = await res.text()

  for (const m of body.matchAll(/__vite__mapDeps=\([^)]*=\[([^\]]*)\]/g)) {
    for (const item of m[1].split(',')) {
      const clean = item.replace(/^"|"$/g, '').trim()
      if (!clean) continue
      const asset = '/' + clean
      if (!lazyAssets.includes(asset)) lazyAssets.push(asset)
      if (asset.endsWith('.js')) scanQueue.push(asset)
    }
  }
}

for (const path of lazyAssets) {
  if (!(await served(path))) missing.push(`${path} (lazy)`)
}
check(`all ${lazyAssets.length} lazy route assets are reachable`, missing.length === 0, missing.join(', '))

/*
 * Nothing should be emitted but unreachable. This is the assertion that catches
 * a partial embed: the assets exist on disk, index.html loads, the entry chunk
 * runs — and one route 404s only when it is navigated to.
 */
const diskChunks = readdirSync(join(DIST, 'assets')).filter((f) => f.endsWith('.js'))
const reachable = new Set([...referenced, ...lazyAssets])
const unaccounted = diskChunks.filter((f) => !reachable.has('/assets/' + f))
check(
  `all ${diskChunks.length} emitted chunks are reachable`,
  unaccounted.length === 0,
  `unreachable: ${unaccounted.join(', ')}`,
)

// ---------------------------------------------------------------------------
// The served bytes match the build output
// ---------------------------------------------------------------------------

/*
 * A binary built before the last frontend build embeds the old bundle. It serves
 * 200 for everything and looks fine, which is precisely the trap `go:embed`
 * sets: the snapshot is taken at compile time. Comparing content hashes against
 * the current dist detects it.
 */
const { createHash } = await import('node:crypto')
const hash = (s) => createHash('sha256').update(s).digest('hex').slice(0, 16)

const servedEntry = await (await fetch(BASE + '/assets/' + entryFile)).text()
const { readFileSync } = await import('node:fs')
const diskEntry = readFileSync(join(DIST, 'assets', entryFile), 'utf8')
check(
  'the served entry chunk matches the current build output',
  hash(servedEntry) === hash(diskEntry),
  'the binary was built before the last `npm run build` — rebuild with `go build`',
)

console.log(`\n${failures === 0 ? 'SERVED APP OK' : `${failures} FAILURE(S)`}`)
process.exit(failures === 0 ? 0 : 1)
