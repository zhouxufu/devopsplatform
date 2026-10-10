// Actual local UI with mocked data-sync APIs. No metadata or database writes.
import assert from 'node:assert/strict'
const base = process.env.SHELL_UI_TEST_URL || 'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname, '127.0.0.1')
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({ headless: true, executablePath: process.env.CHROME_PATH })
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
const errors = [], writes = [], saved = [], comparisons = [], frozen = []
page.on('pageerror', e => errors.push(e.message))
let task
await page.route('**/api/**', async route => {
  const request = route.request(), path = new URL(request.url()).pathname.replace('/api/workbench/dbms/','/api/'), method = request.method()
  const respond = json => route.fulfill({ json })
  if (method === 'GET' && path.endsWith('/api/me')) return respond({username:'local',role:'admin',auth_enabled:true})
  if (method === 'GET' && path.endsWith('/api/registry/applications')) return respond([{name:'ui-sync-api'}])
  if (method === 'GET' && path.endsWith('/api/policy')) return respond({query_approval:false,change_approval:true})
  if (method === 'GET' && path.endsWith('/api/services')) return respond([{ name: 'ui-sync-api' }])
  if (method === 'GET' && path.endsWith('/api/environments')) return respond(['dev', 'test', 'prod'].map(name => ({ name, display_name: name })))
  if (method === 'GET' && path.endsWith('/api/services/ui-sync-api/resources')) return respond({ requirements: [{ alias: 'db', kind: 'mysql' }], bindings: [] })
  if (method === 'GET' && path.endsWith('/api/data-sync/tasks')) return respond(task ? [task] : [])
  if (method === 'POST' && path.endsWith('/api/data-sync/tasks')) {
    saved.push(request.postDataJSON()); task = { ...saved.at(-1), id: 990010, revision: 1, owner: 'local' }; return respond(task)
  }
  if (method === 'POST' && path.endsWith('/api/data-sync/tasks/990010/compare')) {
    const scope = request.postDataJSON(); comparisons.push(scope)
    return respond({ task, ...scope, digest: scope.source_environment + '->' + scope.target_environment, columns: ['id', 'name'], differences: [{ key: '["1"]', kind: 'update', before: { id: '1', name: 'old' }, after: { id: '1', name: 'new' } }], unchanged: 1, target_only: 1 })
  }
  if (method === 'POST' && path.endsWith('/api/data-sync/tasks/990010/freeze')) {
    const plan = request.postDataJSON(); frozen.push(plan)
    return respond({ id: 990011, sql: { content: "UPDATE menu SET name='edited' WHERE id=1;", data_sync: { task_id: task.id, task_revision: task.revision, source_environment: plan.source_environment, target_environment: plan.target_environment } } })
  }
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method)) { writes.push(method + ' ' + path); return route.abort() }
  return route.continue()
})
const field = (dialog, label) => dialog.locator('label').filter({ hasText: new RegExp('^' + label) })
async function choose(dialog, label, option) {
  const container = field(dialog, label)
  await container.locator('.el-select').click()
  const popup = await container.getByRole('combobox').getAttribute('aria-controls')
  await page.locator(`[id="${popup}"]`).getByRole('option', { name: option, exact: true }).click()
}
try {
  await page.goto(base + (process.env.SHELL_UI_TEST_PATH || '/api/plugins/dbms/ui') + '#/sync')
  const dialog = page.locator('.sync-page')
  await dialog.getByRole('heading', {name:'数据对比同步',exact:true}).waitFor()
  assert.equal(await page.getByRole('dialog').count(),0,'Sync still opens another window')
  await dialog.getByRole('button', { name: '＋ 新建任务', exact: true }).click()
  await dialog.getByText('尚未保存任务', { exact: true }).waitFor()
  assert.equal(await dialog.locator('.grid .el-select').count(), 2, 'Task definition still has environment selectors')
  await field(dialog, '任务名称').locator('input').fill('可跨环境复用的菜单任务')
  await choose(dialog, '应用', 'ui-sync-api'); await choose(dialog, '逻辑数据库资源', 'db')
  await field(dialog, '来源查询 SQL').locator('textarea').fill('SELECT id,name FROM menu WHERE id > 0')
  await field(dialog, '目标查询 SQL').locator('textarea').fill('SELECT id,name FROM menu WHERE id > 0')
  await field(dialog, '匹配键').locator('input').fill('id')
  await dialog.getByRole('button', { name: '保存任务', exact: true }).click()
  await dialog.getByRole('heading', { name: '本次对比', exact: true }).waitFor()
  assert.equal(saved.length, 1)
  assert(!('source_environment' in saved[0]) && !('target_environment' in saved[0]))
  await dialog.getByRole('button', { name: '对比数据（只读）', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: '请选择本次来源和目标环境' }).waitFor()
  assert.equal(comparisons.length, 0)
  await choose(dialog, '本次来源环境', 'dev'); await choose(dialog, '本次目标环境', 'dev')
  await dialog.getByRole('button', { name: '对比数据（只读）', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: '来源和目标环境不能相同' }).waitFor()
  await choose(dialog, '本次目标环境', 'test')
  await dialog.getByRole('button', { name: '对比数据（只读）', exact: true }).click()
  await dialog.locator('.diff-list article').waitFor()
  await dialog.locator('.diff-title input').check()
  await choose(dialog, '本次来源环境', 'prod')
  await dialog.locator('.diff-list').waitFor({ state: 'hidden' })
  await dialog.getByRole('button', { name: '对比数据（只读）', exact: true }).click()
  await dialog.locator('.diff-list article').waitFor()
  assert.equal(await dialog.locator('.diff-title input').isChecked(), false)
  await choose(dialog, '本次目标环境', 'dev')
  await dialog.locator('.diff-list').waitFor({ state: 'hidden' })
  await choose(dialog, '本次来源环境', 'test'); await choose(dialog, '本次目标环境', 'prod')
  await dialog.getByRole('button', { name: '对比数据（只读）', exact: true }).click()
  await dialog.locator('.diff-list article').waitFor()
  await dialog.locator('.diff-title input').check()
  await dialog.locator('.diff-row').filter({ has: page.getByText('name', { exact: true }) }).locator('.el-input input').fill('edited')
  await dialog.getByRole('button', { name: /^生成同步草稿/ }).click()
  await dialog.locator('.result').waitFor()
  assert.equal(frozen.length, 1)
  assert.equal(frozen[0].source_environment, 'test'); assert.equal(frozen[0].target_environment, 'prod')
  assert.equal(frozen[0].digest, 'test->prod'); assert.equal(frozen[0].rows[0].after.name, 'edited')
  assert.equal(saved.length, 1, 'Scope choices rewrote reusable task')
  assert.deepEqual(comparisons, [{ source_environment: 'dev', target_environment: 'test' }, { source_environment: 'prod', target_environment: 'test' }, { source_environment: 'test', target_environment: 'prod' }])
  for (const width of [1440, 900, 700]) {
    await page.setViewportSize({ width, height: 1000 })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
    const box = await dialog.boundingBox(); assert(box.x >= 0 && box.x + box.width <= width + 1)
  }
  await dialog.getByRole('button', { name: '关闭并返回原页面', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await page.getByRole('heading', { name: '数据库工作台', exact: true }).waitFor()
  assert.deepEqual(errors, []); assert.deepEqual(writes, [])
  console.log(JSON.stringify({ ok: true, checks: ['task saved without environments or bindings', 'both environments chosen per comparison', 'same environment rejected', 'source/target changes invalidate selection', 'same task reused dev/test/prod', 'freeze binds selected scope', 'task revision untouched', 'responsive', 'return preserves page', 'no real writes'] }))
} finally { await browser.close() }
