// Only the owning console may navigate an embedded workspace. Vue Router still
// runs its leave guards; non-router workbenches can veto while an action is busy.
const workspaceRouters=new WeakMap()
export function installWorkspaceNavigation(host=window){
 host.addEventListener('message',event=>{
  if(host.parent===host||event.source!==host.parent||event.origin!==host.location.origin)return
  const hash=event.data?.hash
  if(event.data?.type!=='workspace.navigate'||typeof hash!=='string'||!/^#\/(?!\/)[^\r\n]*$/.test(hash))return
  if(!host.dispatchEvent(new CustomEvent('workspace:before-navigate',{cancelable:true,detail:{hash}})))return
  const router=workspaceRouters.get(host)
  if(router)router[event.data.replace===true?'replace':'push'](hash.slice(1))
  else if(event.data.replace===true)host.location.replace(host.location.href.split('#')[0]+hash)
  else host.location.hash=hash
 })
}

// Vue Router uses pushState/replaceState for hash history; those do not fire
// hashchange. Report committed routes through its official lifecycle instead.
export function installWorkspaceRouterSync(app,host=window){
 let remove,popping=false
 const onPop=()=>{popping=true}
 const notify=()=>{if(host.parent!==host)host.parent.postMessage({type:'workspace.route'},host.location.origin)}
 app.mixin({
  mounted(){
   if(this!==this.$root)return
   const router=app.config.globalProperties.$router
   if(!router)return
   workspaceRouters.set(host,router)
   host.addEventListener('popstate',onPop)
   remove=router.afterEach((_to,_from,failure)=>{const traversed=popping;popping=false;if(!failure){notify();if(traversed&&host.parent!==host)host.parent.postMessage({type:'workspace.history'},host.location.origin)}})
   notify()
  },
  unmounted(){if(this===this.$root){remove?.();workspaceRouters.delete(host);host.removeEventListener('popstate',onPop)}}
 })
}
