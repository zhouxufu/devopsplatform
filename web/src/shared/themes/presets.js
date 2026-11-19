// Pinned, locally bundled Arco Vue themes. No runtime CDN or user-supplied CSS.
export const themePresets = [
 {id:'default',name:'平台默认'},
 {id:'gi-demo',name:'清简蓝'},
 {id:'wst2',name:'雅致蓝'},
 {id:'0000',name:'晴空蓝'},
 {id:'mengtu',name:'梦兔粉'},
 {id:'taolu-publishing-platform',name:'青禾绿'},
]
export const validThemePreset=id=>themePresets.some(p=>p.id===id)
export const normalizeThemePreset=id=>validThemePreset(id)?id:'default'
const loaders={
 'gi-demo':()=>import('./gi-demo.less?inline'),
 wst2:()=>import('./wst2.less?inline'),
 '0000':()=>import('./0000.less?inline'),
 mengtu:()=>import('./mengtu.less?inline'),
 'taolu-publishing-platform':()=>import('./taolu-publishing-platform.less?inline'),
}
const pending=new Map()
export function loadThemePreset(id){
 if(!loaders[id])return Promise.reject(new Error('Unknown theme preset'))
 if(!pending.has(id))pending.set(id,loaders[id]().then(m=>{
  if(typeof m.default!=='string'||!m.default.trim())throw new Error('Empty theme stylesheet')
  return m.default
 }).catch(error=>{pending.delete(id);throw error}))
 return pending.get(id)
}
