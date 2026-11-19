export const evidenceAPI='/api/plugins/deploy/api/'
export async function bugAPI(path,input,{fetcher=fetch,timeoutMS=60000}={}){
 const controller=new AbortController(),writing=path==='bug-evidence/link'
 const timer=setTimeout(()=>controller.abort(new Error(writing?'关联操作超时，结果尚未确认；请保留当前填写内容并核对 Bug，不要重新建单':'证据读取超时，请重试')),timeoutMS)
 try{
  const r=await fetcher(evidenceAPI+path,{signal:controller.signal,...(input===undefined?{}:{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(input)})})
  let data;try{data=await r.json()}catch{throw Error(writing?'服务响应格式错误，关联结果尚未确认；请核对 Bug 后再操作':'证据响应格式错误，请重试')}
  if(!r.ok)throw Error(data?.error||'证据操作失败')
  if(!data||typeof data!=='object'||(writing&&(!Number.isSafeInteger(data.id)||data.id<1||!Number.isSafeInteger(data.work_item_id)||data.work_item_id<1)))throw Error(writing?'关联响应缺少记录编号，结果尚未确认；请核对 Bug 后再操作':'证据响应格式无效')
  return data
 }catch(e){if(controller.signal.aborted)throw controller.signal.reason;throw e}finally{clearTimeout(timer)}
}
export function evidenceWorkspace(plugin,hash){if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin,hash,context:evidenceContext(hash)},location.origin);else window.open('/api/plugins/'+plugin+'/ui'+hash,'_blank','noopener')}
export function evidenceLogInput(hit,source=''){return {kind:'log',log:{service:hit.service||'',environment:hit.environment||'',source,host:hit.host||'',timestamp:Number(hit.ts),trace_id:hit.trace_id||'',excerpt:hit.message||''}}}
export function evidenceLogHash(e){const log=e.log,from=Math.max(1,log.timestamp-60000),to=log.timestamp+60000;return '#/search?'+new URLSearchParams({evidence:'1',service:e.service||log.service,environment:log.environment,from:String(from),to:String(to),log_source:log.source||'',host:log.host||'',trace_id:log.trace_id||''})}

export function evidenceContext(hash){const q=new URL(hash.slice(1),'http://workspace').searchParams,context={};for(const k of ['service','environment'])if(q.has(k))context[k]=q.get(k);const from=Number(q.get('from')),to=Number(q.get('to'));if(from>0&&to>from&&to-from<=86400000)context.range=[from,to];return context}
