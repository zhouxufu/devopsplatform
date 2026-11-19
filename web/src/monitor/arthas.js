export const arthasQuickCommands=['version','jvm','thread -n 5','dashboard -n 1','help']
export const arthasDiagnostics=[
 {key:'overview',label:'JVM 概览',description:'查看运行时、内存与线程指标'},
 {key:'memory',label:'内存使用',description:'查看堆、非堆及内存池'},
 {key:'hot_threads',label:'CPU 热点线程',description:'采样前 5 个热点线程'},
 {key:'blocked_threads',label:'阻塞线程',description:'检查持锁与等待线程'},
]
const metricLabels={used:'已使用',max:'最大值',committed:'已提交',init:'初始值',usage:'使用率',threadId:'线程 ID',threadName:'线程名称',threadState:'线程状态',cpuUsage:'CPU 使用率',cpuTime:'CPU 时间',blockedCount:'阻塞次数',blockedTime:'阻塞时间',lockName:'等待的锁',lockOwnerName:'持锁线程','JVM-NAME':'JVM 名称','JVM-VERSION':'JVM 版本','JVM-VENDOR':'JVM 提供商','UPTIME':'已运行时间','THREAD-COUNT':'线程数','PEAK-THREAD-COUNT':'峰值线程数','DAEMON-THREAD-COUNT':'守护线程数'}
const extraMetricLabels={name:'名称',id:'线程 ID',state:'线程状态',total:'已分配',cpu:'CPU（%）',blockingThreadCount:'等待该锁的线程数',waitedCount:'等待次数',deltaTime:'采样 CPU 时间'}
export const arthasMetricLabel=key=>extraMetricLabels[key]||metricLabels[key]||key
export const matchesArthasSample=(result,target)=>!!result?.target&&!!target&&result.target.id===target.id&&result.target.service===target.service&&result.target.environment===target.environment&&result.target.revision===target.revision
export const arthasNeedsConfirmation=command=>!arthasQuickCommands.includes(command.trim())
export const arthasScopeChanged=(target,service,environment)=>!!target&&!!(service&&service!==target.service||environment&&environment!==target.environment)
export const arthasStateLabel=state=>({success:'已完成',failed:'失败',timeout:'已达执行上限',unknown:'结果待核对',executing:'执行中',interrupted:'已请求中断'})[state]||state
// Keep output in this workspace's memory only, never in localStorage or URLs.
export function appendArthasOutput(entries,entry){return [...entries,entry].slice(-20)}

const sectionLabels={RUNTIME:'运行时',THREAD:'线程',MEMORY:'内存','CLASS-LOADING':'类加载',COMPILATION:'即时编译','GARBAGE-COLLECTORS':'垃圾回收器','MEMORY-MANAGERS':'内存管理器','OPERATING-SYSTEM':'操作系统',heapMemory:'堆内存',nonHeapMemory:'非堆内存',heap:'堆内存',nonHeap:'非堆内存',bufferPoolMemory:'缓冲池',HEAP:'堆内存',NON_HEAP:'非堆内存',busyThreads:'热点线程',threadStats:'线程统计',blockingLockInfo:'阻塞锁',threadInfo:'线程信息',blockingThreadInfo:'持锁线程'}
const overviewLabels={'JAVA-VERSION':'Java 版本','JAVA-HOME':'Java 安装目录','START-TIME':'启动时间','MACHINE-NAME':'主机名称','PROCESSORS-COUNT':'CPU 核数','OS-NAME':'系统名称','OS-VERSION':'系统版本','LOADED-CLASS-COUNT':'已加载类数','TOTAL-LOADED-CLASS-COUNT':'累计加载类数','UNLOADED-CLASS-COUNT':'已卸载类数','TOTAL-COMPILE-TIME':'累计编译时间','HEAP-MEMORY-USAGE':'堆内存使用','NON-HEAP-MEMORY-USAGE':'非堆内存使用','PENDING-FINALIZE-COUNT':'待回收对象数','TOTAL-STARTED-THREAD-COUNT':'累计启动线程数'}
export function arthasSectionLabel(section){
 return String(section||'诊断指标').replace(/^内存 \/ /,'').split(' ').map(part=>sectionLabels[part]||part).join(' ')
}
export function arthasDiagnosticSections(result){
 const groups=new Map()
 for(const [index,row] of (result?.rows||[]).entries()){
  const rawSection=String(row.section||'诊断指标')
  const section=result.diagnostic==='overview'?rawSection.replace(/^([A-Z][A-Z-]*) \d+$/,'$1'):rawSection
  if(!groups.has(section))groups.set(section,{id:section,label:arthasSectionLabel(section),rows:[]})
  groups.get(section).rows.push({id:index,label:overviewLabels[row.label]||arthasMetricLabel(row.label),value:row.value==null?'未返回':String(row.value)})
 }
 return [...groups.values()]
}
// Missing metrics are never synthesized as zero/healthy.
export function arthasDiagnosticHighlights(result){
 const keys=result?.diagnostic==='overview'?['JVM-VERSION','UPTIME','THREAD-COUNT','PEAK-THREAD-COUNT']:[]
 return keys.flatMap(key=>{const row=(result.rows||[]).find(r=>r.label===key);return row?[{label:arthasMetricLabel(key),value:String(row.value??'未返回')}]:[]})
}

function finiteMetric(value){
 const text=String(value??'').trim()
 if(!/^(?:\d+(?:\.\d*)?|\.\d+)%?$/.test(text))return null
 const number=Number(text.replace(/%$/,''));return Number.isFinite(number)?number:null
}
function memoryBytes(value){
 const match=String(value??'').trim().match(/^(\d+(?:\.\d+)?)\s*(B|KiB|MiB|GiB|TiB|PiB)?$/i)
 return match?Number(match[1])*1024**(['b','kib','mib','gib','tib','pib'].indexOf((match[2]||'b').toLowerCase())):null
}
export function arthasDiagnosticBars(result){
 if(!['memory','hot_threads'].includes(result?.diagnostic))return []
 const groups=new Map()
 for(const row of result.rows||[]){if(!groups.has(row.section))groups.set(row.section,{});groups.get(row.section)[row.label]=row.value}
 return [...groups].flatMap(([section,metrics])=>{
  if(result.diagnostic==='hot_threads'){
   const percent=finiteMetric(metrics.cpuUsage??metrics.cpu)
   return percent===null?[]:[{id:section,label:String(metrics.threadName??metrics.name??arthasSectionLabel(section)),percent,detail:`线程 ${metrics.threadId??metrics.id??'未返回 ID'} · ${metrics.threadState??metrics.state??'状态未返回'}`}]
  }
  const used=memoryBytes(metrics.used),max=memoryBytes(metrics.max)
  const percent=String(metrics.usage??'').trim().endsWith('%')?finiteMetric(metrics.usage):used!==null&&max>0?used/max*100:null
  return percent===null?[]:[{id:section,label:String(metrics.name??arthasSectionLabel(section)),percent,detail:`已用 ${metrics.used??'未返回'} / 上限 ${metrics.max??'未返回'}`}]
 }).sort((a,b)=>result.diagnostic==='hot_threads'?b.percent-a.percent:0)
}
