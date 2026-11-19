import test from 'node:test'
import assert from 'node:assert/strict'
import {createTable,getCoreRowModel} from '@tanstack/vue-table'

test('TanStack column layout preserves server row order, row identity and selection payloads',()=>{
 const rows=[{id:9,title:'B',secret:'kept'},{id:2,title:'A',secret:'kept-2'}],snapshot=JSON.stringify(rows)
 const table=createTable({data:rows,columns:[{id:'id',accessorKey:'id',enableHiding:false},{id:'title',accessorKey:'title'},{id:'secret',accessorKey:'secret'}],getRowId:row=>String(row.id),getCoreRowModel:getCoreRowModel(),state:{columnVisibility:{secret:false},columnPinning:{left:['id'],right:[]},columnSizing:{title:260},columnOrder:[]},onStateChange:()=>{},renderFallbackValue:null})
 assert.deepEqual(table.getRowModel().rows.map(r=>r.original.id),[9,2])
 assert.deepEqual(table.getVisibleLeafColumns().map(c=>c.id),['id','title'])
 assert.deepEqual(table.getLeftVisibleLeafColumns().map(c=>c.id),['id'])
 assert.equal(table.getColumn('title').getSize(),260)
 assert.equal(table.getColumn('id').getCanHide(),false)
 assert.equal(table.getRowModel().rows[0].original.secret,'kept')
 assert.equal(JSON.stringify(rows),snapshot)
})
