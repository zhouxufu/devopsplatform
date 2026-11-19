import { ref, watch, onBeforeUnmount, nextTick } from 'vue'
import {browserView} from '../shared/viewMemory.js'
export function buildState(key, defaults, actor) {
  const state=ref({...defaults})
  let memory,restoring=false
  watch(actor||(()=>''),value=>{if(!value)return;restoring=true;memory=browserView('build:'+key,value);state.value=memory.read(defaults);restoring=false},{immediate:true,flush:'sync'})
  watch(state,value=>{if(!restoring)memory?.write(value,defaults)},{deep:true,flush:'sync'})
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
