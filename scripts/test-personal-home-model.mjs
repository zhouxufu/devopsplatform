import assert from 'node:assert/strict'
import {safeWorkspaceHash,windowContext,workspaceSnapshot,restoreWorkspaceSnapshot,workspaceStorageKey} from '../web/src/shell/workspaceSession.js'
assert.equal(safeWorkspaceHash('#/orchestration?run=12&token=secret&work_items=1,2&sql=DELETE&digest=secret'),'#/orchestration?run=12')
assert.equal(safeWorkspaceHash('#/work?quick=following&cursor=secret'),'#/work?quick=following')
assert.equal(safeWorkspaceHash('#//evil.example/'),'#/')
assert.equal(safeWorkspaceHash('#/%2e%2e/secret'),'#/')
assert.equal(safeWorkspaceHash('javascript:alert(1)'),'#/')
assert.equal(safeWorkspaceHash('#/?view=todo&task=12:qa'),'#/?view=todo&task=12%3Aqa')
const plugins=[{name:'logs'},{name:'research'}]
const ws=[{id:8,plugin:plugins[0],el:{name:'lp-workspace-fixture'},hash:'#/search?sql=SECRET',title:'应用 A 日志',context:{service:'a',environment:'test',range:[1,2],rangeMinutes:0},sql:'SECRET',dirty:true},{id:9,plugin:plugins[0],el:{name:'lp-workspace-second'},hash:'#/live',context:{service:'b',environment:'prod',rangeMinutes:15}}]
const snapshot=workspaceSnapshot(ws,9)
assert(!JSON.stringify(snapshot).includes('SECRET'))
assert(!('dirty' in snapshot.windows[0]))
const restored=restoreWorkspaceSnapshot(snapshot,plugins)
assert.equal(restored.active,1);assert.equal(restored.windows.length,2)
assert.equal(restored.windows[0].title,'应用 A 日志');assert.equal(restored.windows[0].name,'lp-workspace-fixture')
assert.equal(restored.windows[0].context.service,'a');assert.equal(restored.windows[1].context.service,'b')
assert.equal(workspaceSnapshot(ws,0).active,-1)
assert.notEqual(workspaceStorageKey('alice'),workspaceStorageKey('bob'))
assert.equal(restoreWorkspaceSnapshot(snapshot,[plugins[1]]).windows.length,0)
assert.equal(restoreWorkspaceSnapshot({...snapshot,windows:[...snapshot.windows,snapshot.windows[0]]},plugins).windows[2].name,'')
assert.equal(restoreWorkspaceSnapshot(null,plugins).active,-1)
assert.equal(windowContext({range:[Infinity,2],rangeMinutes:-1}).rangeMinutes,15)
console.log('Personal home window model: restore, isolation, renamed windows, route/secret allowlist, removed plugins and per-user keys passed')

assert.equal(safeWorkspaceHash('#/orchestration?area=templates&token=secret'),'#/orchestration?area=templates')
assert.equal(safeWorkspaceHash('#/changes?tab=work&item=101&digest=secret'),'#/changes?tab=work&item=101')
assert.equal(safeWorkspaceHash('#/changes?area=execute&item=-1'),'#/changes')
