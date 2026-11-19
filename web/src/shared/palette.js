// Each colorway belongs to one complete design. Never combine arbitrary accents with surfaces.
export const appearancePresets = [
  {id:'default',label:'默认主题',description:'Nova / Neutral · 原有布局与黑白灰配色',colorways:[
    {id:'neutral',label:'默认黑白灰',swatches:['#ffffff','#f5f5f5','#171717']},
  ]},
  {id:'compact',label:'紧凑工作台',description:'深色导航 · 双列待办 · 扁平分区',colorways:[
    {id:'graphite',label:'经典黑灰',swatches:['#1d2533','#f7f8fa','#2a3546']},
    {id:'steel',label:'冷调蓝灰',swatches:['#172b40','#f3f6fa','#285d8f']},
  ]},
  {id:'layered',label:'清爽分层',description:'浅灰画布 · 内容面板 · 清晰层级',colorways:[
    {id:'blue',label:'清爽蓝白',swatches:['#eaf0fc','#f3f5f9','#315eb7']},
    {id:'indigo',label:'沉静靛蓝',swatches:['#eeedf9','#f5f5fa','#514fa1']},
  ]},
  {id:'soft',label:'柔和现代',description:'悬浮导航 · 柔和卡片 · 舒展分栏',colorways:[
    {id:'sage',label:'暖灰绿',swatches:['#edf0e8','#f5f5f0','#3a594b']},
    {id:'sand',label:'暖沙棕',swatches:['#f0e9df','#f8f5ef','#76583d']},
  ]},
]
export function normalizeAppearance(value){
 const preset=appearancePresets.find(p=>p.id===value?.design)||appearancePresets[0]
 return {design:preset.id,colorway:preset.colorways.some(c=>c.id===value?.colorway)?value.colorway:preset.colorways[0].id}
}
export function migratePalette(value){
 return normalizeAppearance(value==='blue'?{design:'layered',colorway:'blue'}:value==='violet'?{design:'layered',colorway:'indigo'}:{design:'default',colorway:'neutral'})
}
export const densityPresets=[{id:'standard',label:'标准'},{id:'compact',label:'紧凑'}]
export const normalizeDensity=value=>densityPresets.some(p=>p.id===value)?value:'standard'
