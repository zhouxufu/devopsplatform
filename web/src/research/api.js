import {formatUnixTime} from '../shared/datePresentation.js'
import { randomUUID } from '../shared/uuid.js'
export async function api(path,options={}) {
 const controller=new AbortController(),cancel=()=>controller.abort(options.signal?.reason)
 const timeout=setTimeout(()=>controller.abort(new Error(!options.method||options.method==='GET'?'读取超时，请重试':'请求超时，结果尚未确认；请先核对处理记录，不要重复提交')),path.includes('/page')?95000:60000)
 options.signal?.addEventListener('abort',cancel,{once:true});if(options.signal?.aborted)cancel()
 try{
  const response=await fetch(path.startsWith('/')?path:'api/'+path,{...options,signal:controller.signal})
  let data;try{data=await response.json()}catch{throw new Error('服务返回格式无效，请重试或检查登录状态')}
  if(!response.ok){const error=new Error(data.error||'请求失败');error.status=response.status;error.code=data.code;throw error}
  return data
 }catch(e){if(controller.signal.aborted&&!options.signal?.aborted)throw controller.signal.reason;throw e}
 finally{clearTimeout(timeout);options.signal?.removeEventListener('abort',cancel)}
}
export const post=(path,value,method='POST')=>api(path,{method,headers:{'Content-Type':'application/json'},body:JSON.stringify(value)})
export const stamp=formatUnixTime
export const operationId=()=>randomUUID().replaceAll('-','')
export function openWorkspace(plugin,hash){if(window.parent!==window)window.parent.postMessage({type:'workspace.open',plugin,hash},location.origin);else window.open('/api/plugins/'+plugin+'/ui'+hash,'_blank','noopener')}
