const clone = value => JSON.parse(JSON.stringify(value));
export function deliveryRows(changes, editable, services=[]) {
  const rows=[];
  for (const c of changes) {
    const canEdit=c.status==='draft'&&!c.sql?.data_sync&&editable(c);
    // Existing records retain their independent identities and associations.
    // Only code rows with identical semantics/ownership may share one input.
    const repo=services.find(s=>s.name===c.service)?.git_url;
    const group=canEdit&&c.kind==='code'&&repo&&rows.find(r=>services.find(s=>s.name===r.service)?.git_url===repo&&!r.readonly&&r.kind==='code'&&r.reference===c.reference&&r.summary===c.summary&&r.risk_level===c.risk_level&&r.rollback_plan===c.rollback_plan&&r.owner===c.owner);
    if(group){group.services.push(c.service);group.originals.push(clone(c));continue}
    rows.push({...clone(c),key:'existing-'+c.id,services:c.service?[c.service]:[],originals:[clone(c)],readonly:!canEdit});
  }
  return rows;
}
export function expandDeliveryRows(rows, title) {
  const out=[];
  for(const row of rows){
    if(row.readonly)continue;
    const names=row.kind==='code'?row.services:[row.service||''];
    if(!names.length)throw new Error('代码内容请选择至少一个应用');
    if(row.kind==='code'&&row.originals.some(c=>!names.includes(c.service)))throw new Error('已保存的代码应用不能在此移除；请在变更列表核对版本引用后处理');
    for(const service of [...new Set(names)]){
      const old=row.originals.find(c=>c.service===service);
      const c={...row,id:old?.id||0,service,title:old?.title||`${title} · ${{code:'代码',mysql:'SQL',nacos:'配置',manual:'人工',redis:'Redis',artifact:'制品'}[row.kind]||row.kind}${service?' · '+service:''}`};
      delete c.key;delete c.services;delete c.originals;delete c.readonly;delete c.work_items;
      if(c.kind!=='mysql')delete c.sql;if(c.kind!=='nacos')delete c.nacos;
      // Unchanged rows, including shared ones, are not rewritten.
      if(old&&['kind','title','service','reference','summary','risk_level','rollback_plan','sql','nacos'].every(k=>JSON.stringify(c[k])===JSON.stringify(old[k])))continue;
      out.push(c);
    }
  }
  return out;
}
