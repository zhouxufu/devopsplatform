// Allocate only presentation space. Explicit user widths and result-set geometry remain unchanged.
export function tableColumnLayout(columns,{viewportWidth=0,detailWidth=0,resultMode=false}={}){
 const sizes=Object.fromEntries(columns.map(column=>[column.id,column.size]))
 const baseWidth=columns.reduce((sum,column)=>sum+column.size,detailWidth)
 const extra=Math.max(0,viewportWidth-baseWidth)
 if(!resultMode&&extra>0){
  let flexible=columns.filter(column=>column.flex&&!column.resized)
  // A hidden title column must not leave the remaining list occupying only part of the workspace.
  if(!flexible.length&&!columns.some(column=>column.flex)){
   const candidates=columns.filter(column=>!column.selection&&!column.resized&&column.pinned!=='right')
   const widest=candidates.reduce((best,column)=>!best||column.size>best.size?column:best,null)
   flexible=widest?[widest]:[]
  }
  if(flexible.length)for(const column of flexible)sizes[column.id]+=extra/flexible.length
 }
 return {sizes,totalWidth:columns.reduce((sum,column)=>sum+sizes[column.id],detailWidth)}
}
