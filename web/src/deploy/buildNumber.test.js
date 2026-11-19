import test from 'node:test'
import assert from 'node:assert/strict'
import {buildRunLabel,artifactBuildLabel,releaseOrderLabel} from './buildNumber.js'
import {readFileSync} from 'node:fs'
import {baseParse} from '@vue/compiler-dom'
import {parse} from '@vue/compiler-sfc'
test('application build labels use persisted sequence, not global route ID',()=>{
 assert.equal(buildRunLabel({id:18,number:1}),'#1')
 assert.equal(buildRunLabel({id:17,number:9}),'#9')
 assert.equal(buildRunLabel({id:19,number:2}),'#2')
})
test('missing sequence is explicitly an old record ID, not a guessed local sequence',()=>{
 assert.equal(buildRunLabel({id:18}),'记录 ID 18')
 assert.equal(buildRunLabel(null),'—')
 assert.equal(buildRunLabel({id:18,number:-1}),'记录 ID 18')
 assert.equal(artifactBuildLabel({source_id:18,source_number:1}),'#1')
})
test('releases in different applications may show the same number without changing control IDs',()=>{
 const a={id:19,number:1,service:'bus'},b={id:18,number:1,service:'seek'}
 assert.equal(releaseOrderLabel(a),'#1')
 assert.equal(releaseOrderLabel(b),'#1')
 assert.equal(a.id,19)
 assert.equal(b.id,18)
 assert.equal(releaseOrderLabel({id:19}),'记录 ID 19')
 assert.equal(releaseOrderLabel({id:19,number:0}),'记录 ID 19')
})
test('all application record panels receive user identity for filter memory and ownership',()=>{
 const source=readFileSync(new URL('./pages/DetailView.vue',import.meta.url),'utf8')
 const tree=baseParse(parse(source).descriptor.template.content),panels=[]
 function visit(node){if(node.tag==='BuildRecordsTable')panels.push(node);for(const child of node.children||[])visit(child)}
 visit(tree);assert.equal(panels.length,3)
 for(const panel of panels)assert.ok(panel.props.some(p=>p.type===7&&p.name==='bind'&&p.arg?.content==='me'&&p.exp?.content==='me'),'record panel must not lose its actor-scoped preferences')
})
