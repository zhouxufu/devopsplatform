// Initialize Vue's non-DOM runtime before installing the preference-only browser stub.
import 'vue'
import test from 'node:test'
import assert from 'node:assert/strict'
let instance=0
async function harness({stored={},embedded=false,denied=false}={}){
 const values=new Map(Object.entries(stored)),events=new Map(),systemEvents=new Map(),messages=[],styles={}
 const storage={getItem:key=>{if(denied)throw Error('blocked');return values.get(key)||null},setItem:(key,value)=>{if(denied)throw Error('blocked');values.set(key,value)}}
 const root={dataset:{},style:{setProperty(k,v){styles[k]=v},removeProperty(k){delete styles[k]}},classList:{toggle(name,value){this[name]=value}}},body={dataset:{},style:{setProperty(){}}}
 const system={matches:false,addEventListener:(type,fn)=>systemEvents.set(type,fn)},win={matchMedia:()=>system,addEventListener:(type,fn)=>events.set(type,fn)}
 win.parent=embedded?{}:win
 Object.assign(globalThis,{window:win,document:{documentElement:root,body,querySelectorAll:()=>[{contentWindow:{postMessage:(...args)=>messages.push(args)}}]},location:{origin:'http://fixture.invalid'},localStorage:storage})
 const theme=await import(`./theme.js?test=${++instance}`);theme.initTheme()
 return {theme,root,body,styles,storage,values,events,system,systemEvents,win,messages}
}
test('existing accent choices migrate to a coordinated design without changing mode or type',async()=>{
 for(const [legacy,design,colorway] of [['neutral','default','neutral'],['blue','layered','blue'],['violet','layered','indigo']]){
  const {theme,root}=await harness({stored:{'lp-theme-palette':legacy,'lp-theme':'dark','lp-theme-typography':'large'}})
  assert.equal(theme.themeDesign.value,design);assert.equal(theme.themeColorway.value,colorway)
  assert.equal(root.dataset.consoleTypography,'large');assert.equal(theme.themeDark.value,true)
 }
})
test('a design change selects only its own colorway and leaves density/type/mode independent',async()=>{
 const {theme,root,values,styles,messages}=await harness()
 theme.setTheme({design:'layered',colorway:'indigo',density:'compact',typography:'large',mode:'dark'})
 theme.setTheme({design:'soft'})
 assert.equal(root.dataset.consoleDesign,'soft');assert.equal(root.dataset.consoleColorway,'sage')
 assert.equal(root.dataset.consoleDensity,'compact');assert.equal(root.dataset.consoleTypography,'large');assert.equal(theme.themeDark.value,true)
 const sage=styles['--console-canvas'];theme.setTheme({colorway:'sand'})
 assert.notEqual(styles['--console-canvas'],sage);assert.equal(styles['--sidebar'],'#2b241d')
 theme.setTheme({mode:'light'});theme.setTheme({typography:'small'});theme.setTheme({density:'standard'})
 assert.deepEqual(JSON.parse(values.get('lp-theme-appearance')),{design:'soft',colorway:'sand'})
 assert.deepEqual(messages.at(-1),[{type:'theme.sync',dark:false,mode:'light',typography:'small',appearance:{design:'soft',colorway:'sand'},density:'standard'},'http://fixture.invalid'])
 theme.setTheme({colorway:'violet'});assert.equal(theme.themeColorway.value,'sage')
 const reload=await harness({stored:Object.fromEntries(values)});assert.equal(reload.theme.themeDesign.value,'soft');assert.equal(reload.theme.themeColorway.value,'sage')
})
test('new appearance preferences take precedence over legacy and malformed storage fails safely',async()=>{
 const {theme}=await harness({stored:{'lp-theme-appearance':'{"design":"soft","colorway":"sand"}','lp-theme-palette':'violet'}})
 assert.equal(theme.themeColorway.value,'sand')
 const malformed=await harness({stored:{'lp-theme-appearance':'{bad json','lp-theme-palette':'violet'}})
 assert.equal(malformed.theme.themeDesign.value,'default')
})
test('embedded synchronization trusts only the same-origin parent and older payloads retain design',async()=>{
 const {theme,root,events,win}=await harness({embedded:true})
 const send=(source,origin,data)=>events.get('message')({source,origin,data})
 const payload={type:'theme.sync',mode:'dark',appearance:{design:'soft',colorway:'sand'},typography:'small',density:'compact'}
 send({},'http://fixture.invalid',payload);send(win.parent,'https://untrusted.invalid',payload)
 assert.equal(root.dataset.consoleDesign,'default');assert.equal(theme.themeDark.value,false)
 send(win.parent,'http://fixture.invalid',payload)
 assert.equal(root.dataset.consoleColorway,'sand');assert.equal(root.dataset.consoleDensity,'compact');assert.equal(theme.themeDark.value,true)
 send(win.parent,'http://fixture.invalid',{type:'theme.sync',dark:false,palette:'violet'})
 assert.equal(root.dataset.consoleColorway,'sand');assert.equal(theme.themeDark.value,false)
 send(win.parent,'http://fixture.invalid',{type:'theme.sync',appearance:{design:'soft',colorway:'../../other.css'}})
 assert.equal(root.dataset.consoleColorway,'sage')
})
test('cross-tab changes are atomic for design/colorway and system mode keeps the design',async()=>{
 const {theme,storage,events,system,systemEvents,root}=await harness()
 storage.setItem('lp-theme-appearance','{"design":"soft","colorway":"sand"}')
 events.get('storage')({key:'lp-theme-appearance',storageArea:storage});assert.equal(root.dataset.consoleColorway,'sand')
 theme.setTheme({mode:'system'});system.matches=true;systemEvents.get('change')()
 assert.equal(theme.themeDark.value,true);assert.equal(root.dataset.consoleColorway,'sand')
 storage.setItem('lp-theme-appearance','{"design":"layered","colorway":"indigo"}')
 events.get('storage')({key:'lp-theme-appearance',storageArea:{}});assert.equal(root.dataset.consoleColorway,'sand')
 events.get('storage')({key:null,storageArea:storage});assert.equal(root.dataset.consoleColorway,'indigo')
})
test('blocked preference storage allows in-memory selection and toggling keeps the colorway',async()=>{
 const {theme,root}=await harness({denied:true})
 assert.doesNotThrow(()=>theme.setTheme({design:'soft',colorway:'sand',mode:'dark',density:'compact'}))
 theme.toggleTheme();assert.equal(root.dataset.consoleColorway,'sand');assert.equal(root.dataset.consoleDensity,'compact');assert.equal(theme.themeDark.value,false)
})

