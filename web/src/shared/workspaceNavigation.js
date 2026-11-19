// Only the owning console may navigate an embedded workspace. Vue Router still
// runs its leave guards; non-router workbenches can veto while an action is busy.
export function installWorkspaceNavigation(){
 window.addEventListener('message',event=>{
  if(window.parent===window||event.source!==window.parent||event.origin!==location.origin)return
  const hash=event.data?.hash
  if(event.data?.type!=='workspace.navigate'||typeof hash!=='string'||!/^#\/(?!\/)[^\r\n]*$/.test(hash))return
  if(window.dispatchEvent(new CustomEvent('workspace:before-navigate',{cancelable:true,detail:{hash}})))location.hash=hash
 })
}

// Vue Router uses pushState/replaceState for hash history; those do not fire
// hashchange. Report committed routes through its official lifecycle instead.
export function installWorkspaceRouterSync(app,host=window){
 let remove
 const notify=()=>{if(host.parent!==host)host.parent.postMessage({type:'workspace.route'},host.location.origin)}
 app.mixin({
  mounted(){
   if(this!==this.$root)return
   const router=app.config.globalProperties.$router
   if(!router)return
   remove=router.afterEach((_to,_from,failure)=>{if(!failure)notify()})
   notify()
  },
  unmounted(){if(this===this.$root)remove?.()}
 })
}
