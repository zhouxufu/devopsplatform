import {safePluginReturn,loginPageURL} from './loginNavigation.js'
export {safePluginReturn} from './loginNavigation.js'
// Catch platform session expiry consistently across all console entry points.
// This guard never retries a business request, including failed mutations.
export const AUTH_REQUIRED_EVENT='platform-auth-required'
export const AUTH_RESTORED_EVENT='platform-auth-restored'
let guard
export function resetPlatformAuth(){guard?.reset()}
export function requirePlatformLogin(){guard?.requireLogin()}
export function installPlatformAuthGuard({shell=false}={}){
 if(guard)return guard
 const nativeFetch=window.fetch.bind(window)
 let expired=false,epoch=0,checking
 const loginEndpoints=new Set(['/api/login','/api/logout','/api/me'])
 const requiredResponse=()=>new Response(JSON.stringify({error:'登录已过期，请重新登录',code:'platform_login_required'}),{status:401,headers:{'Content-Type':'application/json','X-Platform-Auth-Required':'1'}})
 function requireLogin(){
  if(expired)return
  expired=true
  window.dispatchEvent(new Event(AUTH_REQUIRED_EVENT))
  if(window.parent!==window){window.parent.postMessage({type:'platform.auth.required'},location.origin);return}
  if(shell)return
  const target=safePluginReturn(location.href)
  location.replace(loginPageURL(target||'/',true))
 }
 function reset(){expired=false;epoch++;checking=undefined;window.dispatchEvent(new Event(AUTH_RESTORED_EVENT))}
 async function platformExpired(){
  checking ||= nativeFetch('/api/me',{cache:'no-store'}).then(r=>r.status===401).catch(()=>false).finally(()=>{checking=undefined})
  return checking
 }
 window.fetch=async(input,options)=>{
  let url;try{url=new URL(typeof input==='string'||input instanceof URL?input:input.url,location.href)}catch{return nativeFetch(input,options)}
  const protectedAPI=url.origin===location.origin&&url.pathname.startsWith('/api/')&&!loginEndpoints.has(url.pathname)
  if(expired&&protectedAPI)return requiredResponse()
  const requestEpoch=epoch,response=await nativeFetch(input,options)
  if(response.status!==401||url.origin!==location.origin||url.pathname==='/api/login'||url.pathname==='/api/logout')return response
  const platformRequired=response.headers.get('X-Platform-Auth-Required')==='1'||url.pathname==='/api/me'||(protectedAPI&&await platformExpired())
  if(platformRequired&&requestEpoch===epoch){requireLogin();return requiredResponse()}
  return response
 }
 window.addEventListener('message',event=>{if(event.source===window.parent&&window.parent!==window&&event.origin===location.origin&&event.data?.type==='platform.auth.restored')reset()})
 guard={reset,requireLogin};return guard
}
