export const instanceStateText = state => ({running:'运行中',stopped:'已停止','not-deployed':'未部署',unreachable:'无法连接','inspection-failed':'读取失败',unknown:'状态待核对',restarting:'重启循环中',paused:'已暂停',dead:'容器异常',removing:'正在移除'})[state]||state
export function instanceActionPayload(instance,target,pending){
 if(!instance?.controllable||!instance.fingerprint)throw Error('当前实例归属未确认，请刷新状态')
 if(!pending?.confirmed)throw Error('请核对应用、环境和实例并确认')
 if(!['start','stop','restart','ready'].includes(pending.action))throw Error('操作类型无效')
 if(pending.action==='start'?instance.state!=='stopped':pending.action==='stop'?!['running','restarting'].includes(instance.state):instance.state!=='running')throw Error('实例状态已变化，请刷新后重新确认')
 const reason=(pending.reason||'').trim()
 if((target.protected||target.require_reason)&&!reason)throw Error('请填写操作原因')
 if(new TextEncoder().encode(reason).length>2000)throw Error('操作原因过长')
 if(!/^[a-zA-Z0-9-]{16,64}$/.test(pending.requestId||''))throw Error('操作标识无效')
 return {fingerprint:instance.fingerprint,request_id:pending.requestId,reason,confirm_protected:!!target.protected}
}
