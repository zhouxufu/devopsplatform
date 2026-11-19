export function executableSpec(tool) {
  const gradle = tool === 'gradle'
  return {name: gradle ? 'Gradle' : 'Maven', wrapper: gradle ? './gradlew' : './mvnw', system: gradle ? 'gradle' : 'mvn', path: gradle ? '/opt/gradle/bin/gradle' : '/opt/maven/bin/mvn'}
}
export function executableMode(tool, value) {
  const spec = executableSpec(tool)
  return !value || ['auto', spec.wrapper, spec.system].includes(value) ? (value || 'auto') : 'custom'
}
export function executableOptions(tool) {
  const spec = executableSpec(tool)
  return [
    {value:'auto', label:`自动（优先项目 ${spec.wrapper.slice(2)}）`},
    {value:spec.wrapper, label:`项目 ${spec.name} Wrapper`},
    {value:spec.system, label:`构建环境内置 ${spec.name}`},
    {value:'custom', label:'自定义可执行路径'},
  ]
}
export function buildRepairLocation(service, gradle = false) {
  return {path:`/svc/${encodeURIComponent(service)}/settings`, query:{settings_section:'command', settings_return:'build', ...(gradle ? {build_repair:gradle===true?'gradle':gradle} : {})}}
}
export function currentBuildLocation(service) {
  return {path:`/svc/${encodeURIComponent(service)}/build`, query:{prepare_build:'1'}}
}
