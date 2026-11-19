import test from 'node:test'
import assert from 'node:assert/strict'
import {filterCatalog,parseVariableLines,auditActionLabel,auditResourceLabel} from './resourceCatalog.js'
test('category filters only search public resource fields',()=>{
 const rows=[{name:'db-test',kind:'mysql',endpoint:'db:3306',config:{database:'orders',password:'never-find'},password:'hidden'}]
 assert.equal(filterCatalog('connections',rows,{query:'orders'}).length,0)
 assert.equal(filterCatalog('connections',rows,{query:'hidden'}).length,0)
 assert.equal(filterCatalog('connections',rows,{query:'never-find'}).length,0)
 assert.equal(filterCatalog('connections',rows,{filter:'redis'}).length,0)
 assert.equal(filterCatalog('variables',[{name:'shared',keys:['TOKEN'],values:{TOKEN:'secret-value'}}],{query:'secret-value'}).length,0)
})
test('environment filters and display names are consistent across host and group lists',()=>{
 assert.equal(filterCatalog('hosts',[{name:'h',environment:'test'}],{query:'测试'},[{name:'test',display_name:'测试'}]).length,1)
 assert.equal(filterCatalog('groups',[{name:'g',environment:'test',hosts:['app-01']}],{query:'app-01',filter:'test'}).length,1)
})
test('audit filters stay within loaded records and missing dates never become today',()=>{
 const rows=[{actor:'admin',action:'save',resource_type:'connection',created_at:100},{actor:'a',action:'custom-action',resource_type:'custom-kind',created_at:0}]
 assert.equal(filterCatalog('audit',rows,{query:'保存'}).length,1)
 assert.equal(filterCatalog('audit',rows,{start:'2026-10-01T00:00:00'}).length,0)
 assert.equal(auditActionLabel('custom-action'),'custom-action')
 assert.equal(auditResourceLabel('custom-kind'),'custom-kind')
})
test('variable parser retains equals and empty values without leaking secrets in errors',()=>{
 assert.deepEqual(parseVariableLines('TOKEN=a=b\nEMPTY=\n'),{TOKEN:'a=b',EMPTY:''})
 assert.throws(()=>parseVariableLines('super-secret-line'),error=>!error.message.includes('super-secret'))
 assert.throws(()=>parseVariableLines('A=first-secret\nA=second-secret'),/第 2 行变量名重复/)
 assert.equal(Object.getPrototypeOf(parseVariableLines('__proto__=safe')),Object.prototype)
 assert.equal(parseVariableLines('__proto__=safe').__proto__,'safe')
})
