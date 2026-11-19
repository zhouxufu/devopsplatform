import {emptyRequest} from './testingModel.js'
// Parse words only. Never invoke a shell, follow redirects, or read local paths.
export function shellWords(input){
 if(new TextEncoder().encode(input).length>384*1024)throw Error('cURL 内容超过 384 KiB')
 const out=[];let word='',quote='',started=false
 for(let i=0;i<input.length;i++){
  const ch=input[i]
  if(quote!=="'"&&ch==='$'&&/[A-Za-z_]/.test(input[i+1]||''))throw Error('不支持 shell 环境变量，请改为平台变量 {{var.NAME}} 或具体值')
  if(!quote&&ch==='$'&&input[i+1]==="'")throw Error('暂不支持 $ 引号转义，请改为普通单引号或双引号')
  if(quote==="'"){if(ch==="'")quote='';else word+=ch;continue}
  if(ch==='\\'&&quote!=="'"){if(i+1===input.length)throw Error('命令末尾转义不完整');const next=input[++i];if(next==='\n')continue;if(next==='\r'&&input[i+1]==='\n'){i++;continue}if(quote==='"'&&!['"','\\','$','`'].includes(next))word+='\\';word+=next;started=true;continue}
  if(ch===quote&&quote){quote='';continue}
  if(!quote&&(ch==='"'||ch==="'")){quote=ch;started=true;continue}
  if(!quote&&/\s/.test(ch)){if(started){out.push(word);word='';started=false}continue}
  if(!quote&&/[;|&<>`]/.test(ch)||ch==='`'||ch==='$'&&quote!=="'"&&['(','{'].includes(input[i+1]))throw Error('仅支持一条 cURL 请求，不支持管道、重定向或命令替换')
  word+=ch;started=true
 }
 if(quote)throw Error('引号未闭合');if(started)out.push(word);return out
}
export function parseCurl(text){
 const words=shellWords(text.trim());if(words.shift()!=='curl')throw Error('请粘贴以 curl 开头的命令')
 const request=emptyRequest(),warnings=[],data=[],forms=[];let url='',explicitMethod='',get=false,head=false,binary=false,bodyType='',timeout=30
 const header=(raw)=>{const i=raw.indexOf(':');if(i<=0)throw Error('请求头格式应为 名称: 值');const key=raw.slice(0,i).trim(),value=raw.slice(i+1).trim();if(!/^[A-Za-z0-9!#$%&'*+.^_`|~-]+$/.test(key)||/[\r\n\0]/.test(value))throw Error('请求头名称或值无效');const existing=Object.keys(request.headers).find(k=>k.toLowerCase()===key.toLowerCase());if(existing)delete request.headers[existing];request.headers[key]=value}
 for(let i=0;i<words.length;i++){
  let option=words[i],value
  if(option.startsWith('--')&&option.includes('=')){[option,...value]=option.split('=');value=value.join('=')}
  else if(/^-[XHdFu]/.test(option)&&option.length>2){value=option.slice(2);option=option.slice(0,2)}
  const take=()=>{if(value!==undefined)return value;if(++i>=words.length)throw Error(option+' 缺少参数');return words[i]}
  switch(option){
   case '-X':case '--request':explicitMethod=take().toUpperCase();break
   case '-H':case '--header':header(take());break
   case '--url':if(url)throw Error('仅支持一个 URL');url=take();break
   case '-d':case '--data':case '--data-raw':case '--data-binary':case '--data-urlencode':case '--json':{
    const raw=take();if(option!=='--data-raw'&&raw.startsWith('@')){binary=true;request.file={name:raw.slice(1).split(/[\\/]/).pop(),content_type:'application/octet-stream',data_base64:'',pending:true};warnings.push('请在正文中重新选择文件：'+request.file.name);break}
    if(option==='--data-urlencode'){const split=raw.indexOf('=');forms.push({name:split<0?'':raw.slice(0,split),value:split<0?raw:raw.slice(split+1)});bodyType='urlencoded'}else{data.push(raw);if(option==='--json'){bodyType='json';if(!Object.keys(request.headers).some(k=>k.toLowerCase()==='content-type'))header('Content-Type: application/json');if(!Object.keys(request.headers).some(k=>k.toLowerCase()==='accept'))header('Accept: application/json')}}break
   }
   case '-F':case '--form':case '--form-string':{
    const raw=take(),split=raw.indexOf('=');if(split<=0)throw Error('表单参数应为 name=value');const name=raw.slice(0,split),value=raw.slice(split+1)
    if(option!=='--form-string'&&value.startsWith('@')){const [path,...meta]=value.slice(1).split(';');const type=meta.find(v=>v.startsWith('type='))?.slice(5)||'application/octet-stream';forms.push({name,value:'',file:{name:path.split(/[\\/]/).pop(),content_type:type,data_base64:'',pending:true}});warnings.push('请在表单中重新选择文件：'+path)}else{if(option!=='--form-string'&&value.startsWith('<'))throw Error('不支持从本地文件读取表单文本');forms.push({name,value})};bodyType='multipart';break
   }
   case '-u':case '--user':{const raw=take();header('Authorization: Basic '+btoa(unescape(encodeURIComponent(raw))));warnings.push('包含认证凭据，请核对；也可以改用环境认证');break}
   case '-b':case '--cookie':{const raw=take();if(!raw.includes('='))throw Error('不支持 Cookie 文件，请填写 Cookie 键值');header('Cookie: '+raw);warnings.push('已导入固定 Cookie，不会自动维护会话');break}
   case '-A':case '--user-agent':header('User-Agent: '+take());break
   case '-e':case '--referer':header('Referer: '+take());break
   case '--max-time':case '-m':timeout=Number(take());break
   case '-G':case '--get':get=true;break
   case '-I':case '--head':head=true;break
   case '-N':case '--no-buffer':request.mode='sse';break
   case '--compressed':case '-s':case '--silent':case '-S':case '--show-error':break
   case '-L':case '--location':warnings.push('平台不跟随重定向，保留首个响应');break
   case '-k':case '--insecure':throw Error('不支持忽略证书验证，请配置有效证书')
   default:if(option.startsWith('-'))throw Error('暂不支持选项 '+option+'，未导入，请先移除或手动配置');if(url)throw Error('仅支持一条请求和一个 URL');url=option
  }
 }
 if(!url)throw Error('缺少请求 URL');let parsed;try{parsed=new URL(url)}catch{throw Error('请输入完整 HTTP(S) URL')}
 if(!['http:','https:'].includes(parsed.protocol)||parsed.username||parsed.password||parsed.hash)throw Error('URL 不可包含凭据或片段')
 if(!Number.isInteger(timeout)||timeout<1||timeout>120)throw Error('超时须为 1–120 秒')
 if((binary&&data.length)||(forms.length&&data.length)||(binary&&forms.length))throw Error('不能混合多种正文类型，请拆开配置')
 const contentType=Object.entries(request.headers).find(([k])=>k.toLowerCase()==='content-type')?.[1]||''
 if(get){if(binary||bodyType==='multipart')throw Error('GET 参数不能引用文件');const raw=forms.length?new URLSearchParams(forms.map(f=>[f.name,f.value])).toString():data.join('&');if(raw)parsed.search+=(parsed.search?'&':'?')+raw;data.length=0;forms.length=0;bodyType=''}
 request.path=parsed.pathname+parsed.search;request.timeout_seconds=timeout;request.method=explicitMethod||(head?'HEAD':data.length||forms.length||binary?'POST':'GET')
 if(!['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS'].includes(request.method))throw Error('不支持的请求方法')
 if(binary)request.body_type='binary'
 else if(forms.length){request.body_type=bodyType;request.form=forms}
 else if(data.length){request.body=data.join('&');request.body_type=bodyType||(contentType.includes('json')?'json':contentType.startsWith('text/')?'text':'');if(!contentType&&!bodyType)header('Content-Type: application/x-www-form-urlencoded')}
 if(Object.entries(request.headers).some(([k,v])=>k.toLowerCase()==='accept'&&v.includes('text/event-stream')))request.mode='sse'
 const forbidden=/^(host|connection|content-length|transfer-encoding|proxy-authorization|proxy-connection|upgrade|trailer|te)$/i
 for(const key of Object.keys(request.headers)){if(forbidden.test(key)){delete request.headers[key];warnings.push('已移除由平台生成的请求头 '+key)}}
 if(Object.keys(request.headers).some(k=>/authorization|cookie|token|api[-_]?key/i.test(k)))warnings.push('当前环境认证配置优先生效，请核对认证头')
 return {request,origin:parsed.origin,warnings:[...new Set(warnings)]}
}
