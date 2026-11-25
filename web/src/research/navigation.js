export function safeWorkReturn(value, fallback='/work') {
 if(typeof value!=='string'||value.length>2000||!value.startsWith('/')||value.startsWith('//'))return fallback
 try {const url=new URL(value,'http://workspace.local');return url.origin==='http://workspace.local'&&['/','/work','/work/delivery'].includes(url.pathname)&&!url.hash?value:fallback}catch{return fallback}
}
export function workOrigin(route,embedded=false) {
 return embedded?'/?view=work':safeWorkReturn(route.fullPath)
}
