import test from 'node:test'
import assert from 'node:assert/strict'
import {writeClipboardText} from './clipboardWrite.js'

function environment({dialog=false,command=true,throwCommand=false}={}){
 let focused,copied,selected,created=0,removed=0,owner
 const body={appendChild(el){owner='body';el.isConnected=true}}
 const modal={appendChild(el){owner='dialog';el.isConnected=true}}
 const active={isConnected:true,selectionStart:2,selectionEnd:5,selectionDirection:'backward',closest(){return dialog?modal:null},focus(){focused=this},setSelectionRange(...v){this.restored=v}}
 focused=active
 const doc={body,get activeElement(){return focused},createElement(){
  created++
  return {value:'',style:{},setAttribute(){},focus(){focused=this},select(){selected=this},setSelectionRange(){},remove(){removed++;this.isConnected=false}}
 },execCommand(name){assert.equal(name,'copy');if(throwCommand)throw Error('blocked');if(command)copied=selected?.value;return command}}
 return {doc,active,get copied(){return copied},get owner(){return owner},get created(){return created},get removed(){return removed},get focused(){return focused}}
}
test('available Clipboard API writes exact Unicode and multiline text without DOM changes',async()=>{
 const e=environment(),calls=[],text='标题\nSQL: select 1;\n令牌 lp_dev_fixture'
 assert.equal(await writeClipboardText(text,{navigator:{clipboard:{async writeText(v){calls.push(v)}}},document:e.doc}),true)
 assert.deepEqual(calls,[text]);assert.equal(e.created,0)
})
test('ordinary HTTP copies synchronously without navigator.clipboard',async()=>{
 const e=environment(),text='研发平台\n第二行\t✓'
 const result=writeClipboardText(text,{navigator:{},document:e.doc})
 assert.equal(e.copied,text) // Must occur while the click's user activation is active.
 assert.equal(await result,true)
 assert.equal(e.removed,1);assert.equal(e.focused,e.active);assert.deepEqual(e.active.restored,[2,5,'backward'])
})
test('denied modern API falls back inside the active dialog and restores focus',async()=>{
 const e=environment({dialog:true})
 const nav={clipboard:{async writeText(){throw new Error('NotAllowedError')}}}
 assert.equal(await writeClipboardText('lp_dev_fixture',{navigator:nav,document:e.doc}),true)
 assert.equal(e.owner,'dialog');assert.equal(e.copied,'lp_dev_fixture');assert.equal(e.focused,e.active);assert.equal(e.removed,1)
})
test('throwing clipboard getter still uses the HTTP-compatible path',async()=>{
 const e=environment(),nav={get clipboard(){throw Error('Permissions policy')}}
 assert.equal(await writeClipboardText('copy me',{navigator:nav,document:e.doc}),true)
 assert.equal(e.copied,'copy me')
})
test('both copying methods failing returns false and cleans up without claiming success',async()=>{
 for(const options of [{command:false},{throwCommand:true}]){
  const e=environment(options)
  assert.equal(await writeClipboardText('sensitive',{navigator:{clipboard:{async writeText(){throw Error('denied')}}},document:e.doc}),false)
  assert.equal(e.removed,1);assert.equal(e.focused,e.active);assert.equal(e.copied,undefined)
 }
})
test('null values copy as empty text and a missing document fails cleanly',async()=>{
 const e=environment()
 assert.equal(await writeClipboardText(null,{navigator:{},document:e.doc}),true)
 assert.equal(e.copied,'')
 assert.equal(await writeClipboardText('value',{navigator:{},document:null}),false)
})
test('restoration failure never changes a successful copy into failure',async()=>{
 const e=environment();e.active.focus=()=>{throw Error('dialog removed during copy')}
 assert.equal(await writeClipboardText('copied',{navigator:{},document:e.doc}),true)
 assert.equal(e.copied,'copied');assert.equal(e.removed,1)
})
