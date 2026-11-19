export const sourceActionLabel=action=>({comment:'发表评论',edit:'编辑工作项',resolve:'解决工作项',activate:'激活工作项',close:'关闭工作项',assignTo:'指派工作项',develop:'标记开发完成'})[action]||'来源操作'
export function inboxAttention(view,attention){return view==='owned'?attention:view==='done'?'all':'active'}
export function safeSourceUrl(value){try{const u=new URL(value);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:''}catch{return ''}}
export async function verifySourceOperation(op,send){
 const result=await send('research/operations/'+encodeURIComponent(op.id)+'/reconcile',{})
 if(result?.id!==op.id)throw Error('核对结果与原操作不一致，请刷新原记录')
 return {operation:result,verified:result.state==='success',message:result.state==='success'?'已核实来源操作，待核对记录已清除':result.message||'仍未找到匹配的来源历史；记录已保留，请前往来源平台查看'}
}
