export const activeReleaseStatuses = ['needs_review', 'waiting_manual', 'pending_approval', 'queued', 'running', 'waiting_next_batch', 'rolling_back']
export const activeInstanceStatuses = ['running', 'waiting_next_batch', 'needs_review']

// One owner for both projections. Failed reads keep the previous state and
// retry; they never unlock publication based on an empty/error response.
export function createReleasePolling({key, read, apply, error, enabled, schedule=setTimeout, cancel=clearTimeout}) {
  let timer, flight, generation=0, warned=false
  const stop=()=>{generation++;cancel(timer);timer=null;flight=null}
  const refresh=()=>{
    if(flight)return flight
    cancel(timer)
    const stamp=generation, current=key()
    const valid=()=>stamp===generation&&current===key()
    flight=(async()=>{
      let again=true
      try {
        const [orders,instances]=await read()
        if(!valid())return
        if(!Array.isArray(orders)||!Array.isArray(instances))throw Error('发布状态响应无效')
        apply(orders,instances);warned=false
        again=orders.some(v=>activeReleaseStatuses.includes(v.status))||instances.some(v=>activeInstanceStatuses.includes(v.status))
      }catch(e){if(valid()&&!warned){warned=true;error(e)}}
      finally {if(valid()){flight=null;if(again&&enabled())timer=schedule(refresh,2500)}}
    })()
    return flight
  }
  return {refresh,stop}
}
