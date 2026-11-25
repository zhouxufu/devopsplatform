// Installation identities are stable; only the current reported name may map
// an Agent back to a resource host. Historical aliases must not retarget writes.
const hostName = host => `${host.environment}/${host.name}`
const unique = rows => rows.length === 1 ? rows[0] : null

export function agentForHost(host, agents = [], remote = {}) {
  if (remote.agent_id) return unique(agents.filter(agent => agent.id === remote.agent_id))
  return unique(agents.filter(agent => agent.id === hostName(host) || agent.name === hostName(host)))
}

export function hostForAgent(id, hosts = [], agents = [], remoteStates = {}) {
  const installed = hosts.filter(host => remoteStates[host.name]?.agent_id === id)
  if (installed.length) return unique(installed)
  const agent = unique(agents.filter(row => row.id === id))
  const name = agent?.name || id
  if (agents.filter(row => row.id === name || row.name === name).length > 1) return null
  return unique(hosts.filter(host => hostName(host) === name &&
    (!remoteStates[host.name]?.agent_id || remoteStates[host.name].agent_id === id)))
}
