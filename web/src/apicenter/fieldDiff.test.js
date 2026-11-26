import {test} from 'node:test'
import assert from 'node:assert/strict'
import {describeFieldChanges,fieldPath} from './fieldDiff.js'
const field=(before,after,bp=true,ap=true)=>({path:'/operation/responses/200/content/application~1json/example',before,after,before_present:bp,after_present:ap})
test('whole removed response examples become individual fields including object arrays and exact IDs',()=>{
 const raw='{"data":{"id":210000000000000001,"images":[{"url":"demo.jpg","ok":false}],"cover":null}}'
 const {rows}=describeFieldChanges([field(raw,'null',true,false)])
 assert.equal(rows.length,4)
 assert.equal(rows.find(r=>r.path.endsWith('/ id')).before,'210000000000000001')
 assert.equal(rows.find(r=>r.path.endsWith('[0] / url')).before,'demo.jpg')
 assert.ok(rows.every(r=>r.kind==='删除'&&r.after==='（不存在）'&&r.path.includes('示例')))
})
test('compares leaves without unchanged noise and retains zero false null and empty values',()=>{
 const {rows}=describeFieldChanges([field('{"same":1,"v":0,"n":null,"s":""}','{"same":1,"v":false,"n":"null","s":"x","a":[]}')])
 assert.equal(rows.length,4)
 assert.deepEqual(rows.map(r=>r.kind),['修改','修改','修改','新增'])
 assert.equal(rows[0].before,'0');assert.equal(rows[0].after,'false');assert.equal(rows[2].before,'（空字符串）')
})
test('invalid or truncated original JSON is never silently dropped',()=>{
 const result=describeFieldChanges([field('{"id":…（已截断）','null',true,false)])
 assert.equal(result.truncated,true);assert.equal(result.rows[0].incomplete,true)
 assert.match(result.rows[0].before,/已截断/)
})
test('container type replacement preserves both sides and equal empty containers add no noise',()=>{
 const changes=describeFieldChanges([field('{"value":1}','false'),field('[]','[]'),field('null','"null"')])
 assert.deepEqual(changes.rows.map(r=>r.kind),['删除','新增','修改'])
 assert.equal(changes.rows[0].before,'1');assert.equal(changes.rows[1].after,'false')
 assert.equal(changes.rows[2].before,'null');assert.equal(changes.rows[2].after,'"null"')
})
test('bounds deep or large structures and preserves escaped pointer and literal markup',()=>{
 assert.equal(fieldPath('/operation/responses/200/content/application~1json/schema/properties/a~0b/description'),'接口 / 响应 / 200 / 内容 / application/json / 字段定义 / 字段 / a~b / 说明')
 const huge=JSON.stringify(Object.fromEntries(Array.from({length:400},(_,i)=>['f'+i,'<script>'])) )
 const result=describeFieldChanges([field('null',huge,false,true)])
 assert.equal(result.rows.length,300);assert.equal(result.truncated,true);assert.equal(result.rows[0].after,'<script>')
})
