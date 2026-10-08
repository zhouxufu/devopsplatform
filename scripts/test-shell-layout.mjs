// Read-only smoke test against the actual local shell. Never submits changes.
import assert from 'node:assert/strict'
const base = process.env.SHELL_UI_TEST_URL || 'http://127.0.0.1:9800'
assert.equal(new URL(base).hostname, '127.0.0.1')
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const browser = await chromium.launch({headless:true,...(process.env.CHROME_PATH ? {executablePath:process.env.CHROME_PATH} : {})})
const page = await browser.newPage({viewport:{width:1440,height:1000}})
const errors = [], writes = []
page.on('pageerror',e=>errors.push(e.message))
await page.route('**/api/**',async route=>{
  if (!['GET','HEAD','OPTIONS'].includes(route.request().method())) {
    writes.push(route.request().method()+' '+route.request().url())
    return route.abort()
  }
  return route.continue()
})
const checkLayout = async (surface, width) => {
  await surface.locator('.layout > header').waitFor()
  const geometry = await surface.locator('.layout > header').evaluate(header=>({
    width:header.getBoundingClientRect().width,
    direction:getComputedStyle(header.querySelector('nav')).flexDirection,
    links:[...header.querySelectorAll('nav a')].map(e=>({x:e.getBoundingClientRect().x,y:e.getBoundingClientRect().y})),
    overflow:document.documentElement.scrollWidth>innerWidth,
  }))
  assert.equal(geometry.width,width,'Sidebar shrank or expanded')
  assert.equal(geometry.direction,'column','Navigation wrapped horizontally')
  assert.equal(geometry.overflow,false,'Page overflow')
  assert(geometry.links.every((v,i,all)=>i===0||(v.y>all[i-1].y&&v.x===all[0].x)))
}
try {
  for (const plugin of ['deploy','logs','monitor']) {
    await page.goto(`${base}/api/plugins/${plugin}/ui`+(plugin==='deploy'?'#/orchestration':''))
    for (const width of [1440,900,700]) {
      await page.setViewportSize({width,height:900})
      await checkLayout(page,160)
    }
  }
  await page.setViewportSize({width:1440,height:1000})
  await page.goto(base)
  const deploy=page.frameLocator('iframe[src*="/api/plugins/deploy/ui"]')
  await checkLayout(deploy,46)
  await deploy.getByRole('link',{name:'🚀 编排'}).click()
  await deploy.getByRole('heading',{name:'部署编排',exact:true}).waitFor()
  await deploy.getByRole('link',{name:'🧾 变更'}).click()
  await deploy.locator('.platform-nav button').filter({hasText:'资源'}).click()
  const panel=page.getByRole('dialog',{name:'资源中心',exact:true})
  await panel.waitFor()
  await panel.getByRole('button',{name:'＋ 新建连接'}).click()
  const connection=page.getByRole('dialog',{name:'资源连接',exact:true})
  await connection.locator('.fld').filter({has:page.getByText('类型',{exact:true})}).locator('.el-select').click()
  await page.getByRole('option',{name:'禅道 16.0（需求来源）',exact:true}).waitFor()
  await page.getByRole('option',{name:'MySQL 变更数据库',exact:true}).waitFor()
  await page.keyboard.press('Escape')
  await connection.locator('.el-dialog__headerbtn').click()
  await panel.getByRole('button',{name:'关闭并返回原页面'}).click()
  await panel.waitFor({state:'hidden'})
  await deploy.getByRole('heading',{name:'变更管控',exact:true}).waitFor()
  assert.equal(await deploy.getByRole('button',{name:'资源连接',exact:true}).count(),0,'Low-frequency connection entry returned to main toolbar')
  assert.equal(await deploy.getByRole('button',{name:'应用资源绑定',exact:true}).count(),0,'Low-frequency binding entry returned to main toolbar')
  await page.locator('button.account').click()
  const members=page.getByRole('dialog',{name:'成员管理',exact:true})
  await members.getByRole('button',{name:'从禅道同步',exact:true}).click()
  const userSync=page.getByRole('dialog',{name:'从禅道同步成员',exact:true})
  await userSync.getByRole('button',{name:'读取用户',exact:true}).waitFor()
  await userSync.getByRole('button',{name:'关闭',exact:true}).click()
  await userSync.waitFor({state:'hidden'})
  await members.locator('.el-dialog__headerbtn').click()
  await members.waitFor({state:'hidden'})
  await deploy.getByRole('heading',{name:'变更管控',exact:true}).waitFor()
  await page.screenshot({path:'runtime/shell-layout-qa.png',fullPage:true})
  assert.deepEqual(errors,[],'Browser runtime errors')
  assert.deepEqual(writes,[],'Unexpected write during shell inspection')
  console.log(JSON.stringify({ok:true,checks:['3 standalone plugins × 3 widths','fixed vertical navigation','embedded narrow sidebar','orchestration in shell','resource center preserves workspace','MySQL/ZenTao connection choices','low-frequency configuration removed from main toolbar','user sync entry and cancel preserves workspace','no browser errors','read-only inspection']}))
} catch(error) {
  await page.screenshot({path:'runtime/shell-layout-failure.png',fullPage:true})
  throw error
} finally {await browser.close()}
