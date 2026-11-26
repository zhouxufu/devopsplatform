import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {parse,compile} from '@vue/compiler-dom'
import {parse as parseSFC} from '@vue/compiler-sfc'
import * as Vue from 'vue'
import {cleanTablePreferences,defaultTablePreferences} from './tablePreferences.js'

const source=file=>readFileSync(new URL(file,import.meta.url),'utf8').replaceAll('\r\n','\n')
function nodes(file,tag){
 const root=parse(parseSFC(source(file)).descriptor.template.content)
 const found=[]
 function walk(node){if(node.tag===tag)found.push(node);for(const child of node.children||[])walk(child)}
 walk(root);return found
}
const handler=(node,event)=>node.props.find(p=>p.name==='on'&&p.arg?.content===event)?.exp.content
function execute(expression,event,context){
 const {code}=compile(`<button @click="${expression.replaceAll('"','&quot;')}"/>`)
 const render=new Function('Vue',code)(Vue)
 render(context,[]).props.onClick(event)
}
test('delivery record menu actions emit the original intent exactly once',()=>{
 const file='../deploy/components/DeliveryRecordsTable.vue',events=[]
 const emit=(...args)=>events.push(args),item={id:42,status:'draft'}
 const commandBody=source(file).match(/function command\(op,item\)\{([^\n]+)\}\n/)?.[1]
 assert.ok(commandBody)
 const command=new Function('emit',`return (op,item)=>{${commandBody}}`)(emit)
 const expected=[['view',item],['edit',item],['deliver',item],['version',item],['preview',item],['action',{item,op:'ready'}],['action',{item,op:'reopen'}],['action',{item,op:'delete'}],['view',item]]
 const menu=nodes(file,'DropdownMenuItem')
 assert.equal(menu.length,expected.length)
 for(const [index,node] of menu.entries()){
  execute(handler(node,'select'),{type:'select'},{item,emit,command})
  assert.deepEqual(events,[expected[index]])
  events.length=0
 }
})
test('checkbox value updates support selection, deselection and Docker readonly mounts',()=>{
 for(const file of ['../deploy/components/DockerRuntimeFields.vue','../deploy/components/DeliverySourcePicker.vue','../deploy/pages/OrchestrationView.vue']){
  const callbacks=nodes(file,'Checkbox').map(n=>handler(n,'update:model-value')).filter(e=>e&&(/read_only|toggle\(/.test(e)||/toggleChange/.test(e)))
  assert.ok(callbacks.length,file)
  for(const expression of callbacks){
   const calls=[],context={i:0,row:{id:7},c:{id:7},rows:[{id:7},{id:8}],update:(...args)=>calls.push(args),toggle:(...args)=>calls.push(args),toggleChange:(...args)=>calls.push(args)}
   for(const checked of [true,false]){
    execute(expression,checked,context)
    assert.ok(calls.length)
    assert.ok(calls.every(args=>args.at(-1)===checked),file+': '+expression)
    calls.length=0
   }
  }
 }
})
test('version columns fit common desktop panes with full metadata and a protected action width',()=>{
 const node=nodes('../deploy/components/DeliveryRecordsTable.vue','DataTable')[0]
 const expression=node.props.find(p=>p.name==='bind'&&p.arg?.content==='columns').exp.content
 const columns=new Function('isVersion',`return ${expression}`)(true).filter(c=>c.enabled!==false)
 assert.equal(columns.length,6)
 assert.deepEqual(defaultTablePreferences(columns).hidden,[])
 assert.ok(columns.reduce((sum,c)=>sum+c.size,0)<=990)
 const next=cleanTablePreferences({widths:['c2=64','c4=80','c8=64']},columns)
 assert.deepEqual(next.widths,['c2=112','c4=240','c8=176'])
})
