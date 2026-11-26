// The page's return button has an explicit destination. Browser history may
// belong to another workspace or have been replaced while loading a detail.
export function reportListLocation(query){
 const list={...query}
 for(const key of ['report','detail_service','detail_environment','detail_protocol'])delete list[key]
 return {path:'/reports',query:Object.fromEntries(Object.entries(list).filter(([,v])=>v!==''))}
}
