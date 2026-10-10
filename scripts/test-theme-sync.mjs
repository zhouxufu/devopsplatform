// Unit regression for source validation, reactive state and unavailable storage.
import assert from 'node:assert/strict';
// Load Vue before the deliberately minimal DOM stub below.
await import('../web/node_modules/vue/index.mjs');
const values=new Map([['lp-theme','light']]),listeners=new Map(),classes=new Set();
globalThis.localStorage={getItem:k=>values.get(k)??null,setItem:(k,v)=>values.set(k,v)};
globalThis.location={origin:'http://127.0.0.1:9800'};
globalThis.document={documentElement:{classList:{toggle:(k,on)=>on?classes.add(k):classes.delete(k)},style:{}}};
const parent={};globalThis.window={parent,addEventListener:(type,listener)=>{listeners.set(type,[...(listeners.get(type)||[]),listener])}};
const {initTheme,toggleTheme,isDark,themeDark}=await import('../web/src/shared/composables/theme.js');
const dispatch=(type,event)=>listeners.get(type)?.forEach(fn=>fn(event));
initTheme();initTheme();assert.equal(listeners.get('message').length,1);assert.equal(listeners.get('storage').length,1);assert.equal(isDark(),false);
dispatch('message',{source:parent,origin:location.origin,data:{type:'theme.sync',dark:true}});assert.equal(isDark(),true);assert(classes.has('dark'));assert.equal(document.documentElement.style.colorScheme,'dark');assert.equal(values.get('lp-theme'),'light','theme messages must not rewrite preference');
for(const event of [{source:{},origin:location.origin,data:{type:'theme.sync',dark:false}},{source:parent,origin:'http://untrusted.invalid',data:{type:'theme.sync',dark:false}},{source:parent,origin:location.origin,data:{type:'theme.sync',dark:'false'}},{source:parent,origin:location.origin,data:{type:'other',dark:false}}])dispatch('message',event);
assert.equal(isDark(),true,'untrusted or malformed messages changed theme');
assert.equal(toggleTheme(),false);assert.equal(values.get('lp-theme'),'light');assert.equal(themeDark.value,false);
values.set('lp-theme','dark');dispatch('storage',{key:'lp-theme',storageArea:localStorage});assert.equal(themeDark.value,true);
values.set('lp-theme','light');dispatch('storage',{key:'different',storageArea:localStorage});dispatch('storage',{key:'lp-theme',storageArea:{}});assert.equal(themeDark.value,true);
dispatch('storage',{key:'lp-theme',storageArea:localStorage});assert.equal(isDark(),false);
values.clear();dispatch('storage',{key:null,storageArea:localStorage});assert.equal(isDark(),true,'cleared storage must use default theme');
localStorage.getItem=()=>{throw Error('blocked')};localStorage.setItem=()=>{throw Error('blocked')};initTheme();assert.equal(toggleTheme(),false);assert.equal(isDark(),false);
window.parent=window;dispatch('message',{source:window,origin:location.origin,data:{type:'theme.sync',dark:true}});assert.equal(isDark(),false);
console.log('PASS: parent-only same-origin boolean messages, storage sync, reactive state, idempotent initialization and unavailable storage');
