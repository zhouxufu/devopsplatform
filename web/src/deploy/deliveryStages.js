// Partition only explicit SQL records; the server independently checks coverage.
export function splitSQLNode(nodes,index,selection,id){
 const node=nodes[index],chosen=new Set(selection)
 if(!node||node.kind!=='mysql'||!id||nodes.some(n=>n.id===id)||nodes.length>=30||!chosen.size||chosen.size>=node.change_ids?.length||[...chosen].some(value=>!node.change_ids.includes(value)))return false
 const moved=node.change_ids.filter(value=>chosen.has(value))
 const group=node.sql_group||node.id
 const next={...node,id,sql_group:group,name:`SQL 阶段 · ${moved.map(value=>'#'+value).join('、')}`,change_ids:moved,depends_on:[...(node.depends_on||[])]}
 node.change_ids=node.change_ids.filter(value=>!chosen.has(value))
 nodes.splice(index+1,0,next)
 return true
}
