import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH})
const page=await browser.newPage({viewport:{width:1440,height:960}}),errors=[],writes=[]
page.on('pageerror',e=>errors.push(e.message))
const base='http://127.0.0.1:9800',html=await readFile('internal/devtools/assets/ui.html','utf8')
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname
 if(u.origin!==base)return route.abort()
 if(p==='/api/plugins/devtools/ui')return route.fulfill({body:html,contentType:'text/html'})
 if(p.endsWith('/api/tokens')&&r.method()==='GET')return route.fulfill({json:[]})
 if(p.endsWith('/api/tools'))return route.fulfill({json:[{name:'list_work_items',title:'查询需求与 Bug',scope:'workitems:read',read_only:true},{name:'trigger_build',title:'预览构建',scope:'builds:trigger',read_only:false}]})
 if(p==='/api/resources/environments')return route.fulfill({json:[{name:'test',protected:false},{name:'prod',protected:true}]})
 if(p.endsWith('/api/tokens')&&r.method()==='POST'){writes.push(r.postDataJSON());return route.fulfill({status:201,json:{token:{owner:'alice'},secret:'lp_dev_FIXTURE_ONLY'}})}
 return route.fulfill({json:[]})
})
try{
 await page.goto(base+'/api/plugins/devtools/ui')
 await page.getByRole('button',{name:'＋ 创建访问令牌',exact:true}).click()
 await page.locator('#dialog[open]').waitFor()
 for(const scope of ['workitems:write','changes:write','builds:trigger'])assert.equal(await page.locator('#scopes input[value="'+scope+'"]').isChecked(),false)
 assert.equal(await page.locator('#envs').inputValue(),'test')
 await page.getByRole('button',{name:'取消',exact:true}).click();assert.equal(writes.length,0)
 await page.getByRole('button',{name:'＋ 创建访问令牌',exact:true}).click();await page.locator('#dialog[open]').waitFor();await page.locator('#name').fill('fixture-codex');await page.locator('#apps').fill('*');await page.locator('#scopes input[value="workitems:write"]').check();await page.getByRole('button',{name:'创建',exact:true}).click();await page.getByText('请立即复制，关闭后无法再次查看').waitFor()
 assert(writes[0].scopes.includes('workitems:write'));assert(!writes[0].scopes.includes('builds:trigger'));assert.equal(writes.length,1)
 await page.getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'工具清单',exact:true}).click();await page.getByText('trigger_build',{exact:true}).waitFor()
 await page.getByRole('button',{name:'客户端接入',exact:true}).click();const config=await page.locator('#config').innerText();assert(config.includes('default_tools_approval_mode = "writes"'));assert(!config.includes('"auto"'))
 await page.screenshot({path:'/tmp/devops-mcp-workflow.png'});assert.deepEqual(errors,[])
 console.log(JSON.stringify({ok:true,checks:['write scopes unchecked','protected environment not auto selected','cancel never submits','only selected write capability granted','tool catalog','safe Codex example','all writes mocked']}))
}finally{await browser.close()}
