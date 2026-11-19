import {ref,onMounted,onBeforeUnmount} from 'vue'
export function useWorkspaceActivity(){
 const active=ref(window.parent===window)
 function receive(event){if(event.source===window.parent&&event.origin===location.origin&&event.data?.type==='workspace.visibility')active.value=event.data.active===true}
 onMounted(()=>{window.addEventListener('message',receive);if(window.parent!==window)window.parent.postMessage({type:'workspace.activity.request'},location.origin)})
 onBeforeUnmount(()=>window.removeEventListener('message',receive))
 return active
}
