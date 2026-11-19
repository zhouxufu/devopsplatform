import test from 'node:test'
import assert from 'node:assert/strict'
import {workItemSourceOptions,workItemSourceHelp} from './workItemSources.js'
test('source choices contain only connection names and never stored/live classifications',()=>{
 const options=workItemSourceOptions(['manual','zentao:old'],[{name:'禅道'},{name:'研发'}])
 assert.deepEqual(options,[{value:'禅道',label:'禅道'},{value:'研发',label:'研发'}])
 assert.doesNotMatch(JSON.stringify(options),/纳管|已收录|managed:|自定义/)
 assert.match(workItemSourceHelp,/实时读取/)
 assert.match(workItemSourceHelp,/不修改来源 Bug/)
})
