// Compile the exact production Less entries; do not install or run package code.
import assert from 'node:assert/strict'
import {readFile,readdir} from 'node:fs/promises'
import path from 'node:path'
import {createRequire} from 'node:module'
import {themePresets,normalizeThemePreset} from '../web/src/shared/themes/presets.js'
const require=createRequire(path.resolve('web/package.json')),less=require('less'),semver=require('semver')
const pins={'gi-demo':['vue-gi-demo','0.0.60'],wst2:['vue-wst2','0.0.20'],'0000':['vue-0000','0.0.7'],mengtu:['vue-mengtu','0.1.1'],'taolu-publishing-platform':['vue-taolu-publishing-platform','0.0.2']}
const packageJSON=JSON.parse(await readFile('web/package.json','utf8')),lock=JSON.parse(await readFile('web/package-lock.json','utf8'))
assert.equal(themePresets.length,6);assert.equal(normalizeThemePreset('https://untrusted/theme.css'),'default')
assert.deepEqual(themePresets.map(p=>p.name),['平台默认','清简蓝','雅致蓝','晴空蓝','梦兔粉','青禾绿'])
for(const [id,[pkg,version]]of Object.entries(pins)){
 const name='@arco-themes/'+pkg,root=path.resolve('web/node_modules',name),meta=JSON.parse(await readFile(path.join(root,'package.json'),'utf8'))
 assert.equal(meta.version,version);assert.equal(packageJSON.dependencies[name],version);assert.equal(lock.packages['node_modules/'+name].version,version);assert.equal(meta.license,'MIT')
 assert(semver.satisfies(require('@arco-design/web-vue/package.json').version,meta.peerDependencies['@arco-design/web-vue']))
 async function audit(dir){for(const entry of await readdir(dir,{withFileTypes:true})){const file=path.join(dir,entry.name);if(entry.isDirectory())await audit(file);else if(entry.name.endsWith('.less'))assert.doesNotMatch(await readFile(file,'utf8'),/@plugin\b|`|(?:@import\s+|url\(\s*)['"]?(?:https?:)?\/\//i)}}
 await audit(root)
 const file=path.resolve('web/src/shared/themes',id+'.less'),source=await readFile(file,'utf8')
 assert.match(source,/@arco-design\/web-vue\/dist\/arco.less/)
 const css=(await less.render(source,{filename:file,paths:[path.resolve('web/node_modules')],javascriptEnabled:false,math:'always'})).css
 for(const component of ['table','input','modal','menu','btn','radio','select'])assert(css.includes('.arco-'+component),id+' '+component)
 assert.match(css,/\[arco-theme=['"]?dark/);assert.doesNotMatch(css,/(?:@import\s+|url\(\s*)['"]?(?:https?:)?\/\//i)
 const baseline=(await less.render(['@arco-design/web-vue/dist/arco.less',name+'/theme.less',name+'/variables.less',name+'/component.less'].map(s=>'@import "'+path.resolve('web/node_modules',s)+'";').join('\n'),{filename:file,javascriptEnabled:false,math:'always'})).css
 assert.equal(css,baseline,id+' changed original theme colors/styles')
 console.log('PASS:',name,version,'original styles + current Arco, light/dark, no external assets')
}
