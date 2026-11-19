// Presentation preferences only. Callers explicitly allowlist fields via defaults.
// Workspace snapshots take precedence; user defaults seed new workspaces only.
const PREFIX='devops:view:v1:'
function clean(value,defaults){
 const out={}
 for(const [key,base] of Object.entries(defaults)){
  const item=['size','page_size'].includes(key)&&![20,50,100].includes(value?.[key])?undefined:value?.[key]
  out[key]=Array.isArray(base)&&Array.isArray(item)&&item.length<=100&&item.every(v=>typeof v==='string'&&v.length<=200)?[...item]:
   typeof base==='string'&&typeof item==='string'&&item.length<=500?item:
   typeof base==='boolean'&&typeof item==='boolean'?item:
   typeof base==='number'&&Number.isSafeInteger(item)&&item>=1&&item<=1000000?item:Array.isArray(base)?[...base]:base
 }
 return out
}
function read(store,key){try{const raw=store?.getItem(key);return raw&&raw.length<=8192?JSON.parse(raw):null}catch{return null}}
export function personalView({view,actor,workspace='',local,session}){
 const identity=encodeURIComponent(actor||'local'),name=encodeURIComponent(view),base=PREFIX+identity+':'+name,key=base+':'+encodeURIComponent(workspace||'standalone')
 return {
  read(defaults){const value=clean(read(session,key)||read(local,base),defaults);try{session?.setItem(key,JSON.stringify(value))}catch{}return value},
  write(value,defaults){const raw=JSON.stringify(clean(value,defaults));for(const [store,id] of [[session,key],[local,base]])try{store?.setItem(id,raw)}catch{}},
 }
}
export function browserView(view,actor){
 let local,session;try{local=window.localStorage}catch{}try{session=window.sessionStorage}catch{}
 return personalView({view,actor,workspace:window.name,local,session})
}
