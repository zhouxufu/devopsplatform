// Preparation describes current content only; it is never execution or release evidence.
export function deliveryPreparation(summary,{loading=false,error=''}={}){
 if(error)return {state:'error',label:'读取失败',total:null,ready:null}
 if(loading||!summary)return {state:'loading',label:'正在读取',total:null,ready:null}
 if(!Array.isArray(summary.changes))return {state:'error',label:'读取失败',total:null,ready:null}
 const total=summary.changes.length,ready=summary.changes.filter(c=>c.status==='ready').length
 return total===0?{state:'empty',label:'未录入变更',total,ready}:ready===total?{state:'ready',label:'内容已就绪',total,ready}:{state:'draft',label:'内容待完善',total,ready}
}
