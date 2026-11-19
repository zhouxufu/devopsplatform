// Kept in the current workspace's memory. Closing an editor or moving between
// pages preserves drafts; no scripts or remote actions are saved/executed here.
import {reactive} from 'vue'
export const deliveryDrafts=reactive(new Map())