test('returning to the original theme clears all curated tokens and persists without resetting display settings',async()=>{
 const {theme,styles,root,values,messages}=await harness()
 assert.equal(theme.themeDesign.value,'default');assert.equal(theme.themeColorway.value,'neutral')
 theme.setTheme({design:'soft',colorway:'sand',typography:'large',density:'compact',mode:'dark'})
 assert.equal(styles['--sidebar'],'#2b241d')
 theme.setTheme({design:'default'})
 for(const key of ['--sidebar','--primary','--console-canvas','--radius','--chart-1'])assert.equal(styles[key],undefined)
 assert.equal(root.dataset.consoleDesign,'default');assert.equal(root.dataset.consoleColorway,'neutral')
 assert.equal(root.dataset.consoleTypography,'large');assert.equal(root.dataset.consoleDensity,'compact');assert.equal(theme.themeDark.value,true)
 assert.deepEqual(JSON.parse(values.get('lp-theme-appearance')),{design:'default',colorway:'neutral'})
 assert.deepEqual(messages.at(-1)[0].appearance,{design:'default',colorway:'neutral'})
 theme.toggleTheme();assert.equal(styles['--primary'],undefined)
 const reload=await harness({stored:Object.fromEntries(values)})
 assert.equal(reload.theme.themeDesign.value,'default');assert.equal(reload.styles['--primary'],undefined)
})
