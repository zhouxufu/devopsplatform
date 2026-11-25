const clone=value=>JSON.parse(JSON.stringify(value))
export const defaultDeploymentTarget=service=>({'java-bundle':'ssh-java-bundle',jar:'ssh-jar',image:'docker-host',static:'ssh-static'}[service?.artifact_type]||'ssh-jar')
export function deploymentTargetOptions(service,artifacts=[]){
 const primary=defaultDeploymentTarget(service)
 const javaImage=service?.artifact_type==='image'&&(['maven','gradle'].includes(service?.build_tool)||artifacts.some(a=>a.kind==='jar'))
 const options=[{value:primary,label:javaImage?'平台自动发布（Docker）':'平台自动发布'}]
 if(javaImage)options.push({value:'ssh-jar',label:'Java 进程（JAR）'})
 return [...options,{value:'aliyun-sae',label:'阿里云 SAE 自动发布'},{value:'script',label:'执行脚本发布'},{value:'manual',label:'下载制品后手动发布'}]
}
// Keep per-mode edits only within this editor. The saved environment still has
// one driver; Docker mounts/transport must never leak into a new Java target.
const modeFields=['remote_dir','start_cmd','stop_cmd','health_url','deploy_config']
export function switchDeploymentTarget(form,value,drafts){
 if(form.target_type===value)return
 drafts[form.target_type]=clone(Object.fromEntries(modeFields.map(k=>[k,form[k]??(k==='deploy_config'?{}:'')])))
 const fresh={remote_dir:value==='docker-host'?'/':'',start_cmd:'',stop_cmd:'',health_url:'',deploy_config:{}}
 if(value==='ssh-jar'||value==='ssh-java-bundle')fresh.deploy_config={java_mode:'managed',java_version:'21',java_jvm_options:'-Xms256m\n-Xmx1024m\n-Dfile.encoding=UTF-8',health_failure_action:'rollback'}
 Object.assign(form,clone(drafts[value]||fresh),{target_type:value})
}
export function artifactKindMatchesTarget(artifact,target,service){
 if(!target)return true
 const expected=target.target_type==='manual'&&target.deploy_config?.manual_artifact_kind||({'ssh-jar':'jar','ssh-java-bundle':'java-bundle','docker-host':'oci-image','ssh-static':'static-archive'}[target.target_type])||({'jar':'jar','image':'oci-image','static':'static-archive','java-bundle':'java-bundle'}[service?.artifact_type])
 return !expected||artifact.kind===expected||expected==='oci-image'&&artifact.kind==='docker-image-archive'
}
