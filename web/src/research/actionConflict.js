// Read-only recovery: never submit/replay a source action here. Keep all user
// input, including uploaded attachment IDs, and require another explicit click.
export async function refreshActionConflict(id, draft, read, updateDetail=()=>{}) {
 draft.expected_digest=''
 const detail=await read('research/work-items/'+id)
 updateDetail(detail)
 if(!detail.remote||detail.source_identity==='public')throw new Error(detail.source_error||'请重新验证本人来源账号；填写内容已保留')
 const method=draft.action==='comment'?'edit':draft.action
 const options=await read('research/work-items/'+id+'/action-options?action='+method)
 if(!options?.digest)throw new Error('未读取到最新 Bug 版本，填写内容已保留')
 return {detail,options,form:{...draft,expected_digest:options.digest,metadata:{...draft.metadata,revision:detail.item.metadata_revision}}}
}
export const conflictRefreshedMessage='Bug 已变化，已刷新最新详情和可选项；填写内容已保留，请核对后再次确认。'
