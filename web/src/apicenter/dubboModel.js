// Match the indexed signature against the losslessly parsed native document.
export function dubboMethod(operation,document){
 const indexed=operation.rpc||operation.definition||{}
 return document?.methods?.find(m=>m.interface===indexed.interface&&m.method===indexed.method&&(m.group||'')===(indexed.group||'')&&(m.version||'')===(indexed.version||'')&&JSON.stringify((m.parameters||[]).map(p=>p.java_type))===JSON.stringify((indexed.parameters||[]).map(p=>p.java_type)))||indexed
}
export const isDubboTarget=target=>target?.protocol==='dubbo'
