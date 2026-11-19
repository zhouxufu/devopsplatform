import assert from 'node:assert/strict'
import {validateDeliveryChange,locateDeliveryServerError} from '../web/src/deploy/deliveryValidation.js'
import {deliveryRows,expandDeliveryRows} from '../web/src/deploy/deliveryContent.js'
const valid={kind:'nacos',service:'api',nacos:{resource_alias:'config',group:'DEFAULT_GROUP',format:'yaml',entries:[{key:'feature.enabled',action:'set',value:'true'}]}}
const clone=x=>JSON.parse(JSON.stringify(x))
assert.equal(validateDeliveryChange(valid,'nacos-1'),null)
for(const [key,message] of [['','请填写'],['feature.enabled','重复'],['feature','父路径'],['invalid..key','名称使用']]){
 const c=clone(valid);c.nacos.entries.push({key,action:'set',value:''});const e=validateDeliveryChange(c,'nacos-1');assert.equal(e.rowKey,'nacos-1');assert.equal(e.field,'key');assert.equal(e.entry,1);assert(e.message.includes(message))
}
const code={kind:'code',key:'shared-code',services:['api','web'],originals:[],reference:'feature/x'}
const sql={kind:'mysql',key:'sql-1',service:'api',originals:[],sql:{resource_alias:'db',content:'invalid SQL',max_affected_rows:100}}
const unchanged={kind:'manual',key:'unchanged',service:'',reference:'verify',title:'old',originals:[{id:9,kind:'manual',service:'',reference:'verify',title:'old'}]}
const origins=[],items=expandDeliveryRows([unchanged,code,sql],'QA',origins)
assert.deepEqual(origins,['shared-code','shared-code','sql-1'])
assert.deepEqual(locateDeliveryServerError('第 3 项：SQL 解析失败',items,origins),{rowKey:'sql-1',field:'sql',entry:undefined,message:'第 3 项：SQL 解析失败'})
assert.equal(locateDeliveryServerError('网络异常',items,origins),null)
assert.equal(locateDeliveryServerError('第 99 项：未声明资源',items,origins),null)
assert.equal(locateDeliveryServerError('第 1 项：第 2 个配置项：期望值不能超过 64 KiB',[valid],['nacos-1']).field,'value')
assert.throws(()=>expandDeliveryRows([{...code,services:[]}],'QA'),e=>e.rowKey==='shared-code'&&e.field==='services')
// Go omits an empty service from JSON. It must still match the existing identity.
const unbound={id:17,kind:'manual',title:'记录',reference:'步骤',status:'draft',owner:'alice'}
for(let i=0;i<4;i++)assert.deepEqual(expandDeliveryRows(deliveryRows([unbound],()=>true),'QA'),[])
const edited=deliveryRows([unbound],()=>true);edited[0].reference='修改后的步骤'
assert.equal(expandDeliveryRows(edited,'QA')[0].id,17)
assert.equal(expandDeliveryRows([{key:'new',kind:'manual',originals:[],reference:'新记录'}],'QA')[0].id,0)
console.log(JSON.stringify({ok:true,checks:['exact Nacos entry','duplicate and parent path','flattened multi-app mapping','unchanged records excluded','global errors do not invent location','code selection location']}))
