import test from 'node:test'
import assert from 'node:assert/strict'
import {tableColumnLayout} from './tableSizing.js'
const title={id:'title',size:240,flex:true,pinned:'left'}
const status={id:'status',size:100}
const details={id:'details',size:220}
const actions={id:'actions',size:160,pinned:'right'}
test('a wide list assigns spare space to readable columns and keeps action widths stable',()=>{
 const columns=[title,status,actions],snapshot=structuredClone(columns)
 assert.deepEqual(tableColumnLayout(columns,{viewportWidth:1200,detailWidth:36}),{sizes:{title:904,status:100,actions:160},totalWidth:1200})
 assert.deepEqual(columns,snapshot)
})
test('hiding the flexible title fills the list using a visible reading column',()=>{
 assert.deepEqual(tableColumnLayout([status,details,actions],{viewportWidth:1000}),{sizes:{status:100,details:740,actions:160},totalWidth:1000})
})
test('all visible flexible columns share available space',()=>{
 assert.deepEqual(tableColumnLayout([title,{...details,flex:true},actions],{viewportWidth:1000}),{sizes:{title:430,details:410,actions:160},totalWidth:1000})
})
test('resized title and pinned actions preserve their explicit widths',()=>{
 assert.deepEqual(tableColumnLayout([{...title,size:300,resized:true},{...details,flex:true},actions],{viewportWidth:1000}),{sizes:{title:300,details:540,actions:160},totalWidth:1000})
 assert.deepEqual(tableColumnLayout([{...title,resized:true},actions],{viewportWidth:1000}),{sizes:{title:240,actions:160},totalWidth:400})
})
test('narrow embedded workspaces keep readable widths and local table scrolling',()=>{
 assert.deepEqual(tableColumnLayout([title,status,actions],{viewportWidth:320,detailWidth:36}),{sizes:{title:240,status:100,actions:160},totalWidth:536})
})
test('query result columns stay exact and utility-only tables do not stretch actions',()=>{
 assert.deepEqual(tableColumnLayout([title,status],{viewportWidth:1000,resultMode:true}),{sizes:{title:240,status:100},totalWidth:340})
 assert.deepEqual(tableColumnLayout([{id:'select',size:40,selection:true},actions],{viewportWidth:1000}),{sizes:{select:40,actions:160},totalWidth:200})
})
test('initial measurement, empty columns and returning from a narrower pane remain stable',()=>{
 assert.deepEqual(tableColumnLayout([],{viewportWidth:1200}),{sizes:{},totalWidth:0})
 assert.equal(tableColumnLayout([title,actions]).totalWidth,400)
 const columns=[title,status,actions]
 for(const width of [1440,390,900,1440]){
  const layout=tableColumnLayout(columns,{viewportWidth:width})
  assert.equal(layout.totalWidth,Math.max(500,width))
  assert.equal(layout.sizes.actions,160)
 }
})
