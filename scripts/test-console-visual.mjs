// Read-only visual QA. Candidate bundles can be injected before replacing local binaries.
import assert from 'node:assert/strict'
import { readFile, mkdir } from 'node:fs/promises'
import path from 'node:path'
const base = process.env.CONSOLE_UI_TEST_URL || 'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname, '127.0.0.1')
const dist = process.env.CONSOLE_UI_DIST
const output = process.env.CONSOLE_UI_SCREENSHOTS || '/tmp/devops-console-ui'
await mkdir(output, { recursive: true })
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH })
const page = await browser.newPage({ viewport: { width: 1440, height: 960 } })
const errors = [], writes = []
page.on('pageerror', e => errors.push(e.message))
await page.route('**/*', async route => {
  const req = route.request(), url = new URL(req.url())
  if (url.origin !== new URL(base).origin) return route.abort()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(req.method())) {
    writes.push(`${req.method()} ${url.pathname}`)
    return route.abort()
  }
  if (dist) {
    let file
    if (url.pathname === '/') file = 'shell.html'
    else if (/\/assets\/[^/]+\.(?:js|css)$/.test(url.pathname)) file = `assets/${path.basename(url.pathname)}`
    else if (/^\/api\/plugins\/(deploy|logs|monitor|dbms|redis)\/ui(?:\/(?:dbms|redis))?\/?$/.test(url.pathname)) {
      const plugin = url.pathname.split('/')[3]
      file = (['dbms', 'redis'].includes(plugin) ? 'workbench' : plugin) + '.html'
    }
    if (file) return route.fulfill({ body: await readFile(path.join(dist, file)), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' })
  }
  return route.continue()
})
const overflow = async surface => {
  assert.equal(await surface.locator('body').evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'Horizontal page overflow')
}
// Capture after resize / dialog enter transitions; visibility alone can be true at opacity 0.
const capture = async name => { await page.mouse.move(1000, 24); await page.waitForTimeout(250); await page.screenshot({ path: `${output}/${name}.png` }) }
try {
  for (const theme of ['light', 'dark']) {
    await page.addInitScript(value => localStorage.setItem('lp-theme', value), theme)
    await page.goto(base)
    const deploy = page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]')
    await deploy.getByRole('heading', { name: '基础构建', exact: true }).waitFor()
    await page.locator('.contextbar').waitFor({state:'hidden'})
    const paths=await deploy.locator('.workspace-sidebar nav a svg, .platform-nav button svg').evaluateAll(icons=>icons.map(icon=>Array.from(icon.querySelectorAll('path'),p=>p.getAttribute('d')).join('|')))
    assert.equal(new Set(paths).size,paths.length,'Navigation reuses an icon for unrelated operations')
    const rocket=await page.locator('.wstab svg').first().locator('path').first().getAttribute('d')
    assert(rocket.startsWith('M14 4c3-2'),'Deployment tab still uses the percent icon')
    for (const width of [1440, 900, 700, 390]) {
      await page.setViewportSize({ width, height: 960 })
      await overflow(page); await overflow(deploy)
      assert.equal(await deploy.locator('.layout > header').evaluate(e => e.getBoundingClientRect().width), 64)
      await capture(`build-${theme}-${width}`)
    }
    await page.setViewportSize({ width: 1440, height: 960 })
    // Direct template entry and cancel are UI-only; do not create an application.
    const template = deploy.locator('.onboarding-templates button').first()
    if (await template.count()) {
      await template.click()
      const dialog = deploy.getByRole('dialog', { name: '接入应用', exact: true })
      await dialog.waitFor()
      await capture(`build-form-${theme}`)
      await dialog.locator('.el-dialog__headerbtn').click()
      await dialog.waitFor({ state: 'hidden' })
    }
    for (const [link, heading] of [['编排', '部署编排'], ['变更', '变更管控']]) {
      await deploy.getByRole('link', { name: link, exact: true }).click()
      await deploy.getByRole('heading', { name: heading, exact: true }).waitFor()
      await page.locator('.contextbar').waitFor({state:'hidden'})
      await capture(`${link}-${theme}`)
    }
    await deploy.locator('.platform-nav button').filter({ hasText: '资源' }).click()
    const panel = page.getByRole('dialog', { name: '资源中心', exact: true })
    await panel.waitFor()
    await capture(`resources-${theme}`)
    await panel.getByRole('button', { name: '关闭并返回原页面', exact: true }).click()
    await panel.waitFor({ state: 'hidden' })
    await deploy.getByRole('heading', { name: '变更管控', exact: true }).waitFor()
  }
  for (const plugin of ['dbms', 'redis', 'logs', 'monitor']) {
    await page.goto(`${base}/api/plugins/${plugin}/ui`)
    for (const width of [1440, 700, 390]) {
      await page.setViewportSize({ width, height: 960 })
      await page.locator('.layout > header').waitFor()
      await overflow(page)
      await page.mouse.move(width-10, 900)
      await page.waitForTimeout(200)
      assert.equal(await page.locator('.layout > header').evaluate(e => e.getBoundingClientRect().width), 64)
    }
    await page.setViewportSize({ width: 1440, height: 960 })
    await capture(`${plugin}-dark`)
  }
  assert.deepEqual(errors, [], 'Browser errors')
  assert.deepEqual(writes, [], 'Unexpected business write')
  console.log(JSON.stringify({ ok: true, screenshots: output, checks: ['light/dark', '1440/900/700/390 widths', 'consistent sidebars', 'template entry/cancel', 'public center retains workspace', 'no browser errors', 'no business writes'] }))
} finally { await browser.close() }
