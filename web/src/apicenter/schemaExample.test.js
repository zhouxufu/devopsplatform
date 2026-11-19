import test from 'node:test'
import assert from 'node:assert/strict'
import {parse} from 'lossless-json'
import {schemaExample,jsonText} from './schemaExample.js'
import {schemaRows} from './model.js'
test('JSON schema examples preserve declared values and bounded recursion',()=>{
 const doc={components:{schemas:{Node:{type:'object',properties:{id:{type:'integer'},next:{$ref:'#/components/schemas/Node'}}}}}}
 const generated=schemaExample({$ref:'#/components/schemas/Node'},doc)
 assert.equal(generated.value.id,0);assert.equal(generated.truncated,true);assert.equal(generated.inferred,true)
 const explicit=schemaExample({},doc,{example:parse('{"id":9223372036854775807}')})
 assert.equal(explicit.inferred,false);assert.match(jsonText(explicit.value),/9223372036854775807/)
 assert.equal(schemaExample({$ref:'https://example.invalid/schema'},{}).truncated,true)
})
test('JSON structure and table identities distinguish dotted fields from nested paths',()=>{
 const schema={type:'object',properties:{'a.b':{type:'string'},a:{type:'object',properties:{b:{type:'integer'}}}}}
 const rows=schemaRows(schema,{})
 assert.equal(rows[0].field_path,'/a.b');assert.equal(rows[1].children[0].field_path,'/a/b')
 const example=schemaExample(schema,{})
 assert.equal(example.value['a.b'],'');assert.equal(example.value.a.b,0)
})
test('example generation handles arrays, nulls, enums and prototype-like field names as data',()=>{
 const schema=JSON.parse('{"type":"object","properties":{"__proto__":{"type":"string"},"flags":{"type":"array","items":{"type":"boolean"}},"status":{"enum":["OK"]},"none":{"type":"null"}}}')
 const {value}=schemaExample(schema,{})
 assert.equal(Object.getPrototypeOf(value),Object.prototype);assert.equal(Object.hasOwn(value,'__proto__'),true);assert.deepEqual(value.flags,[false]);assert.equal(value.none,null);assert.equal(value.status,'OK')
})
