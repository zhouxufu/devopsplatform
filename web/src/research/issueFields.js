export const severityLabels={'1':'致命','2':'严重','3':'一般','4':'提示'}
export const priorityLabels={'1':'紧急','2':'高','3':'中','4':'低'}
export const resolutionLabels={fixed:'已修复',duplicate:'重复 Bug',notrepro:'无法重现',bydesign:'设计如此',external:'外部原因',postponed:'延期处理',willnotfix:'不予解决',tostory:'转需求'}
export function fieldLabel(item,key,dictionaries={}){
 const code=item?.[key]??'',label=item?.[key+'_label']||dictionaries[item?.source]?.[key]?.[code]
 if(label)return label
 if(!code)return item?.source==='manual'?'—':item?.source_fields_read_at?(key==='resolution'?'未填写':'未设置'):'待补齐'
 return ({severity:severityLabels,priority:priorityLabels,resolution:resolutionLabels}[key]||{})[code]||String(code)
}
export {issueStatus, sourcePerson} from '../shared/issuePresentation.js'
