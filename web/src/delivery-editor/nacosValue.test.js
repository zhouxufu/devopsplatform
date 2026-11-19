import test from 'node:test'
import assert from 'node:assert/strict'
import {nacosValueType} from './nacosValue.js'
test('Nacos scalar type hints distinguish strings, booleans and properties without changing input',()=>{
 for(const [value,type] of [['false','布尔值'],['"false"','字符串'],['123','数字'],['null','空值 null'],['hello','字符串'],['01','字符串'],['[1]','不支持对象或数组，请逐项填写']])assert.equal(nacosValueType(value),type)
 assert.equal(nacosValueType('false','properties'),'字符串（保留原文）')
 assert.equal(nacosValueType('false','yaml','delete'),'删除配置项')
})
