import test from 'node:test'
import assert from 'node:assert/strict'
import {fieldValue,validateRules} from './formValidation.js'
test('required nested fields, arrays and numeric zero retain business validation',async()=>{
 const rules={'tree.id_field':[{required:true,message:'ID 必填'}],keys:[{required:true,type:'array',message:'键必填'}],max_parallel:[{required:true,type:'number'}]}
 assert.equal(fieldValue({tree:{id_field:'id'}},'tree.id_field'),'id')
 assert.deepEqual(await validateRules({tree:{id_field:''},keys:[],max_parallel:0},rules),{'tree.id_field':{message:'ID 必填'},keys:{message:'键必填'}})
 assert.equal(await validateRules({tree:{id_field:'id'},keys:['code'],max_parallel:0},rules),undefined)
})
test('identifier patterns and callback validators continue to block invalid saves',async()=>{
 const rules={name:[{match:/^[a-z][a-z0-9_-]{0,31}$/,message:'标识格式错误'}],parallel:[{validator:(value,callback)=>callback(Number.isInteger(value)&&value>=1&&value<=32?undefined:'并发数无效')}]}
 assert.deepEqual(await validateRules({name:'Bad Name',parallel:33},rules),{name:{message:'标识格式错误'},parallel:{message:'并发数无效'}})
 assert.equal(await validateRules({name:'runner-1',parallel:2},rules),undefined)
})
