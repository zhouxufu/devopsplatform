import {formatUnixTime} from '../shared/datePresentation.js'

export function artifactCreatedLabel(artifact){
 const time=formatUnixTime(artifact?.created_at)
 return time==='—'?'生成时间未知':'生成于 '+time
}

export function artifactCodePresentation(artifact){
 const origin=artifact?.code_origin
 if(origin?.target_branch&&origin?.commit)return {branch:origin.target_branch,commit:origin.commit,sources:origin.source_branches||[],environment:origin.environment||'',prepared:true}
 const ref=String(artifact?.metadata?.branch||'').trim()
 // A detached checkout is a commit, not a branch name. Unknown history stays
 // unknown; the application's current default cannot describe an old build.
 const detached=/^[a-f0-9]{40,64}$/i.test(ref)
 return {branch:detached?'分支未记录':ref||'分支未记录',commit:detached?ref:'',sources:[],environment:'',prepared:false}
}
