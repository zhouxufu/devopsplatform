// Exercise the production neutral-theme state machine without browser writes.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import vm from 'node:vm'
import * as typography from '../web/src/shared/typography.js'
const values=new Map([['lp-theme','light'],['lp-theme-preset','mengtu']]),listeners=new Map(),classes=new Set(),sent=[]
const style=()=>({values:new Map(),setProperty(k,v){this.values.set(k,v)}})
const element=()=>({dataset:{},style:style(),classList:{toggle:(k,on)=>on?classes.add(k):classes.delete(k)}})
const parent={},storage={getItem:k=>values.get(k)??null,setItem:(k,v)=>values.set(k,v)}
const system={matches:false,addEventListener:(type,fn)=>listeners.set('system',[fn])}
const document={documentElement:element(),body:element(),querySelectorAll:()=>[{contentWindow:{postMessage:(data,origin)=>sent.push({data,origin})}}]}
const window={parent,matchMedia:()=>system,addEventListener:(type,fn)=>listeners.set(type,[...(listeners.get(type)||[]),fn])}
const context=vm.createContext({document,window,localStorage:storage,location:{origin:'http://fixture'},console})
const module=new vm.SourceTextModule(await readFile(new URL('../web/src/shared/composables/theme.js',import.meta.url),'utf8'),{context})
await module.link(specifier=>{const exports=specifier==='vue'?{ref:value=>({value})}:typography;return new vm.SyntheticModule(Object.keys(exports),function(){for(const [key,value] of Object.entries(exports))this.setExport(key,value)},{context})})
await module.evaluate()
const {initTheme,toggleTheme,isDark,themeMode,themeTypography,setTheme}=module.namespace
const dispatch=(type,event)=>listeners.get(type)?.forEach(fn=>fn(event))
const message=data=>dispatch('message',{source:parent,origin:'http://fixture',data:{type:'theme.sync',...data}})
initTheme();initTheme()
assert.equal(listeners.get('message').length,1);assert.equal(listeners.get('storage').length,1)
assert.equal(isDark(),false);assert.equal(document.documentElement.dataset.consoleTheme,'shadcn')
message({dark:true});assert.equal(isDark(),true);assert(classes.has('dark'));assert.equal(values.get('lp-theme'),'light','messages must not rewrite saved preference')
for(const event of [{source:{},origin:'http://fixture',data:{type:'theme.sync',dark:false}},{source:parent,origin:'http://untrusted.invalid',data:{type:'theme.sync',dark:false}},{source:parent,origin:'http://fixture',data:{type:'other',dark:false}},{source:parent,origin:'http://fixture',data:{type:'theme.sync',dark:'false',preset:'https://untrusted/theme.css'}}])dispatch('message',event)
assert.equal(isDark(),true)
assert.equal(toggleTheme(),false)
setTheme({mode:'dark',typography:'large'});assert.equal(themeTypography.value,'large');assert.equal(document.body.style.values.get('--font-size-body-3'),'18px')
assert.equal(values.get('lp-theme-preset'),'mengtu','migration does not overwrite retired preferences')
assert.equal(document.body.style.values.has('--primary-6'),false,'no old palette can override official neutral tokens')
values.set('lp-theme','light');dispatch('storage',{key:'unrelated',storageArea:storage});dispatch('storage',{key:'lp-theme',storageArea:{}});assert.equal(isDark(),true)
dispatch('storage',{key:'lp-theme',storageArea:storage});assert.equal(isDark(),false)
values.clear();dispatch('storage',{key:null,storageArea:storage});assert.equal(isDark(),false)
setTheme({mode:'system'});system.matches=true;dispatch('system',{});assert.equal(isDark(),true);assert.equal(themeMode.value,'system')
setTheme({mode:'light'});system.matches=false;dispatch('system',{});assert.equal(isDark(),false)
storage.getItem=()=>{throw Error('blocked')};storage.setItem=()=>{throw Error('blocked')};setTheme({mode:'dark'});assert.equal(isDark(),true)
window.parent=window;dispatch('message',{source:window,origin:'http://fixture',data:{type:'theme.sync',dark:false}});assert.equal(isDark(),true)
assert(sent.every(v=>v.origin==='http://fixture'));assert(sent.every(v=>!('preset' in v.data)))
console.log('PASS: official neutral tokens, mode/font sync, same-origin parent validation, storage/system preferences, no legacy palette injection')
