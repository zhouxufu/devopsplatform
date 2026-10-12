// Fixture-only UI regression: no real approval, deployment or resource write.
import assert from 'node:assert/strict'
import {readFile,mkdir} from 'node:fs/promises'
import path from 'node:path'
const base=process.env.CONSOLE_UI_TEST_URL||'http://127.0.0.1:9800',dist=process.env.CONSOLE_UI_DIST
assert.equal(new URL(base).hostname,'127.0.0.1')
const {chromium}=await import(process.env.PLAYWRIGHT_MODULE||'playwright')
const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH}),page=await browser.newPage({viewport:{width:1440,height:1000}})
const errors=[],writes=[];page.on('pageerror',e=>errors.push(e.message))
const users=['author','dev','qa1','qa2','pm'].map(username=>({username,display_name:username,enabled:true,role:'member'}))
let actor='author',saved,run
await page.route('**/*',async route=>{
 const r=route.request(),u=new URL(r.url()),p=u.pathname.replace(/^\/api\/plugins\/deploy(?=\/api\/)/,'')
 if(u.origin!==new URL(base).origin)return route.abort()
 if(dist){let f;if(/\/api\/plugins\/deploy\/ui\/?$/.test(u.pathname))f='deploy.html';else if(/\/assets\/[^/]+\.(js|css)$/.test(u.pathname))f='assets/'+path.basename(u.pathname);if(f)return route.fulfill({body:await readFile(path.join(dist,f)),contentType:f.endsWith('.js')?'text/javascript':f.endsWith('.css')?'text/css':'text/html'})}
 if(!p.includes('/api/')||!['fetch','xhr'].includes(r.resourceType()))return route.continue()
 const reply=json=>route.fulfill({json})
 if(r.method()==='POST'){
  const body=r.postDataJSON();writes.push({p,body})
  if(p==='/api/delivery/templates'){
   saved={...body,id:1,owner:'author'}
   const nodes=saved.nodes
   run={id:1,status:'pending_approval',digest:'fixture-digest',owner:'author',created_at:1791780000,plan:{template:saved,environment:{name:'test',display_name:'测试',approvers:'dev,qa1,qa2,pm'},nodes,services:[],sql_plans:[],nacos_plans:[],redis_plans:[]},steps:nodes.map(n=>({node_id:n.id,name:n.name,kind:n.kind,status:'pending',reviews:[]}))}
   return reply(saved)
  }
  if(p==='/api/delivery/runs/1/approve'){
   const step=run.steps.find(s=>s.kind==='approval'&&s.status!=='success'),node=run.plan.nodes.find(n=>n.id===step.node_id)
   assert.equal(body.node_id,step.node_id);assert.equal(body.digest,run.digest);assert(node.approvers.includes(actor))
   step.reviews.push({actor,approved:true,comment:body.comment,at:1791780000})
   step.status=node.approval_mode==='any'||step.reviews.length===node.approvers.length?'success':'reviewing'
   if(run.steps.filter(s=>s.kind==='approval').every(s=>s.status==='success'))run.status='approved'
   return reply(run)
  }
  throw Error('unexpected business write '+p)
 }
 assert.equal(r.method(),'GET')
 if(p==='/api/me')return reply({username:actor,role:'member'})
 if(p==='/api/users')return reply(users)
 if(p==='/api/delivery/templates')return reply(saved?[saved]:[])
 if(p==='/api/environments')return reply([{name:'test',display_name:'测试'}])
 if(p==='/api/delivery/runs/page')return reply({items:[],total:0,page:1,page_size:20})
 if(p==='/api/delivery/runs/1')return reply(run)
 if(p==='/api/delivery/runs/1/control')return reply({state:'idle',message:'审批不会启动执行'})
 return reply([])
})
async function select(control,option){await control.click();const id=await control.getByRole('combobox').getAttribute('aria-controls');await page.locator('#'+id).getByRole('option',{name:option,exact:true}).click();await page.keyboard.press('Escape')}
async function loginFixture(name){actor=name;await page.goto(base+'/api/plugins/deploy/ui#/orchestration?run=1');await page.reload();await page.getByRole('button',{name:'审核通过',exact:true}).waitFor()}
try{
 await page.goto(base+'/api/plugins/deploy/ui#/orchestration');await page.getByRole('button',{name:'编排模板',exact:true}).click();await page.getByRole('button',{name:'＋ 新建编排',exact:true}).click()
 const form=page.locator('form.form');await form.locator('label').filter({hasText:/^编排名称/}).locator('input').fill('三阶段交付')
 await select(form.locator('label').filter({hasText:/^交付范围/}).locator('.el-select'),'由发布版本推导')
 await page.getByRole('button',{name:'＋ 三阶段审核',exact:true}).click()
 const nodes=page.locator('.node-edit')
 for(const [i,names] of [[0,['dev']],[1,['qa1','qa2']],[2,['pm']]]){
  const node=nodes.nth(i);await node.locator('summary').click()
  for(const name of names)await select(node.locator('label').filter({hasText:/^审批人/}).locator('.el-select'),name+'（'+name+'）')
  if(i===1)await select(node.locator('label').filter({hasText:/^签署规则/}).locator('.el-select'),'会签：全部通过')
 }
 await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('编排已保存',{exact:true}).waitFor()
 assert.equal(saved.nodes.filter(n=>n.kind==='approval').length,3);for(const n of saved.nodes)assert(/^[a-z][a-z0-9_-]{0,31}$/.test(n.id))
 await loginFixture('dev');await page.getByPlaceholder('核对发布内容后填写意见').fill('开发检查通过');await page.getByRole('button',{name:'审核通过',exact:true}).click();assert.equal(run.status,'pending_approval')
 await loginFixture('qa1');await page.getByRole('button',{name:'审核通过',exact:true}).click();assert.equal(run.steps[1].status,'reviewing');await page.getByRole('button',{name:'审核通过',exact:true}).waitFor({state:'hidden'})
 await loginFixture('qa2');await page.getByRole('button',{name:'审核通过',exact:true}).click();assert.equal(run.steps[1].status,'success');assert.equal(run.status,'pending_approval')
 await loginFixture('pm');await page.getByRole('button',{name:'审核通过',exact:true}).click();assert.equal(run.status,'approved');assert.equal(run.steps.at(-1).status,'pending')
 assert(!writes.some(w=>/\/(start|advance)$/.test(w.p)),'approval auto-executed the run')
 await mkdir('/tmp/devops-staged-approval-ui',{recursive:true});await page.screenshot({path:'/tmp/devops-staged-approval-ui/stages.png'})
 assert.deepEqual(errors,[]);console.log(JSON.stringify({ok:true,checks:['three-node preset','select existing members','any/all modes','sequential signing','audit opinions','approval does not execute','fixture writes only']}))
}finally{await browser.close()}
