export async function searchLogs(source,params,signal){
 if(!source){const response=await fetch('api/search?'+params,{signal}),data=await response.json().catch(()=>({}));if(!response.ok)throw new Error(data.error||'日志查询失败');return data}
 const input={service:params.get('service')||'',environment:params.get('environment')||'',host:params.get('host')||'',levels:(params.get('level')||'').split(',').filter(Boolean),text:params.get('q')||'',start:Number(params.get('start')),end:Number(params.get('end')),limit:100,cursor:params.get('cursor')||''}
 const response=await fetch('/api/extensions/'+encodeURIComponent(source)+'/invoke',{method:'POST',signal,headers:{'Content-Type':'application/json'},body:JSON.stringify({capability:'logs.search.v1',operation:'search',input})}),result=await response.json().catch(()=>({}));
 if(!response.ok||result.error)throw new Error(typeof result.error==='object'?result.error.message:result.error||'数据源查询失败');
 if(!result.data||!Array.isArray(result.data.hits))throw new Error('数据源响应格式错误');return result.data
}
