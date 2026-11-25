const clean=value=>String(value||'').replace(/(?:<br\s*\/?>)?\s*\[delivery-operation:[a-f0-9]{32}\]/gi,'').trim()
const sourceKind=kind=>({resolve:'resolved',activate:'activated',close:'closed',assignto:'assigned',comment:'edited',edit:'edited'}[String(kind||'').toLowerCase()])
const canonicalSource=kind=>({resolve:'resolved',activate:'activated',close:'closed',assignto:'assigned',comment:'edited',commented:'edited',edit:'edited'}[String(kind||'').toLowerCase()]||String(kind||'').toLowerCase())

// A local audit and verified source action describe one operation. Keep the
// source fields/HTML, while retaining every genuinely distinct source action.
export function mergeActivityRecords(activities=[],remote={},people={}){
 const matched=new Set(),locals=activities||[]
 const sources=Object.entries(remote?.actions||{}).filter(([,a])=>a&&typeof a==='object').map(([key,a])=>{
  const markers=[...String(a.comment||'').matchAll(/\[delivery-operation:([a-f0-9]{32})\]/gi)].map(m=>m[1].toLowerCase())
  const matches=locals.filter(local=>local.operation_id&&markers.includes(local.operation_id)&&local.source_actor===a.actor&&sourceKind(local.kind)===canonicalSource(a.action))
  const local=matches.length===1?matches[0]:null
  if(local)matched.add(local)
  return {key:'source-'+key,actor:remote.users?.[a.actor]||a.actor,kind:local?.kind||a.action,content:clean(a.comment),format:'html',date:a.date,source:true,history:Object.values(a.history||{}).filter(h=>h&&typeof h==='object')}
 })
 return [...locals.filter(a=>!matched.has(a)).map(a=>({key:'local-'+a.id,actor:people[a.actor]||remote?.users?.[a.source_actor]||a.actor,kind:a.kind,content:a.content||'',format:a.kind==='comment'?'markdown':'text',date:a.at,source:false,history:[]})),...sources]
}

export function localActivityContent(activity){
 if(activity.kind==='release-mark'){let value;try{value=JSON.parse(activity.content)}catch{return '人工上线记录'};return (value?.marked===true?'人工确认已上线':value?.marked===false?'撤销了已上线标记':'人工上线记录')+(typeof value?.comment==='string'&&value.comment?'：'+value.comment:'')}
 if(['work-item-updated','work-item-status','work-item-assigned'].includes(activity.kind)){
  let changes;try{changes=JSON.parse(activity.content)}catch{return '工作项信息已更新'}
  if(!changes||typeof changes!=='object'||Array.isArray(changes))return '工作项信息已更新'
  const labels={status:'状态',assigned_to:'负责人',title:'标题',description:'说明',priority:'优先级',severity:'严重程度',resolution:'解决方案'}
  const status=value=>({open:'待处理',active:'处理中',resolved:'已解决',closed:'已关闭'})[value]||value||'未设置'
  if(activity.kind==='work-item-status'&&changes.status)return `状态：${status(changes.status[0])} → ${status(changes.status[1])}`
  if(activity.kind==='work-item-assigned'&&changes.assigned_to)return `负责人：${changes.assigned_to[0]||'未指派'} → ${changes.assigned_to[1]||'未指派'}`
  return '更新了：'+Object.keys(changes).map(key=>labels[key]||key).join('、')
 }
 if(!['release-intent','delivery-ownership','change-created','change-updated','change-ready'].includes(activity.kind))return activity.content||''
 let value;try{value=JSON.parse(activity.content)}catch{return '详情暂不可读，可展开查看原始记录'}
 if(!value||typeof value!=='object'||Array.isArray(value))return '详情暂不可读，可展开查看原始记录'
 if(activity.kind==='release-intent')return value.release_required===true?'需要上线':value.release_required===false?'不需要上线'+(value.no_release_reason?'：'+value.no_release_reason:''):'上线意图待确认'
 const kind={code:'代码',mysql:'SQL',nacos:'Nacos 配置',redis:'Redis',manual:'自定义记录',artifact:'制品','data-sync':'数据同步'}[value.kind]||'变更'
 const name=`${kind}${value.change_id?' #'+value.change_id:''}${value.title?' · '+value.title:''}`
 if(activity.kind==='delivery-ownership')return name+'：'+({self:'由当前工作项交付',draft:'生产归属待确认',follow:'随工作项 #'+value.work_item_id+' 交付'}[value.mode]||'归属已更新')
 return name+' · '+(value.status==='ready'?'已校验并就绪':'草稿')
}
