export const resourceKindLabels={git:'Git 代码凭据',maven:'Maven 私服',docker:'Docker Registry','aliyun-sae':'阿里云 SAE',mysql:'MySQL 数据库',redis:'Redis',nacos:'Nacos 配置中心（2.4 / 2.5）',zentao:'禅道 16.0（需求来源）',xxl_job:'XXL-JOB 调度中心'}
// Only enable the parameter tab when the editor actually renders fields.
export function hasConnectionParameters(kind){
 return ['mysql','redis','nacos','zentao','maven','aliyun-sae'].includes(kind)
}
export function connectionDatabaseLabel(connection){
 if(connection.kind==='mysql')return '绑定时选库'
 return connection.kind==='redis'?'DB '+(connection.config?.db||'0'):'—'
}
// Never search hidden credentials or arbitrary configuration values.
export function filterResourceConnections(connections,allowedKinds,kind='',query=''){
 const term=String(query||'').trim().toLocaleLowerCase()
 return connections.filter(c=>allowedKinds.includes(c.kind)&&(!kind||c.kind===kind)&&(!term||[c.name,resourceKindLabels[c.kind],c.endpoint].some(value=>String(value||'').toLocaleLowerCase().includes(term))))
}

// Drop obsolete defaults even when editing a stale pre-upgrade response.
export function instanceConnectionConfig(kind,config={}){
 const copy={...config}
 if(kind==='mysql')delete copy.database
 if(kind==='xxl_job')return {environment:copy.environment||''}
 return copy
}
