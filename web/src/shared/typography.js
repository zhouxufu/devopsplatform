// Semantic type sizes used by Arco's Less tokens and business content.
export const typographyPresets=[{id:'small',label:'小',body:14,caption:12,heading:20,icon:16,component:'small'},{id:'standard',label:'标准',body:16,caption:14,heading:22,icon:18,component:'medium'},{id:'large',label:'大',body:18,caption:16,heading:24,icon:20,component:'large'}]
export const normalizeTypography=value=>typographyPresets.some(p=>p.id===value)?value:'standard'
export function typographyVariables(value){
 const p=typographyPresets.find(p=>p.id===normalizeTypography(value))
 const px=n=>n+'px'
 return {'--font-size-body-1':px(p.caption),'--font-size-body-2':px(p.body-1),'--font-size-body-3':px(p.body),'--font-size-caption':px(p.caption),'--font-size-title-1':px(p.body+2),'--font-size-title-2':px(p.heading),'--font-size-title-3':px(p.heading+4),'--font-size-display-1':px(p.heading+12),'--font-size-display-2':px(p.heading+24),'--font-size-display-3':px(p.heading+32),'--icon-size-small':px(p.icon-2),'--icon-size-medium':px(p.icon),'--icon-size-large':px(p.icon+4),'--icon-scale':String(p.icon/18)}
}
export const typographyComponentSize=value=>typographyPresets.find(p=>p.id===normalizeTypography(value)).component
