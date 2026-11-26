// Fast detail responses can normalize their URL before router.push commits.
// Keep the requested history entry until the latest navigation has settled.
export function createViewNavigation(router){
 let pushing=false,revision=0
 return (target,push=false)=>{
  pushing=push||pushing
  const seq=++revision
  return router[pushing?'push':'replace'](target).finally(()=>{if(seq===revision)pushing=false})
 }
}
