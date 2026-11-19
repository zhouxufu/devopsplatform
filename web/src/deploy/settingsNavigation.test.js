import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {compile} from '@vue/compiler-dom'
import * as Vue from 'vue'

test('actual application settings menu clicks select every section without replacing state with the mouse event',()=>{
 const source=readFileSync(new URL('./pages/DetailView.vue',import.meta.url),'utf8')
 const menu=source.match(/<SidebarMenu\b[^>]*\baria-label="应用设置分组"[^>]*>[\s\S]*?<\/SidebarMenu>/)?.[0]
 assert.ok(menu,'settings menu must remain discoverable')
 // Execute the production template handlers; only component rendering is
 // replaced by custom elements so the test requires no browser or API calls.
 const {code}=compile(`<section :data-section="settingsSection">${menu}</section>`,{isCustomElement:()=>true})
 const render=new Function('Vue',code)(Vue)
 const context={settingsSection:'source',draft:{git_url:'https://example.invalid/unsaved.git'}}
 const draft=context.draft
 function buttons(node){
  if(!node||typeof node!=='object')return []
  return [...(node.type==='SidebarMenuButton'?[node]:[]),...(Array.isArray(node.children)?node.children.flatMap(buttons):[])]
 }
 const sections=['source','command','environment','resource','trigger','bindings','advanced']
 for(const [index,section] of sections.entries()){
  buttons(render(context,[]))[index].props.onClick({type:'click'})
  assert.equal(context.settingsSection,section)
  const view=render(context,[])
  assert.equal(view.props['data-section'],section)
  assert.deepEqual(buttons(view).map(button=>button.props['is-active']),sections.map(value=>value===section))
  assert.equal(context.draft,draft)
  assert.equal(context.draft.git_url,'https://example.invalid/unsaved.git')
 }
})
