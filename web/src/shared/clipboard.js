import {toast} from './shadcnFeedback.js'
import {writeClipboardText} from './clipboardWrite.js'
export async function copyText(value,{successMessage='已复制',failureMessage='复制失败，请手动选择内容复制'}={}){
 const copied=await writeClipboardText(value)
 toast(copied?successMessage:failureMessage,copied?'success':'error')
 return copied
}
