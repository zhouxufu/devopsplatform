// Read-only browser regression: fixture work items replace GET responses; all writes abort.
import assert from 'node:assert/strict'
const base = process.env.SHELL_UI_TEST_URL || 'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname, '127.0.0.1')
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) })
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
const embedded = process.env.SHELL_UI_TEST_EMBEDDED === '1'
const ui = embedded ? page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]') : page
const errors = [], writes = [], remoteRequests = []
let explicitPreview = false
const previewRequests = []
const relativeImage = 'https://zentao.example.test/upload/menu.png'
const failedImage = 'https://zentao.example.test/zentao/file-read-42.png'
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=', 'base64')
page.on('pageerror', e => errors.push(e.message))
const html = `<p>服务名：<strong>auth</strong> &amp; API</p><p>异常类型：BusinessException</p>
<ul><li>检查环境变量</li><li>核对配置</li></ul><pre><code>if (a &lt; b) { return true; }</code></pre>
<table><tr><th>字段</th><th>值</th></tr><tr><td>环境</td><td>开发</td></tr></table>
<a href="https://docs.example.test/guide">安全链接</a><a href="bug-view-39.html">相对链接</a>
<a href="javascript:window.__issueXSS=1">危险链接</a><a href="https://user:secret@evil.example.test/">凭据链接</a>
<script>window.__issueXSS=1</script><img src="https://evil.example.test/beacon" onerror="window.__issueXSS=1">
<img src="../upload/menu.png" alt="菜单截图" onload="window.__issueXSS=1"><img src="/zentao/file-read-42.png" alt="登录后可见截图">
<img src="javascript:window.__issueXSS=1"><img src="data:image/svg+xml;base64,PHN2Zz4="><img src="https://user:secret@evil.example.test/private.png">
<iframe src="https://evil.example.test/frame"></iframe><svg onload="window.__issueXSS=1"><script>window.__issueXSS=1</script></svg>
<p style="position:fixed" onclick="window.__issueXSS=1">只保留文本</p>` + Array.from({ length: 60 }, (_, i) => `<p>详细诊断第 ${i + 1} 行：日志和 JSON 不应撑满列表。</p>`).join('')
const plain = '直接说明：count < 5 && value > 1\n保留换行与 JSON {"enabled":true}'
const fixtures = [
  { id: 990001, title: '富文本测试 BUG', kind: 'bug', status: 'closed', owner: 'local', description: html, url: 'https://zentao.example.test/zentao/bug-view-38.html' },
  { id: 990002, title: '纯文本需求', kind: 'requirement', status: 'open', owner: 'local', description: plain },
  { id: 990003, title: '空说明需求', kind: 'requirement', status: 'open', owner: 'local', description: null },
  { id: 990004, title: '只有图片的需求', kind: 'requirement', status: 'open', owner: 'local', description: '<p><img src="../upload/menu.png" alt="菜单截图"></p>', url: 'https://zentao.example.test/zentao/bug-view-40.html' },
]
await page.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url())
  if (explicitPreview && [relativeImage, failedImage, 'https://evil.example.test/beacon'].includes(request.url())) {
    previewRequests.push({ url: request.url(), referer: request.headers().referer })
    return request.url() === failedImage ? route.fulfill({ status: 403, body: 'Login required' }) : route.fulfill({ contentType: 'image/png', body: png })
  }
  if (!['127.0.0.1', 'localhost'].includes(url.hostname)) { remoteRequests.push(request.url()); return route.abort() }
  if (!['GET', 'HEAD', 'OPTIONS'].includes(request.method())) { writes.push(url.pathname); return route.abort() }
  if (url.pathname.endsWith('/api/work-items')) return route.fulfill({ json: fixtures })
  if (['/api/changes', '/api/release-versions', '/api/delivery/runs'].some(s => url.pathname.endsWith(s))) return route.fulfill({ json: [] })
  return route.continue()
})
try {
  if (embedded) {
    await page.goto(base)
    await ui.getByRole('link', { name: '🧾 变更', exact: true }).click()
  } else await page.goto(base + (process.env.SHELL_UI_TEST_PATH || '/api/plugins/deploy/ui') + '#/changes?tab=work')
  const card = ui.locator('article.record').filter({ has: ui.getByRole('button', { name: '富文本测试 BUG', exact: true }) })
  await card.waitFor()
  const summary = await card.locator('.record-summary').innerText()
  assert(summary.startsWith('服务名：auth & API 异常类型：BusinessException'), summary)
  assert(!summary.includes('<p>') && !summary.includes('__issueXSS'))
  assert(Array.from(summary).length <= 181 && summary.endsWith('…'))
  for (const width of [1440, 900, 700]) {
    await page.setViewportSize({ width, height: 1000 })
    const lines = await card.locator('.record-summary').evaluate(el => el.clientHeight / parseFloat(getComputedStyle(el).lineHeight))
    assert(lines <= 2.1, `Summary exceeds two lines at ${width}`)
    assert(await card.evaluate(el => el.ownerDocument.documentElement.scrollWidth <= el.ownerDocument.defaultView.innerWidth + 1), `List overflow at ${width}`)
  }
  await card.getByRole('button', { name: '查看需求', exact: true }).click()
  const panel = ui.locator('.description-panel'), body = panel.locator('.description-body')
  await panel.getByRole('button', { name: '展开完整说明', exact: true }).waitFor()
  assert.equal(await body.locator('strong').innerText(), 'auth')
  assert.equal(await body.locator('ul li').count(), 2)
  assert((await body.locator('code').innerText()).includes('a < b'))
  assert.equal(await body.locator('table td').count(), 2)
  assert.equal(await body.getByText('相对链接', { exact: true }).getAttribute('href'), 'https://zentao.example.test/zentao/bug-view-39.html')
  assert.equal(await body.getByText('安全链接', { exact: true }).getAttribute('rel'), 'noopener noreferrer')
  for (const name of ['危险链接', '凭据链接']) assert.equal(await body.getByText(name, { exact: true }).getAttribute('href'), null)
  assert.equal(await body.locator('script,img,iframe,svg,math,object,embed,[onclick],[onerror],[style]').count(), 0)
  assert.equal(await body.evaluate(el => el.ownerDocument.defaultView.__issueXSS), undefined)
  assert.equal(await panel.locator('.description-images button').count(), 3, 'Unsafe image addresses admitted')
  assert.deepEqual(remoteRequests, [], 'Images requested before user click')
  assert((await body.boundingBox()).height <= 161, 'Long detail not initially collapsed')
  await panel.getByRole('button', { name: '展开完整说明', exact: true }).click()
  assert.equal(await panel.getByRole('button', { name: '收起说明', exact: true }).getAttribute('aria-expanded'), 'true')
  assert((await body.boundingBox()).height <= 560, 'Expanded detail must use bounded scrolling')
  assert((await body.innerText()).includes('详细诊断第 60 行'))
  await panel.getByRole('button', { name: '收起说明', exact: true }).click()
  assert((await body.boundingBox()).height <= 161)
  explicitPreview = true
  await panel.getByRole('button', { name: '查看图片 2 · 菜单截图', exact: true }).click()
  const preview = ui.getByRole('dialog', { name: '图片预览', exact: true })
  await preview.locator('img').evaluate(img => new Promise((resolve, reject) => { if (img.complete) return img.naturalWidth ? resolve() : reject(new Error('Image failed')); img.addEventListener('load', resolve, { once: true }); img.addEventListener('error', reject, { once: true }) }))
  assert.equal(await preview.locator('img').getAttribute('src'), relativeImage)
  assert.equal(await preview.getByRole('link', { name: '打开原图', exact: true }).getAttribute('href'), relativeImage)
  assert.equal(await preview.getByRole('link', { name: '打开外部记录', exact: true }).getAttribute('href'), fixtures[0].url)
  assert.deepEqual(previewRequests, [{ url: relativeImage, referer: undefined }])
  for (const width of [1440, 900, 700]) {
    await page.setViewportSize({ width, height: 1000 })
    const box = await preview.boundingBox(); assert(box.x >= 0 && box.x + box.width <= width + 1)
  }
  await preview.getByRole('button', { name: '关闭预览', exact: true }).click()
  await preview.waitFor({ state: 'hidden' })
  assert.equal(await body.locator('img').count(), 0)
  await panel.getByRole('button', { name: '查看图片 3 · 登录后可见截图', exact: true }).click()
  await preview.getByRole('alert').filter({ hasText: '图片加载失败' }).waitFor()
  assert.equal(previewRequests.length, 2)
  await preview.locator('.el-dialog__headerbtn').click()
  await preview.waitFor({ state: 'hidden' })
  explicitPreview = false
  await ui.getByRole('button', { name: '← 返回列表', exact: true }).click()
  await ui.getByRole('button', { name: '纯文本需求', exact: true }).click()
  assert.equal(await body.innerText(), plain)
  assert.equal(await panel.locator('.description-toggle').count(), 0)
  await ui.getByRole('button', { name: '← 返回列表', exact: true }).click()
  await ui.getByRole('button', { name: '空说明需求', exact: true }).click()
  assert.equal(await body.innerText(), '暂无说明')
  await ui.getByRole('button', { name: '← 返回列表', exact: true }).click()
  const imageCard = ui.locator('article.record').filter({ has: ui.getByRole('button', { name: '只有图片的需求', exact: true }) })
  assert.equal(await imageCard.locator('.record-summary').innerText(), '[图片 1：菜单截图]')
  await imageCard.getByRole('button', { name: '查看需求', exact: true }).click()
  assert.equal(await panel.locator('.description-images button').count(), 1)
  assert.equal(await body.locator('img').count(), 0)
  assert.deepEqual(errors, []); assert.deepEqual(writes, []); assert.deepEqual(remoteRequests, [])
  console.log(JSON.stringify({ ok: true, embedded, checks: ['two-line list summary', 'HTML formatting', 'entities and plain text', 'expand/collapse', 'null description', 'safe relative links', 'no automatic image requests', 'click-to-load preview', 'safe relative image paths', 'login failure fallback', 'unsafe image URLs blocked', 'image-only description', 'three viewport widths', 'no real writes'] }))
} finally { await browser.close() }
