// The catalog owns visibility. Historical telemetry and deployments are not
// evidence that a deleted application should appear in the current picker.
export function currentApplications(catalog, deployments = []) {
  const deployed = new Set(deployments.map(app => app.name))
  return catalog.filter(app => app.status !== 'archived').map(app => ({
    ...app, managed: deployed.has(app.name),
  })).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
}
