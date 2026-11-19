import {ref} from 'vue'
import {toast as sonner} from 'vue-sonner'
export function toast(message,kind='info',options={}){return (sonner[kind]||sonner)(String(message),{duration:options.duration||3500,closeButton:true})}
export function dismissToast(id){if(id)sonner.dismiss(id)}
export const pendingConfirmation=ref(null)
export const confirmDialog={confirm:(content,title,options={})=>new Promise((resolve,reject)=>{if(pendingConfirmation.value){reject(Error('已有确认待处理'));return}pendingConfirmation.value={content,title,options,resolve,reject}})}
export async function confirmAction(content,title='请确认',options={}){try{await confirmDialog.confirm(content,title,options);return true}catch{return false}}
export async function promptAction(content,title='填写说明',options={}){try{return await confirmDialog.confirm(content,title,{...options,input:true})}catch{return null}}
export function finishConfirmation(accept,value=true){const request=pendingConfirmation.value;if(!request)return;pendingConfirmation.value=null;accept?request.resolve(value):request.reject(Error('canceled'))}
