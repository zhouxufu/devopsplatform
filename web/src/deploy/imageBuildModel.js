export function dockerStep(service){return service?.steps?.find(s=>s.step==='docker-build')}
export function imageCompiler(service){
 if(['maven','gradle'].includes(service?.build_tool))return service.build_tool
 const step=service?.steps?.find(s=>['java-build','npm-build'].includes(s.step))
 return step?.step==='java-build'?'java-command':step?.step==='npm-build'?'node':'dockerfile'
}
export function ensureDockerStep(service){
 service.steps||=[]
 if(!service.steps.length)service.steps.push({step:'git-checkout',params:{GIT_URL:service.git_url||'',BRANCH:service.branch||''}})
 let docker=dockerStep(service)
 if(!docker){docker={step:'docker-build',params:{CONTEXT:'.',DOCKERFILE_SOURCE:'repository',DOCKERFILE:'Dockerfile',PUSH:'true'}};service.steps.push(docker)}
 docker.params||={};docker.params.IMAGE_NAME=service.image_name||''
 return docker
}
export function setImageCompiler(service,mode){
 if(!['maven','gradle','java-command','node','dockerfile'].includes(mode))throw Error('请选择源码编译方式')
 const count=(service.steps||[]).filter(s=>['java-build','npm-build'].includes(s.step)).length
 if(count>1)throw Error('当前有多个源码编译节点，请在高级流水线中调整，平台不会覆盖这些节点')
 const oldIndex=(service.steps||[]).findIndex(s=>['java-build','npm-build'].includes(s.step))
 const previous=(service.steps||[])[oldIndex]?.params||{}
 const old=imageCompiler(service),docker=ensureDockerStep(service),node=mode==='node'
 service.build_tool=['maven','gradle'].includes(mode)?mode:'command'
 service.maven_executable||='auto';service.maven_goals||='clean package'
 service.gradle_executable||='auto';service.gradle_tasks||='clean build'
 if(old!==mode||!service.artifact_glob){service.artifact_glob=mode==='gradle'?'build/libs/*.jar':node?'dist':mode==='dockerfile'?'':'target/*.jar'}
 if(mode==='dockerfile')service.build_cmd=''
 else if(node&&old!==mode)service.build_cmd='npm ci && npm run build'
 else if(mode==='java-command'&&old!==mode)service.build_cmd=''
 const others=service.steps.filter(s=>!['java-build','npm-build'].includes(s.step))
 const index=oldIndex<0?others.indexOf(docker):Math.min(oldIndex,others.indexOf(docker))
 if(mode!=='dockerfile')others.splice(index,0,{step:node?'npm-build':'java-build',params:{...previous,COMMAND:['maven','gradle'].includes(mode)?'':service.build_cmd||previous.COMMAND||'',PRE:service.pre_cmd||previous.PRE||'',...(node?{INSTALL:''}:{})}})
 service.steps=others
 if(docker.params.DOCKERFILE_SOURCE==='template')docker.params.DOCKERFILE_ARTIFACT_PATH=service.artifact_glob
}
export function selectImageDockerfile(service,source,template){
 const docker=ensureDockerStep(service)
 docker.params.DOCKERFILE_SOURCE=source
 if(source==='template'){
  docker.params.DOCKERFILE_TEMPLATE=template||docker.params.DOCKERFILE_TEMPLATE||'java21'
  const compiler=imageCompiler(service)
  if(docker.params.DOCKERFILE_TEMPLATE==='nginx'&&compiler!=='node')setImageCompiler(service,'node')
  else if(docker.params.DOCKERFILE_TEMPLATE!=='nginx'&&!['maven','gradle','java-command'].includes(compiler))setImageCompiler(service,'maven')
  docker.params.DOCKERFILE_ARTIFACT_PATH=service.artifact_glob
 }
}
export function defaultJavaImage(service){
 ensureDockerStep(service).params.DOCKERFILE_SOURCE='template'
 ensureDockerStep(service).params.DOCKERFILE_TEMPLATE='java21'
 setImageCompiler(service,'maven')
 return service
}
export function imageBuildError(service){
 const compiler=imageCompiler(service),docker=dockerStep(service),p=docker?.params||{}
 if(!service.image_name?.trim())return '请填写镜像名称'
 if(p.DOCKERFILE_SOURCE==='template'&&(p.DOCKERFILE_TEMPLATE==='nginx'?compiler!=='node':!['maven','gradle','java-command'].includes(compiler)))return '运行时基础模板不能编译源码，请先选择对应的源码编译方式'
 if(['java-command','node'].includes(compiler)&&!(service.build_cmd||service.steps?.find(s=>['java-build','npm-build'].includes(s.step))?.params?.COMMAND)?.trim())return '请填写源码编译命令'
 if(compiler==='maven'&&(!service.maven_executable?.trim()||!service.maven_goals?.trim()))return '请填写 Maven 执行方式和 Goals'
 if(compiler==='gradle'&&(!service.gradle_executable?.trim()||!service.gradle_tasks?.trim()))return '请填写 Gradle 执行方式和任务'
 if(p.DOCKERFILE_SOURCE==='template'&&!p.DOCKERFILE_ARTIFACT_PATH?.trim())return '请填写源码编译产物路径'
 if(p.DOCKERFILE_SOURCE==='platform'&&(!p.DOCKERFILE_CONTENT?.trim()||new TextEncoder().encode(p.DOCKERFILE_CONTENT).length>32768))return '自定义 Dockerfile 必须非空，且不超过 32 KiB'
 return ''
}
