// Isolated compatibility audit. Never install packages or modify production CSS.
import {readFile,writeFile,mkdir,readdir} from 'node:fs/promises'
import path from 'node:path'
import {createHash} from 'node:crypto'
import {createRequire} from 'node:module'
const require=createRequire(path.resolve('web/package.json')),less=require('less')
const root=process.env.THEME_AUDIT_DIR
if(!root||!path.isAbsolute(root))throw Error('THEME_AUDIT_DIR must name an explicit isolated directory')
const specs=[['gi-demo','GI-DEMO','cf42d4fac65c0bf41abd5ba5bce404862fbf9cd9'],['digitforce','digitforce-vue','403508d87afef17b1075b7312cae5c57010f4f04'],['taolu-publishing-platform','小程序管理平台','52ea408265f7be78416af046eb05a770a2171383'],['wst2','web-wst','0db35930f7eb2f9bf9dc61a9b8d13f521b794657'],['0000','智慧工地 V4.0','da8356679e8a2d8a3fe891094543033d6ff01b2a'],['christmas','Merry Christmas','49a198f08af98e6f5e28fc0344cb34fa9d1c5f18']]
specs.push(['mengtu','Mengtu Pink Vue','75e118a6169f21ff274eedee06c707a9f961b847'])
const base=path.resolve('web/node_modules/@arco-design/web-vue/dist/arco.less'),out=path.join(root,'compiled')
await mkdir(out,{recursive:true});const report=[]
for(const [id,label,sha1]of specs){
 const entry={id,label};report.push(entry)
 try{
  const archive=await readFile(path.join(root,id+'.tgz'))
  if(createHash('sha1').update(archive).digest('hex')!==sha1)throw Error('registry archive checksum mismatch')
  const pkg=path.join(root,id,'package'),meta=JSON.parse(await readFile(path.join(pkg,'package.json'),'utf8'))
  Object.assign(entry,{package:meta.name,version:meta.version,peer:meta.peerDependencies,license:meta.license,integrityVerified:true,vue:meta.depLibrary==='@arco-design/web-vue'||!!meta.peerDependencies?.['@arco-design/web-vue']})
  async function audit(dir){for(const f of await readdir(dir,{withFileTypes:true})){const file=path.join(dir,f.name);if(f.isDirectory())await audit(file);else if(f.name.endsWith('.less')){const source=await readFile(file,'utf8');if(/@plugin\b|`|url\(\s*['"]?https?:/i.test(source))throw Error('requires manual review of executable/remote Less: '+file)}}}
  if(!entry.vue){entry.compiled=false;entry.reason='React theme; not a drop-in Vue stylesheet; Christmas decorations reference external HTTP assets';console.log(JSON.stringify(entry));continue}
  await audit(pkg)
  const input=`@import "${base}";\n@import "${pkg}/theme.less";\n@import "${pkg}/variables.less";\n@import "${pkg}/component.less";`
  const rendered=await less.render(input,{filename:path.join(pkg,'audit-entry.less'),javascriptEnabled:false,math:'always'})
  if(/@import\s+['"]?https?:|url\(\s*['"]?https?:/i.test(rendered.css))throw Error('external asset dependency requires review')
  await writeFile(path.join(out,id+'.css'),rendered.css)
  entry.compiled=true;entry.bytes=Buffer.byteLength(rendered.css);entry.dark=/\[arco-theme=['"]?dark['"]?\]/.test(rendered.css);entry.mode='Original package Less + installed Arco Vue 2.59.0'
 }catch(e){entry.compiled=false;entry.reason=e.message}
 console.log(JSON.stringify(entry))
}
await writeFile(path.join(out,'report.json'),JSON.stringify(report,null,2))
