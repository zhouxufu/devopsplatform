// Source is only an upstream connection filter; stored/live modes are not user choices.
export function workItemSourceOptions(_sources=[],connections=[]){
 return connections.map(c=>({value:c.name,label:c.name}))
}
export const workItemSourceHelp='工作项实时读取来源平台；全部来源会统一查询所有已配置来源。本平台创建的工作项也会即时读取。选择后保留交付内容和关联记录，不修改来源 Bug。'
