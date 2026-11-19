export function environmentHostOptions(hosts=[],environment=''){
 return hosts.filter(h=>h.environment===environment).map(h=>({value:h.name,label:h.name+' · '+h.addr}))
}
export function selectedDeploymentHosts(form,mode,groups=[],hosts=[]){
 if(['aliyun-sae','manual','script'].includes(form.target_type))return {host_group:'',hosts:[]}
 if(mode==='group'){
  const group=groups.find(g=>g.name===form.host_group&&g.environment===form.environment)
  if(!group?.hosts?.length)throw Error('请选择该环境下非空的主机组')
  return {host_group:group.name,hosts:[...group.hosts]}
 }
 const selected=[...(form.hosts||[])]
 if(!selected.length)throw Error('请选择至少一台发布主机')
 if(new Set(selected).size!==selected.length)throw Error('发布主机不能重复')
 if(selected.some(name=>!hosts.some(h=>h.name===name&&h.environment===form.environment)))throw Error('所选主机已失效或不属于当前环境，请重新选择')
 return {host_group:'',hosts:selected}
}
