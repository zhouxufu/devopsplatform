import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
export function buildState(key, defaults) {
  const storageKey='lp-build-ui:'+key
  let stored
  try { stored=JSON.parse(sessionStorage.getItem(storageKey)||'null') } catch {}
  const state=ref({...defaults,...stored})
  watch(state,value=>{try{sessionStorage.setItem(storageKey,JSON.stringify(value))}catch{}},{deep:true})
  return state
}
export function buildScroll(key) {
  const name='lp-build-scroll:'+key
  const main=()=>document.querySelector('.layout>main')
  function remember(){try{sessionStorage.setItem(name,String(main()?.scrollTop||0))}catch{}}
  async function restore(){await nextTick();try{if(main())main().scrollTop=Number(sessionStorage.getItem(name)||0)}catch{}}
  onBeforeUnmount(remember)
  return {remember,restore}
}
export function buildReturn(path, service) {
  if(typeof path==='string'&&(path==='/'||path.startsWith('/svc/'+encodeURIComponent(service)+'/'))&&!path.includes('://')&&!path.includes('\\'))return path
  return '/svc/'+encodeURIComponent(service)+'/build'
}
