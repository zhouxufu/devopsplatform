export const JAVA_BUNDLE = 'java-bundle'
export const JAVA_BUNDLE_TARGET = 'ssh-java-bundle'
export function bundleParams(service = {}) {
  return {MAIN_JAR:'lib/app.jar',REQUIRED_PATHS:'',...(service.steps?.find(s=>s.step==='package-java-bundle')?.params||{}),ROOT:service.artifact_glob||'build/release'}
}
export function updateBundleParams(service, patch) {
  if(!service.steps?.length) service.steps=[{step:'git-checkout',params:{}},{step:'java-build',params:{}}]
  let step=service.steps.find(s=>s.step==='package-java-bundle')
  if(!step){step={step:'package-java-bundle',params:{}};service.steps.push(step)}
  step.params={...bundleParams(service),...patch}
}
export function bundleRelative(value) {
  return typeof value==='string' && !!value && !value.startsWith('/') && !value.includes('\\') && !/[\u0000-\u001f\u007f*?[]/.test(value) && value.split('/').every(part=>part&&part!=='.'&&part!=='..')
}
export function javaBundleError(service) {
  if(service.artifact_type!==JAVA_BUNDLE)return ''
  const p=bundleParams(service)
  if(!bundleRelative(service.artifact_glob))return '发布目录必须是明确的源码相对路径'
  if(!bundleRelative(p.MAIN_JAR)||!p.MAIN_JAR.endsWith('.jar'))return '请填写包内主 JAR 的相对路径'
  if((p.REQUIRED_PATHS||'').split('\n').map(s=>s.trim()).filter(Boolean).some(s=>!bundleRelative(s)))return '必需文件须使用包内相对路径，每行一项'
  if(!['maven','gradle'].includes(service.build_tool)&&!service.build_cmd?.trim())return '请填写整包构建命令'
  return ''
}
