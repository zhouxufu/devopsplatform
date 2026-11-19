export function manualRecoveryBlocker({busy=false,comment='',confirmArtifact=false}={}){
 if(busy)return '正在记录处理结果，请稍候'
 const reasons=[]
 if(!String(comment??'').trim())reasons.push('请填写处理结果与核对依据')
 if(!confirmArtifact)reasons.push('请勾选已核对本次审核制品')
 return reasons.join('；')
}
