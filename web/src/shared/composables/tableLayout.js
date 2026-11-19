import {onMounted,onBeforeUnmount,ref} from 'vue'
// Configure Arco's fixed-column feature only where the workspace has room for it.
// Embedded plugins use their own viewport, so a narrow host pane is handled too.
export function useTableLayout(){
 const query=window.matchMedia('(min-width:960px)'),pinTableActions=ref(query.matches)
 const update=()=>{pinTableActions.value=query.matches}
 onMounted(()=>{update();query.addEventListener('change',update)})
 onBeforeUnmount(()=>query.removeEventListener('change',update))
 return {pinTableActions}
}
