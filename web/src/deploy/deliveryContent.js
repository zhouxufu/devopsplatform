const clone = value => JSON.parse(JSON.stringify(value));
export function deliveryRows(changes, editable, services=[], workItemID=0) {
  const rows=[];
  for (const c of changes) {
    const canEdit=c.status==='draft'&&!c.sql?.data_sync&&editable(c)&&(!workItemID||!c.delivery_ownership||c.delivery_ownership.origin_work_item_id===workItemID);
    // Existing records retain their independent identities and associations.
    // Only code rows with identical semantics/ownership may share one input.
    const repo=services.find(s=>s.name===c.service)?.git_url;
    const group=canEdit&&c.kind==='code'&&repo&&rows.find(r=>JSON.stringify(r.delivery_ownership)===JSON.stringify(c.delivery_ownership)&&services.find(s=>s.name===r.service)?.git_url===repo&&!r.readonly&&r.kind==='code'&&r.reference===c.reference&&r.summary===c.summary&&r.risk_level===c.risk_level&&r.rollback_plan===c.rollback_plan&&r.owner===c.owner);
    if(group){group.services.push(c.service);group.originals.push(clone(c));continue}
    rows.push({...clone(c),key:'existing-'+c.id,services:c.service?[c.service]:[],originals:[clone(c)],readonly:!canEdit});
  }
  return rows;
}
export function expandDeliveryRows(rows, title, origins=[], includeUnchanged=false) {
  const out=[];
  for(const row of rows){
    if(row.readonly)continue;
    const names=row.kind==='code'?row.services:[row.service||''];
    if(!names.length)throw Object.assign(new Error('代码内容请选择至少一个应用'),{rowKey:row.key,field:'services'});
    if(row.kind==='code'&&row.originals.some(c=>!names.includes(c.service)))throw Object.assign(new Error('已保存的代码应用不能在此移除；请在变更列表核对版本引用后处理'),{rowKey:row.key,field:'services'});
    for(const service of [...new Set(names)]){
      const old=row.originals.find(c=>(c.service||'')===service);
      const c={...row,id:old?.id||0,service,title:row.title||old?.title||`${title} · ${{code:'代码',mysql:'SQL',nacos:'配置',manual:'自定义记录',redis:'Redis',artifact:'制品'}[row.kind]||row.kind}${service?' · '+service:''}`};
      delete c.key;delete c.services;delete c.originals;delete c.readonly;delete c.work_items;
      if(c.kind!=='mysql')delete c.sql;if(c.kind!=='nacos')delete c.nacos;if(c.kind!=='redis')delete c.redis;
      // Unchanged rows, including shared ones, are not rewritten.
      if(!includeUnchanged&&old&&['kind','title','service','reference','summary','risk_level','rollback_plan','sql','nacos','redis','delivery_ownership'].every(k=>k==='service'?(c[k]||'')===(old[k]||''):JSON.stringify(c[k])===JSON.stringify(old[k])))continue;
      out.push(c);
      origins.push(row.key);
    }
  }
  return out;
}
