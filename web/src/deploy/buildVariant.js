export const variantFields=['name','maven_profiles','maven_properties','gradle_tasks','gradle_project_properties','gradle_system_properties']
export function normalizeBuildVariant(value){
 const result=Object.fromEntries(variantFields.map(key=>[key,String(value?.[key]||'').trim()]))
 return Object.values(result).some(Boolean)?result:{}
}
export function buildVariantLabel(artifact){return artifact?.metadata?.build_variant||'未记录环境方案'}

// Runtime-only environment defaults must never change a shared artifact build.
// Isolated branch patterns require delivery scope; they are not Git refs.
export function environmentBuildBranch(applicationDefault){
 if(!applicationDefault?.build_variant?.name)return null
 const policy=applicationDefault.code_policy||{},branch=String(policy.target_branch||'').trim()
 if(/[{}]/.test(branch))return {branch:'',hint:'此环境使用发布分支命名规则，请选择已生成的实际分支；也可通过部署编排准备代码。'}
 if(!branch||!policy.mode)return {branch:'',hint:'此环境尚未配置默认构建分支，请选择本次实际构建分支。'}
 return {branch,hint:'已带入此环境关联的默认分支；可仅为本次构建调整。'}
}
