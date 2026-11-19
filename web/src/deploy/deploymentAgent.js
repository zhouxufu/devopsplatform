export function agentTransport(config={}) {
 return !!(config.deployment_agent_id || config.deployment_agent_ids || config.deployment_transport==='agent')
}
export function changeAgentTransport(config,value) {
 const next={...config}
 delete next.deployment_agent_id
 delete next.deployment_agent_ids
 delete next.deployment_transport
 if(value==='agent')next.deployment_transport='agent'
 return next
}
export function eligibleDeploymentAgents(agents,hosts) {
 return Array.isArray(hosts)&&hosts.length>0&&hosts.every(host=>agents.filter(a=>a.host===host&&a.enabled).length===1)
}
