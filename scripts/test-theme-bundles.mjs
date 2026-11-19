import assert from 'node:assert/strict'
import {readFile,readdir} from 'node:fs/promises'
import {createHash} from 'node:crypto'
const ids=['gi-demo','wst2','0000','mengtu','taolu-publishing-platform']
const dirs=['internal/platform/assets','internal/deploy/assets/app','internal/monitor/assets/app','internal/server/assets/app','internal/devtools/assets/app']
const hash=bytes=>createHash('sha256').update(bytes).digest('hex')
const assets=await readdir('web/dist/assets')
for(const id of ids){
 const files=assets.filter(f=>f.startsWith(id+'-')&&f.endsWith('.js'));assert.equal(files.length,1,id+' lazy bundle')
 const file=files[0],source=await readFile('web/dist/assets/'+file)
 assert(source.includes(Buffer.from('.arco-table')));assert(source.includes(Buffer.from('arco-theme')))
 for(const dir of dirs)assert.equal(hash(await readFile(dir+'/assets/'+file)),hash(source),dir+' outdated theme '+id)
}
for(const page of ['shell','deploy','logs','monitor','research','workbench','api-center','devtools']){
 const html=await readFile('web/dist/'+page+'.html','utf8')
 assert.doesNotMatch(html,/(?:gi-demo|wst2|0000|mengtu|taolu-publishing-platform)-[^"\s]+\.js/,'optional theme eagerly loaded: '+page)
}
for(const dir of dirs)assert.match(await readFile(dir+'/theme-notices.txt','utf8'),/@arco-themes\/vue-mengtu 0.1.1/)
assert.equal((await readdir('web/dist')).includes('theme-comparison-qa.html'),false)
console.log('PASS: five lazy theme bundles identical in all five embed destinations; theme notices shipped; no eager themes or QA entry in production HTML')
