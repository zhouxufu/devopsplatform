// Match nacosValue's JSON scalar rules. Never convert or rewrite user input.
export function nacosValueType(value,format='yaml',action='set'){
 if(action==='delete')return '删除配置项'
 if(format==='properties')return '字符串（保留原文）'
 let parsed;try{parsed=JSON.parse(value)}catch{return '字符串'}
 if(parsed===null)return '空值 null'
 if(typeof parsed==='object')return '不支持对象或数组，请逐项填写'
 return {string:'字符串',number:'数字',boolean:'布尔值'}[typeof parsed]||'字符串'
}
