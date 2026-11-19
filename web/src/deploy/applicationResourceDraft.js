export function applicationResourceDraft(value={}) {
 const composed=value.composition_version===2
 return {...value,composition_version:2,
  resource_groups:value.resource_groups||(value.resource_group?[value.resource_group]:[]),
  group_revisions:value.group_revisions||(value.resource_group?{[value.resource_group]:value.group_revision}:{}),
  requirements:composed?(value.own_requirements||[]):value.resource_group?[]:value.requirements||[],
  bindings:(composed?(value.own_bindings||[]):value.resource_group?[]:value.bindings||[]).map(b=>({...b,config:{...b.config}})),
  nacos_configs:value.nacos_configs||[]}
}

// Defaults are suggestions in a new draft only. Never guess among alternatives,
// copy connection-level schema defaults, or change an existing binding.
export function newResourceBinding(resource, bindings=[], connections=[], environments=[]){
 const used=new Set(bindings.filter(b=>b.alias===resource.alias).map(b=>b.environment))
 const remaining=environments.filter(e=>!used.has(e.name))
 const candidates=connections.filter(c=>c.kind===resource.kind)
 return {alias:resource.alias,environment:remaining.length===1?remaining[0].name:'',connection:candidates.length===1?candidates[0].name:'',config:{}}
}
