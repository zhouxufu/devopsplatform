// Native-hash workbenches must use the same route shape as the shell menus.
export function workbenchTabHash(id,currentHash=''){
 if(!['query','operations','sync','history','settings'].includes(id))return '#/query'
 const current=currentHash.slice(1).replace(/^\//,'').split('?')[0]
 return current===id&&currentHash.startsWith('#/')?currentHash:'#/'+id
}
