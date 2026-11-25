// Kept in the current workspace's memory. Closing an editor or moving between
// pages preserves drafts; no scripts or remote actions are saved/executed here.
import {reactive} from 'vue'
export const deliveryDrafts=reactive(new Map())

export function deliveryDraftKey(props){
 if(props.batch)return 'independent-batch'
 return props.focusChangeId&&props.item?.id?`item:${props.item.id}:change:${props.focusChangeId}`:props.item?.id
}
