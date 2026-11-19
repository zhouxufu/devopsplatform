import test from 'node:test'
import assert from 'node:assert/strict'
import {canvasPoint,zoomAt,fitCanvas,canvasShortcut} from './flowInteractions.js'
test('zoom keeps the pointed world location fixed under existing pan and scale',()=>{
 const view={zoom:.68,pan:{x:-125,y:90}},point={x:340,y:180},world=canvasPoint(point,view),next=zoomAt(view,1.2,point)
 const restored=canvasPoint(point,next);assert.ok(Math.abs(restored.x-world.x)<1e-9);assert.ok(Math.abs(restored.y-world.y)<1e-9)
 assert.equal(zoomAt(view,4,point).zoom,1.6);assert.equal(zoomAt(view,0,point).zoom,.4)
})
test('fit changes viewport without reordering or changing saved node positions',()=>{
 const positions={b:{x:640,y:250},a:{x:-200,y:40}},original=structuredClone(positions),view=fitCanvas(positions,1440,800)
 assert.deepEqual(positions,original)
 for(const p of Object.values(positions)){assert.ok(p.x*view.zoom+view.pan.x>=0);assert.ok((p.x+230)*view.zoom+view.pan.x<=1440)}
})
test('canvas shortcuts preserve browser commands and composing input, and readonly cannot delete',()=>{
 assert.equal(canvasShortcut({key:'f',ctrlKey:true}),null)
 assert.equal(canvasShortcut({key:'Backspace',isComposing:true},{hasNode:true}),null)
 assert.equal(canvasShortcut({key:'Delete'},{hasNode:true,editable:false}),null)
 assert.equal(canvasShortcut({key:'Delete'},{hasNode:true,hasEdge:true}),'disconnect')
 assert.equal(canvasShortcut({key:'Enter'},{hasNode:true,editable:false}),'inspect')
})
