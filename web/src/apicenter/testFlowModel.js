// Legacy cases retain their ordered definition. Graph edits use stable IDs;
// connecting or disconnecting edges never reorders nodes or changes their data.
export function connectSteps(steps,sourceID,targetID){
 const source=steps.find(s=>s.id===sourceID),target=steps.find(s=>s.id===targetID)
 if(!source||!target||source===target)throw Error('请选择两个不同的接口节点')
 const next=steps.filter(s=>s!==target),index=next.indexOf(source)
 next.splice(index+1,0,target)
 return next
}
export function flowReferences(steps){
 const sources=new Map(),links=[]
 for(const step of steps){
  const fields=[step.request?.path,step.request?.body,...Object.values(step.request?.headers||{}),...(step.request?.form||[]).map(f=>f.value),...(step.request?.assertions||[]).flatMap(a=>[a.path,a.expected])]
  const seen=new Set()
  for(const field of fields)for(const match of String(field||'').matchAll(/\{\{step\.([A-Za-z_][A-Za-z0-9_]{0,63})\}\}/g)){
   const source=sources.get(match[1]);if(source&&!seen.has(match[1])){links.push({from:source,to:step.id,name:match[1]});seen.add(match[1])}
  }
  for(const extraction of step.extractions||[])sources.set(extraction.name,step.id)
 }
 return links
}
export const linearGraph=steps=>({entry:steps[0]?.id||'',concurrency:4,edges:steps.slice(1).map((s,i)=>({from:steps[i].id,to:s.id}))})
export const edgeKey=edge=>JSON.stringify([edge.from,edge.to,edge.branch||''])
export const disconnectGraph=(graph,edge)=>({...graph,edges:graph.edges.filter(e=>edgeKey(e)!==edgeKey(edge))})
export function connectGraph(graph,steps,from,to,branch=''){
 if(from===to||!steps.some(s=>s.id===from)||!steps.some(s=>s.id===to))throw Error('请选择不同的有效节点')
 const kind=steps.find(s=>s.id===from).kind||'http'
 if(kind==='condition'&&!['true','false'].includes(branch))throw Error('请选择真或假路径')
 const edges=graph.edges.filter(e=>kind==='parallel'?!(e.from===from&&e.to===to):!(e.from===from&&(kind!=='condition'||e.branch===branch)))
 edges.push({from,to,...(branch?{branch}:{})})
 return {...graph,edges}
}
export function validateGraph(steps,graph){
 const ids=new Set(steps.map(s=>s.id)),incoming=new Map(steps.map(s=>[s.id,[]])),outgoing=new Map(steps.map(s=>[s.id,[]])),seen=new Set()
 if(ids.size!==steps.length||!ids.has(graph.entry)||!Number.isInteger(graph.concurrency)||graph.concurrency<1||graph.concurrency>4)throw Error('图入口、节点ID或并发数无效')
 for(const e of graph.edges){const key=e.from+'/'+e.to;if(!ids.has(e.from)||!ids.has(e.to)||e.from===e.to||seen.has(key))throw Error('连线无效或重复');seen.add(key);incoming.get(e.to).push(e);outgoing.get(e.from).push(e)}
 for(const s of steps){const ins=incoming.get(s.id),outs=outgoing.get(s.id),kind=s.kind||'http';if(!ins.length&&s.id!==graph.entry)throw Error('节点“'+s.name+'”尚未接入流程');if(kind!=='join'&&ins.length>1)throw Error('请使用汇合节点承接多条路径');if(kind==='condition'&&(outs.length!==2||!outs.some(e=>e.branch==='true')||!outs.some(e=>e.branch==='false')))throw Error('条件节点需要真/假两条路径');if(kind==='parallel'&&(outs.length<2||outs.length>4))throw Error('并行开始需要2–4条路径');if(kind==='join'&&(ins.length<2||outs.length>1))throw Error('汇合节点需要至少两条输入及最多一条输出');if(['http','dubbo'].includes(kind)&&outs.length>1)throw Error('接口节点只能连接一个后继');if(kind!=='condition'&&outs.some(e=>e.branch))throw Error('只有条件节点可以指定分支')}
 const deg=new Map([...incoming].map(([k,v])=>[k,v.length])),queue=[...deg].filter(([,n])=>n===0).map(([id])=>id);let count=0
 if(queue.length!==1||steps.filter(s=>!outgoing.get(s.id).length).length!==1)throw Error('流程须有唯一入口和终点，分支通过汇合收敛')
 while(queue.length){const id=queue.shift();count++;for(const e of outgoing.get(id)){deg.set(e.to,deg.get(e.to)-1);if(!deg.get(e.to))queue.push(e.to)}}
 if(count!==steps.length)throw Error('流程包含循环')
 return graph
}
