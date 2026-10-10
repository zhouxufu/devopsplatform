// Read-only regression: verify the shared header, not just margins/sidebar width.
import assert from 'node:assert/strict'
import { readFile, mkdir } from 'node:fs/promises'
import path from 'node:path'
const base = process.env.CONSOLE_UI_TEST_URL || 'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname, '127.0.0.1')
const dist = process.env.CONSOLE_UI_DIST
const output = process.env.HEADER_UI_SCREENSHOTS || '/tmp/devops-page-header-ui'
await mkdir(output, { recursive: true })
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH })
const page = await browser.newPage({ viewport: { width: 1440, height: 960 } })
const errors = [], writes = [], checked = []
let detailFixture = false
const fixtureName = 'ui-header-application-with-a-long-identifier'
page.on('pageerror', error => errors.push(error.message))
page.on('console', message => { if (message.type() === 'warning' && message.text().includes('Failed to resolve component')) errors.push(message.text()) })
await page.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url())
  if (url.origin !== new URL(base).origin) return route.abort()
  if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method())) { writes.push(request.method() + ' ' + url.pathname); return route.abort() }
  if (detailFixture && url.pathname.startsWith('/api/plugins/deploy/api/')) {
    let body = []
    if (url.pathname.endsWith('/api/services')) body = [{ name: fixtureName, artifact_type: 'jar', git_url: 'git@example.invalid:group/project.git' }]
    else if (url.pathname.endsWith('/api/environments')) body = [{ name: 'test', display_name: '测试环境' }]
    else if (url.pathname.endsWith('/environments')) body = [{ environment: 'test', display_name: '测试环境', driver: 'ssh', strategy: 'rolling' }]
    else if (url.pathname.endsWith('/delivery')) body = { runner: 'local', variable_groups: [] }
    return route.fulfill({ json: body })
  }
  if (dist) {
    let file
    if (url.pathname === '/') file = 'shell.html'
    else if (/\/assets\/[^/]+\.(js|css)$/.test(url.pathname)) file = 'assets/' + path.basename(url.pathname)
    else if (/^\/api\/plugins\/(deploy|logs|monitor|dbms|redis)\/ui\/?$/.test(url.pathname)) {
      const plugin = url.pathname.split('/')[3]
      file = (['dbms', 'redis'].includes(plugin) ? 'workbench' : plugin) + '.html'
    }
    if (file) return route.fulfill({ body: await readFile(path.join(dist, file)), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' })
  }
  return route.continue()
})
async function verify(surface, name, navigation = false) {
  const header = surface.locator('.platform-page-header').first()
  await header.waitFor()
  const bounds = await header.evaluate(element => {
    const rect = node => { const r = node?.getBoundingClientRect(); return r ? { y: r.y, height: r.height, center: r.y + r.height / 2 } : null }
    return { root: rect(element), title: rect(element.querySelector('.page-header-title')), nav: rect(element.querySelector('.page-header-navigation')), actions: rect(element.querySelector('.page-header-actions')) }
  })
  assert(bounds.root.height <= 48, name + ': desktop header still occupies multiple rows: ' + JSON.stringify(bounds))
  if (navigation) assert(bounds.nav && Math.abs(bounds.nav.center - bounds.title.center) <= 3, name + ': title and navigation are not aligned')
  if (bounds.actions) assert(Math.abs(bounds.actions.center - bounds.title.center) <= 3, name + ': actions are not aligned')
  assert.equal(await header.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, name + ': horizontal overflow')
  checked.push(name)
}
try {
  for (const theme of ['light', 'dark']) {
    await page.addInitScript(value => localStorage.setItem('lp-theme', value), theme)
    for (const [plugin, hash, title, navigation] of [
      ['deploy', '/', '基础构建', false], ['deploy', '/orchestration', '部署编排', true],
      ['deploy', '/changes', '变更管控', true], ['deploy', '/plugins', '插件中心', true],
      ['dbms', '/query', '数据库工作台', true], ['redis', '/query', 'Redis 工作台', true],
      ['logs', '/subs', '日志订阅', false], ['monitor', '/alerts', '指标报警', false],
      ['monitor', '/rules', '可用率规则', false], ['monitor', '/machines', '机器指标', true],
      ['monitor', '/jvm', '服务指标', true], ['monitor', '/api', '接口分位', true],
    ]) {
      await page.setViewportSize({ width: 1440, height: 960 })
      await page.goto(`${base}/api/plugins/${plugin}/ui#${hash}`)
      await page.getByRole('heading', { name: title, exact: true }).waitFor()
      // API filters are not navigation tabs: allow them to wrap as a single control group.
      if (hash === '/api') {
        await page.locator('.platform-page-header').first().waitFor()
        checked.push(plugin + hash + '-' + theme)
      } else await verify(page, plugin + hash + '-' + theme, navigation)
      await page.screenshot({ path: `${output}/${plugin}-${hash.slice(1) || 'home'}-${theme}.png` })
      for (const width of [900, 700, 390]) {
        await page.setViewportSize({ width, height: 960 })
        await page.waitForTimeout(80)
        assert.equal(await page.locator('body').evaluate(() => document.documentElement.scrollWidth > innerWidth), false, title + ': overflow at ' + width)
        const header = page.locator('.platform-page-header').first()
        assert.equal(await header.evaluate(e => e.scrollWidth > e.clientWidth), false, title + ': header overflow at ' + width)
      }
    }
    await page.setViewportSize({ width: 1440, height: 960 })
    detailFixture = true
    {
      await page.goto(`${base}/api/plugins/deploy/ui#/svc/${encodeURIComponent(fixtureName)}/overview`)
      await page.getByRole('heading', { name: fixtureName, exact: true }).waitFor()
      assert.equal(await page.locator('.app-name').getAttribute('title'), fixtureName, 'Truncated application name loses its full-name hint')
      await page.locator('.header-environment').waitFor()
      await verify(page, 'application-detail-' + theme, true)
      await page.screenshot({ path: `${output}/application-detail-${theme}.png` })
      for (const width of [900, 700, 390]) {
        await page.setViewportSize({ width, height: 960 })
        assert.equal(await page.locator('.platform-page-header').first().evaluate(e => e.scrollWidth > e.clientWidth), false, 'Application header overflow at ' + width)
      }
      await page.setViewportSize({ width: 1440, height: 960 })
    }
    detailFixture = false
    await page.goto(base)
    const deploy = page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]')
    await deploy.getByRole('heading', { name: '基础构建', exact: true }).waitFor()
    await verify(deploy, 'shell/build-' + theme)
    for (const [link, title] of [['编排', '部署编排'], ['变更', '变更管控']]) {
      await deploy.getByRole('link', { name: link, exact: true }).click()
      await deploy.getByRole('heading', { name: title, exact: true }).waitFor()
      await verify(deploy, 'shell/' + link + '-' + theme, true)
    }
    for (const [label, title] of [['应用', '应用中心'], ['资源', '资源中心']]) {
      await deploy.locator('.platform-nav button').filter({ hasText: label }).click()
      const dialog = page.getByRole('dialog', { name: title, exact: true })
      await dialog.waitFor()
      await verify(dialog, 'public/' + label + '-' + theme)
      await dialog.getByRole('button', { name: '关闭并返回原页面', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      await deploy.getByRole('heading', { name: '变更管控', exact: true }).waitFor()
    }
  }
  assert.deepEqual(errors, [], 'Browser errors / unresolved components')
  assert.deepEqual(writes, [], 'Unexpected business writes')
  console.log(JSON.stringify({ ok: true, checked, widths: [1440, 900, 700, 390], output }))
} finally { await browser.close() }
