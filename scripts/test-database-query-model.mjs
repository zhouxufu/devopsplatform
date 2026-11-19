import assert from 'node:assert/strict'
import {emptyDataOptions,tableDataSQL,sqlLiteral} from '../web/src/workbench/databaseQuery.js'
const schema=[{field:'id',type:'bigint',key_type:'PRI'},{field:'name',type:'varchar',key_type:''}]
const options=emptyDataOptions()
options.conditions=[{field:'id',operator:'>=',value:'9007199254740993'},{field:'name',operator:'LIKE',value:"%O'Neil%"}]
options.order='name';options.direction='DESC';options.fields=['id','name']
assert.equal(tableDataSQL('MixedCase',schema,options,100),"SELECT `id`, `name` FROM `MixedCase` WHERE (`id` >= 9007199254740993) AND (`name` LIKE '%O''Neil%') ORDER BY `name` DESC LIMIT 100 OFFSET 100")
options.filterMode='sql';options.where="WHERE id > 1 OR name IS NULL"
assert.match(tableDataSQL('MixedCase',schema,options),/WHERE \(id > 1 OR name IS NULL\)/)
assert.equal(sqlLiteral('C:\\temp'),'CONVERT(0x433a5c74656d70 USING utf8mb4)')
const unsafe=emptyDataOptions();unsafe.conditions=[{field:'id; DROP TABLE menu',operator:'=',value:'x'}]
assert.throws(()=>tableDataSQL('menu',schema,unsafe))
unsafe.conditions=[{field:'id',operator:'IN',value:'1,,2'}];assert.throws(()=>tableDataSQL('menu',schema,unsafe))
unsafe.conditions=[];unsafe.size=1001;assert.throws(()=>tableDataSQL('menu',schema,unsafe))
console.log(JSON.stringify({ok:true,checks:['structured conditions','raw WHERE','projection and sorting','bounded pagination','quote and backslash preservation','identifier validation']}))
