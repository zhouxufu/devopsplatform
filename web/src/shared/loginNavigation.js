// Authentication return targets never leave this platform or enter an arbitrary API.
export function safePluginReturn(value,origin=location.origin){
 try{const url=new URL(value,origin);return url.origin===origin&&/^\/api\/plugins\/[a-zA-Z0-9_-]+\/ui\/?$/.test(url.pathname)?url.pathname+url.search+url.hash:''}catch{return ''}
}
export function safeLoginReturn(value,origin=location.origin){
 if(!value)return '/'
 try{const url=new URL(value,origin);if(url.origin===origin&&url.pathname==='/'&&!url.search)return '/'+url.hash}catch{}
 return safePluginReturn(value,origin)||'/'
}
export function loginPageURL(target='/',expired=false,origin=location.origin){
 const query=new URLSearchParams()
 const safe=safeLoginReturn(target,origin)
 if(safe!=='/')query.set('return_to',safe)
 if(expired)query.set('reason','expired')
 return '/login'+(query.size?'?'+query:'')
}
export function requestedLoginReturn(search,hash='',origin=location.origin){
 const value=new URLSearchParams(search).get('return_to')||''
 // HTTP redirects cannot capture a plugin's fragment; the browser carries it forward.
 return safeLoginReturn(value&&hash&&!value.includes('#')?value+hash:value,origin)
}
