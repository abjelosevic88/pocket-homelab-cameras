// README screenshots and a browser smoke test against a demo instance:
//
//   make build && DATA_DIR=/tmp/pcam-demo DEMO_DATA=true AUTH_MODE=none HOST=127.0.0.1 PORT=18280 ./pocket-cameras &
//   CDP=http://127.0.0.1:9344 BASE=http://127.0.0.1:18280 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18280'
const shots = process.env.SHOTS !== '0'
const only = process.env.ONLY || ''
const dir = process.env.OUT || new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false, w = 1440, h = 900 } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: w, height: h },
    deviceScaleFactor: 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource|ERR_/.test(m.text())) errors.push(m.text()) })
  return { ctx, page }
}
async function go(page, path, wait = 1600) {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(wait)
}
async function shot(page, name) {
  if (!shots || (only && !only.split(',').includes(name))) return
  for (let i = 0; ; i++) {
    try { await page.bringToFront(); await page.screenshot({ path: dir + name + '.png', animations: 'disabled' }); return } catch (e) { if (i >= 3) throw e; await page.waitForTimeout(700) }
  }
}
const api = (page, path, method = 'GET', body) => page.evaluate(([p, m, b]) => fetch('/api/v1' + p, { method: m, headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined }).then(r => r.status === 204 ? {} : r.json()), [path, method, body])
const has = async (page, text, what) => assert.ok((await page.content()).includes(text), what)

const { page } = await session()
await go(page, '/', 3500)
await has(page, 'Front door', 'live shows the cameras')
await has(page, 'Last seen', 'live shows the last events')
await shot(page, 'desktop-live')

await go(page, '/events', 2500)
await has(page, 'Yesterday', 'events grouped by day')
await shot(page, 'desktop-events')

const evs = (await api(page, '/events?label=parcel&limit=5')).events
assert.ok(evs.length > 0, 'a parcel was delivered')
await go(page, `/events/${evs[0].id}`, 2200)
await has(page, 'Probably your parcel', 'the event says which parcel')
await shot(page, 'desktop-event')

const cams = (await api(page, '/cameras')).cameras
const yesterday = new Date(Date.now() - 86400000).toISOString().slice(0, 10)
await go(page, '/timeline', 2000)
await page.click('button[aria-label="The day before"]')
await page.waitForTimeout(1500)
await shot(page, 'desktop-timeline')

await go(page, `/cameras/${cams[2].id}?tab=areas`, 2500)
await has(page, 'Lawn', 'the garden watches the lawn')
await shot(page, 'desktop-areas')

await go(page, `/cameras/${cams[0].id}?tab=settings`, 2000)
await has(page, 'When it tells you', 'camera settings')

await go(page, '/stats', 2000)
await has(page, 'Busy hours', 'the numbers')
await shot(page, 'desktop-stats')

await go(page, '/settings/apps')
await has(page, 'Pocket Network', 'the connected apps')

// the flow: the doorbell rings, an event with a picture
const ring = await api(page, `/cameras/${cams[0].id}/ring`, 'POST', {})
assert.equal(ring.kind, 'doorbell', 'a ring makes a doorbell event')
const m = await api(page, '/mode', 'PUT', { mode: 'away' })
assert.equal(m.mode, 'away', 'the mode changes')
await api(page, '/mode', 'PUT', { mode: 'home' })

const dark = await session({ dark: true })
await go(dark.page, '/', 3500)
await shot(dark.page, 'desktop-live-dark')
await dark.ctx.close()

const phone = await session({ phone: true })
await go(phone.page, '/', 3500)
await shot(phone.page, 'phone-live')
await go(phone.page, '/events', 2500)
await shot(phone.page, 'phone-events')
await go(phone.page, `/events/${evs[0].id}`, 2200)
await shot(phone.page, 'phone-event')
await go(phone.page, `/cameras/${cams[1].id}`, 3000)
await shot(phone.page, 'phone-camera')
await phone.ctx.close()

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('ok: browser smoke passed' + (shots ? ', screenshots in ' + dir : ''))
