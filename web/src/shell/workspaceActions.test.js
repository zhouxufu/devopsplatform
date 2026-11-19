import test from 'node:test'
import assert from 'node:assert/strict'
import {workspaceCloseTargets,nextWorkspaceAfterClose} from './workspaceActions.js'
import {platformPage,platformWorkspacePlugin} from './platformPages.js'
import {workspaceSnapshot,restoreWorkspaceSnapshot} from './workspaceSession.js'
const windows=[{id:4},{id:9},{id:2}]
test('bulk close follows displayed order and never closes the fixed home',()=>{
 assert.deepEqual(workspaceCloseTargets(windows,9,'left'),[4]);assert.deepEqual(workspaceCloseTargets(windows,9,'right'),[2]);assert.deepEqual(workspaceCloseTargets(windows,0,'left'),[]);assert.deepEqual(workspaceCloseTargets(windows,0,'right'),[4,9,2]);assert.deepEqual(workspaceCloseTargets(windows,99,'left'),[]);assert.deepEqual(workspaceCloseTargets(windows,9,'all'),[4,9,2])
})
test('closed active page selects a surviving neighbour or home; unrelated active stays',()=>{assert.equal(nextWorkspaceAfterClose(windows,9,[4,9]),2);assert.equal(nextWorkspaceAfterClose(windows,2,[2]),9);assert.equal(nextWorkspaceAfterClose(windows,9,[4]),9);assert.equal(nextWorkspaceAfterClose(windows,2,[4,9,2]),0)})
test('platform navigation gates plugins by administrator and MCP by installation',()=>{assert.equal(platformPage('plugins',{role:'member'}),null);assert.equal(platformPage('plugins',{role:'admin'}).id,'plugins');assert.equal(platformPage('mcp',{},[]),null);assert.equal(platformPage('mcp',{},[{name:'devtools'}]).id,'mcp');assert.equal(platformPage('resources').id,'resources');assert.equal(platformPage('invalid'),null)})
test('platform page layout restores without restoring editor or secret values',()=>{const p=platformWorkspacePlugin;const snapshot=workspaceSnapshot([{id:1,plugin:p,hash:'#/plugins',title:'扩展配置',password:'secret'},{id:2,plugin:p,hash:'#/resources',context:{}}],1);assert.ok(!JSON.stringify(snapshot).includes('secret'));assert.equal(restoreWorkspaceSnapshot(snapshot,[p],{role:'admin'}).windows.length,2);const member=restoreWorkspaceSnapshot(snapshot,[p],{role:'member'});assert.equal(member.windows.length,1);assert.equal(member.windows[0].hash,'#/resources');assert.equal(member.active,-1)})
