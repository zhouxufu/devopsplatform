import {computed, onMounted, onUnmounted, ref, watch} from 'vue'
import {useAsyncData, requestJSON, requestList} from './asyncData.js'
import {toast} from './toast.js'

// Logs and metrics have the same rule API within their own plugin origin.
export function useAlertRules(environment) {
  const {data, loading, error, load:read} = useAsyncData()
  const saving=ref(false), busyId=ref(null)
  const rules=computed(()=>data.value?.rules||[]), st=computed(()=>data.value?.status||{}), notifs=computed(()=>data.value?.notifications||[])
  function load(){
    const scope=environment.value||''
    return read(async signal=>{
      const query=new URLSearchParams({environment:scope})
      const [rules,status,notifications]=await Promise.all([
        requestList('api/alerts?'+query,{signal}),requestJSON('api/alerts/status?'+query,{signal}),
        requestList('api/alert-notifications?'+new URLSearchParams({limit:50,environment:scope}),{signal}),
      ])
      if(status && (Array.isArray(status)||typeof status!=='object'))throw new Error('报警状态响应格式错误，请重试')
      return {rules,status:status||{},notifications}
    })
  }
  watch(environment,load,{immediate:true})
  let timer
  onMounted(()=>{timer=setInterval(()=>{if(!loading.value&&!saving.value&&busyId.value===null)load()},15000)})
  onUnmounted(()=>clearInterval(timer))
  async function save(body,id){
    if(saving.value||busyId.value!==null)return false
    saving.value=true
    try{
      await requestJSON(id?`api/alerts/${id}`:'api/alerts',{method:id?'PUT':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
      toast('已保存','success');await load();return true
    }catch(e){toast(e.message,'error');return false}finally{saving.value=false}
  }
  async function action(rule,type,ms){
    if(busyId.value!==null||saving.value)return
    busyId.value=rule.id
    try{
      const test=type==='test', body=type==='toggle'?{...rule,enabled:!rule.enabled}:{...rule,silence_until:ms>0?Date.now()+ms:0}
      await requestJSON(`api/alerts/${rule.id}`+(test?'/test':''),{method:test?'POST':'PUT',...(!test?{headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{})})
      toast(test?'测试通知已发送':type==='toggle'?(rule.enabled?'已停用':'已启用'):ms>0?'已静默 1 小时':'已解除静默','success')
      await load()
    }catch(e){toast(e.message,'error')}finally{busyId.value=null}
  }
  return {rules,st,notifs,loading,error,load,saving,busyId,save,action}
}
