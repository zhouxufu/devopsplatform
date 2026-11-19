export async function readReleaseLink(id,service,read){
 if(!/^[1-9]\d*$/.test(String(id))||!Number.isSafeInteger(Number(id)))throw Error('发布单编号无效')
 const order=await read('api/releases/'+encodeURIComponent(id))
 if(order?.service!==service||String(order.id)!==String(id))throw Error('发布单与当前应用不匹配，请返回原记录核对')
 return order
}
