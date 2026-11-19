import test from 'node:test'
import assert from 'node:assert/strict'
import {editorAssetURL,editorRequestAllowed,editorProps,bridgeError,editorMessage} from './bridge.js'
import {reactive} from 'vue'
test('isolated editor asset resolves beside direct and gateway entries without changing API ownership',()=>{
 for(const [base,expected] of [['http://test/deploy.html#/changes','http://test/assets/delivery-editor.html'],['http://test/ui','http://test/assets/delivery-editor.html'],['http://test/api/plugins/research/ui','http://test/api/plugins/research/assets/delivery-editor.html'],['http://test/api/plugins/deploy/ui','http://test/api/plugins/deploy/assets/delivery-editor.html']])assert.equal(editorAssetURL(base),expected)
})
test('editor bridge accepts only existing editor operations, never arbitrary platform actions',()=>{
 for(const [path,method] of [['api/work-items/42/delivery-content','GET'],['api/work-items/42/delivery-content','PUT'],['api/changes/batch','POST'],['api/services/ai-service/resources','GET'],['api/environments','GET'],['api/git/branches','POST'],['api/research/work-items/sources','GET'],['api/research/work-items/page?product=0&q=a','GET'],['api/research/work-items/attach','POST'],['api/research/development/preview','POST'],['api/research/development/12/create','POST'],['api/research/development/12/reconcile','POST']])assert.equal(editorRequestAllowed(path,method),true,path)
 for(const [path,method] of [['https://evil.example/api/environments','GET'],['/api/environments','GET'],['api/services/../resources','GET'],['api/services/a%2fb/resources','GET'],['api/services/a\\b/resources','GET'],['api/releases','POST'],['api/users','POST'],['api/query','POST'],['api/work-items/42/delivery-content','DELETE'],['api/environments','PUT'],['api/research/development/12/create','GET']])assert.equal(editorRequestAllowed(path,method),false,path)
})
test('host bridge transports data only and retains login/conflict error identity',()=>{
 const source={show:true,item:{id:42,title:'示例'},services:[{name:'example'}],canManage:()=>true,token:'must-not-transfer'}
 const value=editorProps(source);assert.equal(value.canManage,undefined);assert.equal(value.token,undefined);value.item.title='changed';assert.equal(source.item.title,'示例')
 assert.deepEqual(bridgeError({message:'登录已过期',status:401,code:'platform_login_required',secret:'do-not-copy'}),{message:'登录已过期',status:401,code:'platform_login_required'})
})
test('reactive drafts cross the style boundary as independent data without DataCloneError',()=>{
 const draft=reactive({rows:[{key:'existing-1',sql:{content:'SELECT 1'}}],baseline:'reviewed-digest',initial:'original'})
 const message=editorMessage({type:'event',name:'draft',value:{key:42,draft}})
 assert.doesNotThrow(()=>structuredClone(message));message.value.draft.rows[0].sql.content='SELECT 2';assert.equal(draft.rows[0].sql.content,'SELECT 1')
})
test('ready editor can request guarded draft reopening without permitting arbitrary status or execution routes',()=>{
 assert.equal(editorRequestAllowed('api/changes/42/reopen','POST'),true)
 for(const path of ['api/changes/42/ready','api/changes/42/execute','api/changes/42/reopen/anything'])assert.equal(editorRequestAllowed(path,'POST'),false)
})
