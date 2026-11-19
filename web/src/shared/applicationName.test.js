import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {applicationName,applicationIdentifier,applicationOption,applicationNameError,validApplicationIdentifier} from './applicationName.js'
test('Chinese labels never replace service keys in selectors',()=>{
 const app={name:'orders',identifier:'orders',display_name:'订单中心'}
 assert.deepEqual(applicationOption(app),{value:'orders',label:'订单中心 · orders'})
 assert.equal(applicationName(app),'订单中心');assert.equal(applicationIdentifier(app),'orders')
 assert.equal(applicationOption({name:'旧中文',identifier:'legacy-ab',display_name:'历史服务'}).value,'旧中文')
 assert.equal(applicationName({name:'legacy'}),'legacy')
})
test('application name and identifier have distinct validation',()=>{
 assert.equal(applicationNameError('智能搜索'), '');assert.ok(applicationNameError(' '));assert.ok(applicationNameError('a\nb'));assert.ok(applicationNameError('中'.repeat(101)))
 assert.ok(validApplicationIdentifier('estate-ai-seek'));for(const key of ['中文','UPPER','a_b','a/b','a'.repeat(64)])assert.equal(validApplicationIdentifier(key),false)
})
test('application controls read platform identity even at nested plugin URLs',()=>{
 const base='https://example.invalid/api/plugins/deploy/ui'
 assert.equal(new URL('/api/me',base).pathname,'/api/me')
 assert.notEqual(new URL('api/me',base).pathname,'/api/me')
 for(const file of ['../deploy/pages/ServicesView.vue','../deploy/pages/DetailView.vue']){
  const source=readFileSync(new URL(file,import.meta.url),'utf8')
  assert.match(source,/fetch\('\/api\/me'\)/)
  assert.doesNotMatch(source,/fetch\('api\/me'\)/)
 }
})
test('name edits and fixed identifiers are visually distinct and deletion is discoverable',()=>{
 const detail=readFileSync(new URL('../deploy/pages/DetailView.vue',import.meta.url),'utf8')
 const editor=readFileSync(new URL('./components/ApplicationNameEditor.vue',import.meta.url),'utf8')
 for(const source of [detail,editor]){
  assert.match(source,/应用标识（固定）<\/FieldLabel><span[^>]*><code>/)
  assert.doesNotMatch(source,/<(?:AInput|Input)[^>]*applicationIdentifier/)
 }
 assert.match(detail,/class="application-delete-action"/)
 assert.match(detail,/<Button[^>]*@click="deleteApplication"[^>]*:disabled="\(me.role!=='admin'/)
 assert.ok(editor.indexOf('应用名称')<editor.indexOf('应用标识（固定）'))
 assert.match(editor,/body:JSON.stringify\(\{display_name:label,expected_display_name:expected.value\}\)/)
})
test('catalog removal preserves administrator controls and never cascades deployment deletion',()=>{
 const source=readFileSync(new URL('./pages/ApplicationCenterView.vue',import.meta.url),'utf8')
 assert.match(source,/<template v-if="me.role==='admin'">/)
 assert.match(source,/method:'DELETE'/)
 // The catalog's source badge can be stale. Only the backend's authoritative
 // deployment check may block archival; never restore the old client guard.
 assert.doesNotMatch(source,/if\(app.sources\?\.includes\('deploy'\)\)/)
 assert.match(source,/if\(r.status===409\)/)
 assert.match(source,/前往部署设置/)
 assert.doesNotMatch(source,/fetch\([^\n]*plugins\/deploy[^\n]*method:'DELETE'/)
 assert.match(source,/deleting.value=app.application_id/)
 assert.match(source,/删除结果未确认/)
})
