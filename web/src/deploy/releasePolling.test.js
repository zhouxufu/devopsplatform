import test from 'node:test'
import assert from 'node:assert/strict'
import {createReleasePolling} from './releasePolling.js'

test('needs review keeps polling and reservations until observed success',async()=>{
 let state='needs_review',timers=[],last
 const polling=createReleasePolling({key:()=> 'app:qa',enabled:()=>true,read:async()=>[[{id:1,status:state}],[{id:7,status:state}]],apply:(orders,instances)=>last={orders,instances},error:e=>{throw e},schedule:f=>{timers.push(f);return f},cancel:()=>{}})
 await polling.refresh();assert.equal(last.orders[0].status,'needs_review');assert.equal(timers.length,1)
 state='success';await timers.shift()();assert.equal(last.orders[0].status,'success');assert.equal(timers.length,0);polling.stop()
})

for(const terminal of ['success','failed','canceled','timed_out','interrupted','rolled-back'])test('release polling terminates and restores state: '+terminal,async()=>{
  let state='running',timers=[],calls=0,last
  const polling=createReleasePolling({key:()=> 'app:test',enabled:()=>true,read:async()=>{calls++;return [[{id:1,status:state}],[{id:2,status:state}]]},apply:(orders,instances)=>last={orders,instances},error:e=>{throw e},schedule:f=>{timers.push(f);return f},cancel:f=>{timers=timers.filter(v=>v!==f)}})
  await Promise.all([polling.refresh(),polling.refresh()]);assert.equal(calls,1);assert.equal(timers.length,1)
  state=terminal;const tick=timers.shift();await tick();assert.equal(last.orders[0].status,terminal);assert.equal(last.instances[0].status,terminal);assert.equal(timers.length,0)
  polling.stop()
})

test('read errors preserve locks and retry, stale workspace results discarded',async()=>{
  let current='A',fail=false,timers=[],applied=0,warnings=0,resolve
  const polling=createReleasePolling({key:()=>current,enabled:()=>true,read:async()=>{if(fail)throw Error('network');return new Promise(r=>resolve=r)},apply:()=>applied++,error:()=>warnings++,schedule:f=>{timers.push(f);return f},cancel:f=>{timers=timers.filter(v=>v!==f)}})
  const first=polling.refresh();current='B';polling.stop();resolve([[{status:'success'}],[]]);await first;assert.equal(applied,0)
  fail=true;await polling.refresh();assert.equal(applied,0);assert.equal(warnings,1);assert.equal(timers.length,1)
  await timers.shift()();assert.equal(warnings,1);polling.stop();assert.equal(timers.length,0)
})

test('refresh restores authoritative terminal projections without a timer',async()=>{
  let state
  const polling=createReleasePolling({key:()=> 'app:test',enabled:()=>true,read:async()=>[[{id:1,status:'failed',succeeded:0,failed:1,total:1}],[{id:1,status:'failed'}]],apply:(orders,instances)=>state={orders,instances},error:e=>{throw e},schedule:()=>assert.fail('terminal refresh scheduled polling'),cancel:()=>{}})
  await polling.refresh();assert.equal(state.orders[0].failed,1);assert.equal(state.instances[0].status,'failed');polling.stop()
})
