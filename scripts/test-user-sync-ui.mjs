// Isolated mock API + freshly built shell. No requests reach real servers.
import assert from 'node:assert/strict'
import http from 'node:http'
import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright')
const source='zentao:'+'a'.repeat(64)
const users=[{username:'admin',display_name:'管理员',role:'admin',enabled:true,password_configured:true}]
const candidates=[{id:'1',account:'zhangsan',name:'张三',action:'create',reason:'创建成员，待设置平台密码后启用'},{id:'2',account:'admin',name:'远端管理员',action:'skip',reason:'平台保留账号，不自动关联'}]
const calls=[]
const server=http.createServer(async(req,res)=>{
  const path=new URL(req.url,'http://localhost').pathname
  if(path.startsWith('/api/')){
    res.setHeader('Content-Type','application/json')
    let data=[]
    if(path==='/api/me') data={username:'admin',display_name:'管理员',role:'admin',auth_enabled:true}
    if(path==='/api/users') data=users
    if(path==='/api/resources/connections') data=[{name:'回归禅道',kind:'zentao'}]
    if(path==='/api/users/sync-preview' || path==='/api/users/sync'){
      let raw='';for await(const chunk of req)raw+=chunk
      const body=JSON.parse(raw);calls.push({path,body})
      assert.equal(body.connection,'回归禅道')
      if(path.endsWith('/sync')){
        assert.equal(body.expected_source,source)
        assert.deepEqual(body.ids,['1'])
        users.push({username:'zhangsan',display_name:'张三',role:'member',enabled:false,password_configured:false,source})
      }
      data={source,label:'回归禅道',users:candidates}
    }
    if(req.method==='PUT' && path==='/api/users/zhangsan'){
      let raw='';for await(const chunk of req)raw+=chunk
      const body=JSON.parse(raw);assert.equal(body.password,'temporary-qa-password');assert.equal(body.enabled,true)
      Object.assign(users[1],{enabled:true,password_configured:true});data=users[1]
    }
    res.end(JSON.stringify(data));return
  }
  try{
    const file=resolve('web/dist',path==='/'?'shell.html':'.'+path)
    assert(file.startsWith(resolve('web/dist')+'/'))
    res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html')
    res.end(await readFile(file))
  }catch{res.writeHead(404);res.end()}
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
const browser=await chromium.launch({headless:true,...(process.env.CHROME_PATH?{executablePath:process.env.CHROME_PATH}:{})})
const page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[];page.on('pageerror',e=>errors.push(e.message))
try{
  await page.goto(`http://127.0.0.1:${server.address().port}`)
  await page.getByRole('button',{name:'管理员 · 管理员',exact:true}).click()
  const members=page.getByRole('dialog',{name:'成员管理',exact:true})
  await members.getByRole('button',{name:'从禅道同步',exact:true}).click()
  const sync=page.getByRole('dialog',{name:'从禅道同步成员',exact:true})
  await sync.getByRole('button',{name:'读取用户',exact:true}).click()
  await sync.getByText('张三',{exact:true}).waitFor()
  assert.equal(await sync.locator('input[type=checkbox]:checked').count(),1)
  assert.equal(await sync.locator('input[type=checkbox]:disabled').count(),1)
  for(const width of [1440,900,700]){
    await page.setViewportSize({width,height:900})
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
  }
  await sync.getByRole('button',{name:'同步所选（1）',exact:true}).click()
  await sync.getByText('同步完成：新增 1',{exact:false}).waitFor()
  await sync.getByRole('button',{name:'关闭',exact:true}).click()
  const row=members.locator('tr').filter({hasText:'zhangsan'})
  await row.getByText('待设置密码',{exact:true}).waitFor()
  await row.getByRole('button',{name:'编辑',exact:true}).click()
  const edit=page.getByRole('dialog',{name:'编辑成员',exact:true})
  await edit.getByText('设置平台登录密码（启用前必填）',{exact:true}).waitFor()
  await edit.locator('input[type=password]').fill('temporary-qa-password')
  await edit.getByRole('checkbox',{name:'启用账号'}).check()
  await edit.getByRole('button',{name:'保存',exact:true}).click()
  await edit.waitFor({state:'hidden'})
  await row.getByText('启用',{exact:true}).waitFor()
  await members.getByRole('button',{name:'从禅道同步',exact:true}).click()
  await sync.getByRole('button',{name:'关闭',exact:true}).click()
  await sync.waitFor({state:'hidden'})
  assert.equal(await sync.isVisible(),false,'Cancel should not validate required fields')
  assert.equal(calls.filter(c=>c.path.endsWith('/sync')).length,1)
  assert.deepEqual(errors,[])
  await page.screenshot({path:'runtime/user-sync-ui-qa.png',fullPage:true})
  console.log(JSON.stringify({ok:true,checks:['sync entry','preview','conflict disabled','selected import','pending password','edit and enable','cancel','three widths','no browser errors'],mockOnly:true}))
}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
