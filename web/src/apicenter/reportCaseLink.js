import {runSummary} from './responseModel.js'

// Locate the case from the frozen report, never from the editor's current selection.
export function reportCaseLink(run,unitIndex=null){
 if(!run)return null
 const units=run.units||[]
 let snapshot=run.case_snapshot
 if(unitIndex!==null){
  if(!Number.isInteger(unitIndex)||!units[unitIndex])return null
  snapshot=units[unitIndex].case_snapshot
 }else if(!run.case_id&&!snapshot?.id){
  const failure=runSummary(run).firstFailure
  snapshot=units.find(u=>failure>=0&&failure>=u.start&&failure<u.end)?.case_snapshot
  if(!snapshot&&units.length===1)snapshot=units[0].case_snapshot
 }
 const id=snapshot?.id||(unitIndex===null?run.case_id:null)
 if(!Number.isSafeInteger(id)||id<1)return null
 const scenario=run.scenario===true||snapshot?.scope==='scenario'
 const service=snapshot?.service||run.service
 if(!scenario&&!service)return null
 const query=new URLSearchParams(scenario?{scope:'scenario'}:{service})
 query.set('case',String(id))
 const revision=snapshot?.revision||(run.case_id===id?run.case_revision:null)
 return {id,revision:Number.isSafeInteger(revision)&&revision>0?revision:null,hash:'#/test-cases?'+query}
}
