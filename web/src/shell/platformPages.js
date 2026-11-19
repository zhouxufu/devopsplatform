export const platformPages=[
 {id:'apps',label:'应用中心',icon:'apps'},
 {id:'resources',label:'资源中心',icon:'resources'},
 {id:'mcp',label:'MCP 与集成',icon:'devtools',plugin:'devtools'},
 {id:'plugins',label:'插件管理',icon:'plugins',admin:true},
]
export const platformWorkspacePlugin={name:'platform',display:'平台管理',url:'/?platform-page=1'}
export function platformPage(id,actor={},plugins=[]){const page=platformPages.find(p=>p.id===id);return page&&(!page.admin||actor.role==='admin')&&(!page.plugin||plugins.some(p=>p.name===page.plugin))?page:null}
export function platformPageFromHash(hash){try{return platformPages.find(p=>'/'+p.id===new URL((hash||'#/').slice(1),'http://platform').pathname)}catch{return null}}
