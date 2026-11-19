// The style boundary must not become a new API or permission boundary.
// Requests still use the host's existing injected API and backend authorization.
export function editorAssetURL(baseURI){return new URL('assets/delivery-editor.html',baseURI).href}
export function editorRequestAllowed(path,method='GET'){
 if(typeof path!=='string'||path.length>4096||!['GET','POST','PUT'].includes(method))return false
 const [name]=path.split('?')
 if(/(^|\/)\.\.?($|\/)|%2f|%5c|\\/i.test(name))return false
 const rules={GET:[/^api\/work-items\/\d+\/delivery-content$/,/^api\/services\/[^/]+\/resources$/,/^api\/environments$/,/^api\/research\/work-items\/(sources|page)$/,/^api\/research\/development\/\d+$/],POST:[/^api\/changes\/batch$/,/^api\/changes\/\d+\/reopen$/,/^api\/git\/branches$/,/^api\/research\/work-items\/attach$/,/^api\/research\/development\/preview$/,/^api\/research\/development\/\d+\/(create|reconcile)$/],PUT:[/^api\/work-items\/\d+\/delivery-content$/]}
 return rules[method].some(rule=>rule.test(name))
}
export const editorProps=props=>JSON.parse(JSON.stringify(Object.fromEntries(['inline','embeddedSubmit','hideNextSteps','show','batch','item','services','workItems','actor','workspace'].map(key=>[key,props[key]]))))
export const editorMessage=value=>JSON.parse(JSON.stringify(value))
export function bridgeError(error){return {message:error?.message||'请求失败',...(Number.isInteger(error?.status)?{status:error.status}:{}),...(typeof error?.code==='string'?{code:error.code}:{})}}
