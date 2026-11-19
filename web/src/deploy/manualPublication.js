export const artifactKinds={jar:'Java JAR','oci-image':'Docker 镜像','docker-image-archive':'镜像离线包','static-archive':'静态资源包'}

// The form supplies a result, never a different destination or artifact. Its
// immutable identity always comes from the reviewed release displayed to users.
export function manualPublicationPayload(order,draft,now=Date.now()){
 if(order?.status!=='waiting_manual'||!order.manual_publication?.digest||order.artifact_id!==order.manual_publication.artifact_id)throw Error('当前发布单不在待手动发布状态，请刷新后核对')
 const at=Math.floor(new Date(draft.performed_at).getTime()/1000),comment=(draft.comment||'').trim()
 if(!draft.confirmed)throw Error('请先核对本次制品摘要并确认操作完成')
 if(!['success','failed'].includes(draft.result))throw Error('请选择发布结果')
 if(!Number.isFinite(at)||at<order.started_at||at>Math.floor(now/1000)+60)throw Error('实际发布时间应在本次发布单创建后、不晚于当前时间')
 if(!comment||new TextEncoder().encode(comment).length>8000)throw Error('请填写发布结果说明，最多 8000 字节')
 return {artifact_id:order.artifact_id,digest:order.manual_publication.digest,result:draft.result,performed_at:at,comment}
}
