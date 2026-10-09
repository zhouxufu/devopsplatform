// Disposable, loopback-only platform + core. No user databases or remote writes.
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import {spawn} from 'node:child_process'
const root=process.cwd(),dir=fs.mkdtempSync(path.join(os.tmpdir(),'devops-workbench-platform.'))
const addr=process.env.WORKBENCH_QA_ADDR||'127.0.0.1:19824',core=process.env.WORKBENCH_QA_CORE_ADDR||'127.0.0.1:19825'
assert.match(addr,/^127\.0\.0\.1:\d+$/);assert.match(core,/^127\.0\.0\.1:\d+$/)
const base='http://'+addr
function manifest(name,body){const p=path.join(dir,'plugins',name);fs.mkdirSync(p,{recursive:true});fs.writeFileSync(path.join(p,'manifest.yaml'),body)}
manifest('delivery-core',`name: delivery-core\ninternal: true\nbinary: ${root}/bin/deploy\nadmin_addr: ${core}\nargs: ["-admin-addr", "${core}", "-data", "${dir}/data", "-bus", "${base}"]\n`)
manifest('deploy','name: deploy\nbackend: delivery-core\nui_path: /ui\nenabled: false\n')
for(const kind of ['dbms','redis'])manifest(kind,`name: ${kind}\nbackend: delivery-core\nui_path: /ui/${kind}\n`)
const env=Object.fromEntries(Object.entries(process.env).filter(([k])=>!k.startsWith('DEVOPSPLATFORM_')&&!k.startsWith('LOGPLATFORM_')))
const log=fs.openSync(path.join(dir,'platform.log'),'a')
const child=spawn(path.join(root,'bin/platform'),['-addr',addr,'-plugins',path.join(dir,'plugins'),'-data',path.join(dir,'platform-data')],{env,stdio:['ignore',log,log]})
const ended=new Promise(resolve=>child.once('exit',resolve))
async function json(p,body){const r=await fetch(base+p,body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{});const value=await r.json();assert.equal(r.status,200,JSON.stringify(value));return value}
try{
 let ready=false
 for(let i=0;i<80;i++){try{await json('/api/resources/environments');ready=true;break}catch{}await new Promise(r=>setTimeout(r,100))}
 assert(ready,'isolated platform not ready')
 const plugins=await json('/api/plugins');assert.deepEqual(plugins.map(p=>p.name).sort(),['dbms','redis'])
 assert.equal((await fetch(base+'/api/plugins/deploy/ui')).status,404)
 assert.equal((await fetch(base+'/api/plugins/delivery-core/ui')).status,404)
 for(const kind of ['dbms','redis']){
  const response=await fetch(base+`/api/plugins/${kind}/ui`);assert.equal(response.status,200)
  const html=await response.text();const asset=html.match(/src="\.\/(assets\/[^"]+\.js)"/);assert(asset)
  assert.equal((await fetch(base+`/api/plugins/${kind}/`+asset[1])).status,200)
  assert.equal((await fetch(base+`/api/plugins/${kind}/api/builds`)).status,404)
  await json(`/api/plugins/${kind}/api/workbench/${kind}/services`)
 }
 await json('/api/resources/setup/environments',{environments:[{name:'test',display_name:'测试',approvers:'reviewer'}]})
 const tpl=await json('/api/plugins/dbms/api/workbench/dbms/delivery/templates',{workbench:'dbms',mode:'fixed',name:'DBMS QA',services:[],nodes:[{id:'operation',name:'SQL',kind:'mysql'}]})
 const preview=await json('/api/plugins/dbms/api/workbench/dbms/delivery/preview',{template_id:tpl.id,environment:'test'})
 assert(preview.blockers.length>0,'empty SQL operation was not blocked')
 await json('/api/plugins/dbms/api/workbench/dbms/applications',{name:'resource-only'})
 assert((await json('/api/registry/applications')).some(a=>a.name==='resource-only'))
 assert((await json('/api/plugins/redis/api/workbench/redis/services')).some(a=>a.name==='resource-only'))
 assert.deepEqual(await json('/api/resources/services'),[],'resource onboarding created a fake build service')
 assert.equal((await fetch(base+'/api/plugins/redis/api/workbench/dbms/changes')).status,404)
 console.log(JSON.stringify({ok:true,dir,checks:['deployment plugin disabled','shared backend starts','core hidden','both independent UIs and assets','scoped APIs','resource center and setup','real DBMS template and safe empty-scope preflight','shared application identity','no fake build service','no remote writes']}))
}finally{
 child.kill('SIGTERM');await Promise.race([ended,new Promise(resolve=>setTimeout(resolve,5000))]);fs.closeSync(log)
}
