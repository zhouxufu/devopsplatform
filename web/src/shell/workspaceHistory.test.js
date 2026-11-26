import test from 'node:test'
import assert from 'node:assert/strict'
import {createWorkspaceHistory} from './workspaceHistory.js'

function fixture(){
 const listeners=new Map(),entries=[{other:'preserved'}];let index=0,active='',allowed=true
 const windows=new Set(['lp-workspace-a','lp-workspace-b'])
 const host={addEventListener:(name,fn)=>listeners.set(name,fn),removeEventListener:name=>listeners.delete(name),history:{get state(){return entries[index]},replaceState(state){entries[index]=state},pushState(state){entries.splice(++index);entries.push(state)}}}
 const manager=createWorkspaceHistory(host,{activeName:()=>active,hasWorkspace:name=>windows.has(name),activate:name=>{active=name},canRestore:()=>allowed})
 return {host,manager,entries,windows,get active(){return active},set active(v){active=v},set allowed(v){allowed=v},back(){index--;listeners.get('popstate')({state:entries[index]})},forward(){index++;listeners.get('popstate')({state:entries[index]})},listeners}
}
test('tab back and forward restore previous live workspaces without creating duplicate history',()=>{
 const f=fixture();f.manager.start();f.active='lp-workspace-a';f.manager.record();f.active='lp-workspace-b';f.manager.record()
 assert.equal(f.entries.length,3);f.back();assert.equal(f.active,'lp-workspace-a');f.back();assert.equal(f.active,'');f.forward();assert.equal(f.active,'lp-workspace-a');assert.equal(f.entries.length,3)
 assert.equal(f.host.history.state.other,'preserved')
})
test('same-tab activation does not consume back steps, closed tabs fall back to home',()=>{
 const f=fixture();f.manager.start();f.active='lp-workspace-a';f.manager.record();f.manager.record();assert.equal(f.entries.length,2)
 f.active='lp-workspace-b';f.manager.record();f.windows.delete('lp-workspace-a');f.back();assert.equal(f.active,'');assert.equal(f.windows.has('lp-workspace-a'),false)
})
test('history never stores scope or draft content and does not trap external history',()=>{
 const f=fixture();f.manager.start();f.active='lp-workspace-a';f.manager.record()
 assert.deepEqual(f.host.history.state.devopsWorkspace,{version:1,name:'lp-workspace-a'})
 f.listeners.get('popstate')({state:null});assert.equal(f.active,'lp-workspace-a')
 f.allowed=false;f.back();assert.equal(f.active,'lp-workspace-a')
 f.manager.dispose();assert.equal(f.listeners.has('popstate'),false)
})
test('child traversal cannot activate an unrelated frame or bypass login',()=>{
 const f=fixture();f.manager.start();f.active='lp-workspace-a';f.manager.record();f.active='lp-workspace-b'
 f.manager.fromFrame('lp-workspace-b');assert.equal(f.active,'lp-workspace-b')
 f.manager.fromFrame('lp-workspace-a');assert.equal(f.active,'lp-workspace-a')
 f.allowed=false;f.active='lp-workspace-b';f.manager.fromFrame('lp-workspace-a');assert.equal(f.active,'lp-workspace-b')
})
