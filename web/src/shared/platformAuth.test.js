import test from 'node:test'
import assert from 'node:assert/strict'

let sequence=0
async function fixture(t,{shell=false,embedded=false,fetcher=async()=>new Response('',{status:200})}={}){
 const previous={window:globalThis.window,location:globalThis.location}
 const messages=[],navigations=[],events=[],calls=[]
 const host=new EventTarget()
 host.location={origin:'https://console.example',href:'https://console.example/api/plugins/research/ui#/work/42',replace:value=>navigations.push(value)}
 host.parent=embedded?{postMessage:(...args)=>messages.push(args)}:host
 host.fetch=async(...args)=>{calls.push(args);return fetcher(...args)}
 globalThis.window=host;globalThis.location=host.location
 t.after(()=>{for(const [key,value] of Object.entries(previous)){if(value===undefined)delete globalThis[key];else globalThis[key]=value}})
 const auth=await import(`./platformAuth.js?test=${++sequence}`)
 host.addEventListener(auth.AUTH_REQUIRED_EVENT,()=>events.push('required'))
 host.addEventListener(auth.AUTH_RESTORED_EVENT,()=>events.push('restored'))
 const guard=auth.installPlatformAuthGuard({shell})
 return {host,auth,guard,messages,navigations,events,calls}
}
const expired=()=>new Response('{}',{status:401,headers:{'X-Platform-Auth-Required':'1'}})

test('a shell redirected into a workspace delegates login once without rendering or navigating there',async t=>{
 const f=await fixture(t,{shell:true,embedded:true})
 f.guard.requireLogin();f.guard.requireLogin()
 assert.deepEqual(f.messages,[[{type:'platform.auth.required'},'https://console.example']])
 assert.deepEqual(f.events,['required']);assert.deepEqual(f.navigations,[])
 assert.equal((await f.host.fetch('/api/plugins')).status,401)
 assert.equal(f.calls.length,0)
})

test('concurrent expiry blocks further business writes and login never replays them',async t=>{
 const f=await fixture(t,{shell:true,fetcher:async()=>expired()})
 await Promise.all([f.host.fetch('/api/changes',{method:'POST'}),f.host.fetch('/api/plugins')])
 assert.deepEqual(f.events,['required']);assert.deepEqual(f.navigations,[])
 const count=f.calls.length
 assert.equal((await f.host.fetch('/api/changes',{method:'POST'})).status,401)
 assert.equal(f.calls.length,count)
 await f.host.fetch('/api/login',{method:'POST'})
 f.auth.resetPlatformAuth()
 assert.equal(f.calls.length,count+1)
 assert.deepEqual(f.events,['required','restored'])
})

test('source authorization errors do not expire the platform session',async t=>{
 const f=await fixture(t,{fetcher:async url=>url==='/api/me'?new Response('{}'):new Response('{}',{status:401})})
 assert.equal((await f.host.fetch('/api/source')).status,401)
 assert.deepEqual(f.events,[]);assert.deepEqual(f.navigations,[])
 assert.deepEqual(f.calls.map(([url])=>url),['/api/source','/api/me'])
})

test('forbidden responses and an unavailable session probe do not redirect to login',async t=>{
 const f=await fixture(t,{fetcher:async url=>{
  if(url==='/api/me')throw new Error('offline')
  return new Response('{}',{status:url==='/api/forbidden'?403:401})
 }})
 assert.equal((await f.host.fetch('/api/forbidden')).status,403)
 assert.equal(f.calls.length,1)
 assert.equal((await f.host.fetch('/api/source')).status,401)
 assert.deepEqual(f.events,[]);assert.deepEqual(f.navigations,[])
})

test('an old in-flight 401 cannot expire a successfully restored session',async t=>{
 let complete
 const f=await fixture(t,{shell:true,fetcher:()=>new Promise(resolve=>{complete=resolve})})
 const request=f.host.fetch('/api/changes')
 f.guard.requireLogin();f.auth.resetPlatformAuth();complete(expired())
 await request
 assert.deepEqual(f.events,['required','restored'])
 assert.deepEqual(f.navigations,[])
})

test('standalone plugins navigate to the dedicated login page with their exact safe return route',async t=>{
 const f=await fixture(t,{fetcher:async()=>expired()})
 await f.host.fetch('/api/me')
 const url=new URL(f.navigations[0],f.host.location.origin)
 assert.equal(url.pathname,'/login');assert.equal(url.searchParams.get('reason'),'expired')
 assert.equal(url.searchParams.get('return_to'),'/api/plugins/research/ui#/work/42')
 assert.deepEqual(f.messages,[])
})

test('only the owning same-origin console may restore an expired embedded plugin',async t=>{
 const f=await fixture(t,{embedded:true})
 f.guard.requireLogin()
 const message=(origin,source)=>{const event=new Event('message');Object.assign(event,{origin,source,data:{type:'platform.auth.restored'}});f.host.dispatchEvent(event)}
 message('https://evil.example',f.host.parent);message(f.host.location.origin,{})
 assert.deepEqual(f.events,['required'])
 message(f.host.location.origin,f.host.parent)
 assert.deepEqual(f.events,['required','restored'])
 assert.equal((await f.host.fetch('/api/plugins')).status,200)
})
