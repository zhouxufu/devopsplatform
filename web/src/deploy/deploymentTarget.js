export function deploymentTargetReady(target){
 if(!target)return false
 const config=target.deploy_config||{}
 if(target.target_type==='script')return !!(config.script?.trim()&&config.script_runner)
 if(target.target_type==='manual')return !!config.manual_instructions?.trim()
 if(target.target_type==='aliyun-sae')return !!(config.connection&&config.app_id&&config.namespace_id&&config.region)
 return !!target.hosts?.length
}
export function buildDeployBlocker(target,reason=''){
 if(!target)return '请选择目标环境'
 if(!deploymentTargetReady(target))return target.target_type==='manual'?'请先配置手动发布操作说明':'该环境发布资源不完整，请先配置环境'
 if(target.protected&&!target.approvers)return '受保护环境尚未配置审批人'
 if(target.require_reason&&!reason.trim())return '请填写发布原因'
 return ''
}
