import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync,readdirSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import path from 'node:path'
import {parse} from '@vue/compiler-sfc'
const root=fileURLToPath(new URL('../',import.meta.url))
function nodes(file){return parse(readFileSync(file,'utf8')).descriptor.template?.ast}
function all(node,predicate,result=[]){if(predicate(node))result.push(node);for(const child of node.children||[])all(child,predicate,result);return result}
const attr=(node,key)=>node.props?.find(p=>p.name===key)?.value?.content
function files(dir){return readdirSync(dir,{withFileTypes:true}).flatMap(item=>item.isDirectory()?files(path.join(dir,item.name)):item.name.endsWith('.vue')?[path.join(dir,item.name)]:[])}
test('dialog action areas remain outside their scrolling body throughout built-in pages',()=>{
 for(const file of files(root)){
  if(file.includes('/components/ui/'))continue
  const ast=nodes(file);if(!ast)continue
  for(const dialog of all(ast,n=>['DialogContent','SheetContent'].includes(n.tag))){
   function visit(node,scrolling=false){
    assert(!(scrolling&&['DialogFooter','SheetFooter'].includes(node.tag)),`${file}: actions scroll with body`)
    const scroll=scrolling||/\boverflow-(?:auto|scroll|y-auto)\b/.test(attr(node,'class')||'')
    for(const child of node.children||[])visit(child,scroll)
   }
   for(const child of dialog.children||[])visit(child)
  }
 }
})
test('moving change-editor actions preserves the native submit target and validation handler',()=>{
 const ast=nodes(path.join(root,'deploy/pages/ChangesView.vue'))
 const form=all(ast,n=>n.tag==='form'&&attr(n,'id')==='change-editor-form')[0]
 assert(form,'change editor form exists')
 assert(form.props.some(p=>p.name==='on'&&p.arg?.content==='submit'&&p.exp?.content==='save'))
 const submit=all(ast,n=>n.tag==='Button'&&attr(n,'type')==='submit'&&attr(n,'form')==='change-editor-form')
 assert.equal(submit.length,1)
 const footer=all(ast,n=>n.tag==='DialogFooter'&&all(n,c=>c===submit[0]).length)[0]
 assert(footer,'save remains in the external footer, associated with the original form')
 assert(submit[0].props.some(p=>p.name==='bind'&&p.arg?.content==='disabled'),'submission guards stay in place')
})
