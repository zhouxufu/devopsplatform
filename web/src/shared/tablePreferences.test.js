import test from 'node:test'
import assert from 'node:assert/strict'
import {cleanTablePreferences,defaultTablePreferences,tableSchemaKey} from './tablePreferences.js'
const columns=[{id:'id',required:true,pin:'left',minSize:64},{id:'title',required:true,flex:true},{id:'severity',required:true},{id:'priority',required:true},{id:'resolution',required:true},{id:'updated',defaultHidden:true},{id:'actions',required:true,pin:'right'}]
test('core issue columns remain visible; secondary fields are available through details',()=>{
 const defaults=defaultTablePreferences(columns)
 assert.deepEqual(defaults.hidden,['updated'])
 assert.deepEqual(defaults.left,['id']);assert.deepEqual(defaults.right,['actions'])
 assert.deepEqual(cleanTablePreferences({hidden:['severity','priority','resolution','title','updated','unknown']},columns).hidden,['updated'])
})
test('stored layout is bounded and ignores unknown or malformed values',()=>{
 const next=cleanTablePreferences({hidden:['updated','updated'],widths:['id=-5','title=99999','unknown=140','id=NaN'],left:['id','id','unknown'],right:['id','actions'],sql:'secret',rows:[{password:'secret'}]},columns)
 assert.deepEqual(next,{hidden:['updated'],widths:['id=64','title=720'],left:['id'],right:['actions']})
 assert.deepEqual(cleanTablePreferences({hidden:{},widths:'bad',left:42,right:null},columns),{hidden:[],widths:[],left:[],right:[]})
})
test('schema preference namespaces are deterministic and separate targets and columns',()=>{
 assert.equal(tableSchemaKey(['db-a','id','name']),tableSchemaKey(['db-a','id','name']))
 assert.notEqual(tableSchemaKey(['db-a','id','name']),tableSchemaKey(['db-b','id','name']))
 assert.notEqual(tableSchemaKey(['db-a','id']),tableSchemaKey(['db-a','id','name']))
})

test('wide list column widths survive preference round trips without widening query defaults',()=>{
 const wide=[{id:'title',minSize:200,maxSize:4096},{id:'actions',minSize:64,maxSize:720}]
 const stored=cleanTablePreferences({widths:['title=1200','actions=200','unknown=800']},wide)
 assert.deepEqual(stored.widths,['title=1200','actions=200'])
 assert.deepEqual(cleanTablePreferences(stored,wide),stored)
 assert.deepEqual(cleanTablePreferences({widths:['title=99999']},wide).widths,['title=4096'])
 assert.deepEqual(cleanTablePreferences({widths:['title=1200']},[{id:'title',maxSize:720}]).widths,['title=720'])
})
