// Audit only the downloaded fixture packages and generated CSS, never install.
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import path from 'node:path'
import {createRequire} from 'node:module'
import {themeReferences} from '../web/src/qa/themeReferences.js'
const root=process.env.THEME_AUDIT_DIR;if(!root||!path.isAbsolute(root))throw Error('THEME_AUDIT_DIR required')
const require=createRequire(path.resolve('web/package.json')),semver=require('semver'),postcss=require('postcss')
const actual=require('@arco-design/web-vue/package.json').version
const report=JSON.parse(await readFile(path.join(root,'compiled/report.json'),'utf8'))
assert.equal(report.length,7)
for(const t of report){
 assert.equal(t.integrityVerified,true,t.id)
 if(!t.vue){assert.equal(t.compiled,false);assert.ok(t.peer['@arco-design/web-react']);continue}
 assert.ok(semver.satisfies(actual,t.peer['@arco-design/web-vue']),t.id+' peer range')
 assert.equal(t.compiled,true,t.id);assert.equal(t.dark,true,t.id)
 const css=await readFile(path.join(root,'compiled',t.id+'.css'),'utf8');postcss.parse(css)
 for(const component of ['table','input','modal','menu','btn','radio','select'])assert.ok(css.includes('.arco-'+component),t.id+' '+component)
 assert.doesNotMatch(css,/(?:@import\s+|url\(\s*)['"]?(?:https?:)?\/\//i,t.id+' remote dependency')
}
const visuals=JSON.parse(await readFile(path.join(root,'compiled/visual-results.json'),'utf8'))
const dialogs=JSON.parse(await readFile(path.join(root,'compiled/dialog-results.json'),'utf8'))
for(const t of report.filter(t=>t.vue))for(const mode of ['light','dark']){
 const v=visuals.find(v=>v.id===t.id&&v.mode===mode);assert.ok(v,t.id+' '+mode+' not browser-verified');assert.equal(v.rows,4);assert.equal(v.overflow,false)
 const dialog=dialogs.find(v=>v.id===t.id&&v.mode===mode);assert.ok(dialog);assert.equal(dialog.title,'接入应用');assert.ok(dialog.width>600&&dialog.height<900)
}
console.log('PASS: seven checksummed packages; six peer-compatible Vue themes recompiled with '+actual+'; twelve browser table + twelve form/modal samples; React theme explicitly excluded')
for(const t of themeReferences){
 assert.equal(t.reference,true);assert.equal(t.compiled,false)
 assert.equal(t.id,'vue-me');assert.match(t.notice,/不是本平台接入效果/);assert.equal(t.package,'@arco-themes/vue-mengtu')
 assert.equal(report.some(p=>p.id===t.id),false,'reference must not masquerade as verified npm package')
 for(const mode of ['light','dark']){
  const png=await readFile(path.join(root,'compiled/shots',t.screenshots[mode]))
  assert.equal(png.subarray(1,4).toString(),'PNG')
 }
}
console.log('PASS: Vue ME official light/dark references are explicitly separate from compiled package results')
