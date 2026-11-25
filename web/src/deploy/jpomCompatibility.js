import {changeAgentTransport} from './deploymentAgent.js'
export function jpomEnvironmentAllowed(environments, name, protectedTarget=false) {
 const environment=environments.find(v=>v.name===name)
 return !!environment&&!environment.protected&&!protectedTarget&&['development','test'].includes(environment.purpose)
}
export function selectJavaMode(config,value){
 const next=value==='jpom'?changeAgentTransport(config,'ssh'):{...config}
 next.java_mode=value;next.java_version||='21'
 if(value==='managed')next.health_failure_action||='rollback'
 return next
}
export function jpomConfigurationError(config={}){
 if(!/^[A-Za-z0-9_.-]{1,191}$/.test(config.jpom_project_id||''))return {field:'Jpom 项目 ID',message:'请填写原 Jpom 项目 ID'}
 const jar=config.jpom_jar_name||''
 if(!jar.toLowerCase().endsWith('.jar')||jar==='.jar'||/[\/\u0000\r\n]/.test(jar))return {field:'Jpom JAR 文件名',message:'请填写原 Jpom JAR 文件名，不包含目录'}
 if(config.jpom_log_file&&(!config.jpom_log_file.startsWith('/')||/[\u0000\r\n]/.test(config.jpom_log_file)))return {field:'Jpom 日志文件',message:'日志文件必须为绝对路径'}
 return null
}

const detectedJavaKeys=['java_mode','java_version','java_home','java_workdir','java_jvm_options','java_args','jpom_project_id','jpom_jar_name','jpom_log_file','jpom_original_dir']
export function applyJpomDetection(form,result){
 if(result?.status!=='matched'||!result.candidate)return null
 const candidate=result.candidate,config=candidate.deploy_config||{}
 if(!candidate.remote_dir?.startsWith('/')||config.java_mode!=='jpom'||jpomConfigurationError(config))throw Error('检测结果不完整，请重新识别')
 const patch=Object.fromEntries(detectedJavaKeys.filter(key=>typeof config[key]==='string').map(key=>[key,config[key]]))
 return {...form,remote_dir:candidate.remote_dir,deploy_config:{...selectJavaMode(form.deploy_config||{},'jpom'),...patch,java_detection:'jpom'}}
}
// Ignore delayed replies after any draft/host/environment change or a different editor is opened.
export function createJpomDetector({read,current,apply,notify,busy}){
 let sequence=0,controller
 return {
  cancel(){sequence++;controller?.abort();busy(false)},
  async detect(){
   controller?.abort();controller=new AbortController();const seq=++sequence,snapshot=current()
   if(!snapshot)return
   busy(true)
   try{
    const result=await read(JSON.parse(snapshot),controller.signal)
    if(seq!==sequence||current()!==snapshot)return
    const next=applyJpomDetection(JSON.parse(snapshot).form,result)
    if(next)apply(next)
    notify(result.message||'未能识别，请核对原 Jpom 配置',next?'success':'warning')
   }catch(error){if(seq===sequence&&current()===snapshot&&error.name!=='AbortError')notify(error.message||'检测未完成，原配置草稿仍保留','error')}
   finally{if(seq===sequence)busy(false)}
  }
 }
}

export function shouldAutoDetectJpom(form,service){
 const config=form.deploy_config||{}
 if(config.java_mode==='jpom'||config.java_detection==='manual')return false
 const directory=String(form.remote_dir||'').replace(/\/+$/,'')
 return !!directory&&(directory.endsWith('/.devopsplatform/'+service)||!!config.jpom_original_dir||!!config.jpom_project_id||!config.java_mode||config.java_mode==='custom')
}
