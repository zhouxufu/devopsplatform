import test from 'node:test'
import assert from 'node:assert/strict'
import {installWorkspaceRouterSync,installWorkspaceNavigation} from './workspaceNavigation.js'
test('only committed router navigations notify the owning console, and unmount removes the hook',()=>{
 const messages=[];let after,removed=0,mixin
 const app={config:{globalProperties:{$router:{afterEach(fn){after=fn;return ()=>removed++}}}},mixin(v){mixin=v}}
 let pop
 const host={location:{origin:'http://fixture'},parent:{postMessage:(...args)=>messages.push(args)},addEventListener(name,fn){pop=fn},removeEventListener(){pop=null}}
 installWorkspaceRouterSync(app,host)
 const root={};root.$root=root
 const child={$root:root};mixin.mounted.call(child);assert.equal(messages.length,0)
 mixin.mounted.call(root);assert.deepEqual(messages,[[{type:'workspace.route'},'http://fixture']])
 // Both a pushed detail and its return/list replacement must update the shell.
 after({fullPath:'/svc/orders/build'},{fullPath:'/changes?item=9'},undefined)
 after({fullPath:'/'},{fullPath:'/svc/orders/build'},undefined)
 assert.equal(messages.length,3)
 after({}, {}, {type:4});assert.equal(messages.length,3)
 pop();after({}, {}, undefined);assert.deepEqual(messages.at(-1),[{type:'workspace.history'},'http://fixture'])
 const count=messages.length;pop();after({}, {}, {type:4});assert.equal(messages.length,count)
 mixin.unmounted.call(child);assert.equal(removed,0);mixin.unmounted.call(root);assert.equal(removed,1);assert.equal(pop,null)
})
test('standalone workbenches and non-router applications send no route messages',()=>{
 let mixin,notifications=0
 const host={location:{origin:'http://fixture'},parent:{postMessage(){notifications++}},addEventListener(){},removeEventListener(){}}
 const app={config:{globalProperties:{}},mixin(v){mixin=v}},root={};root.$root=root
 installWorkspaceRouterSync(app,host);mixin.mounted.call(root);assert.equal(notifications,0)
 host.parent=host;app.config.globalProperties.$router={afterEach(){return ()=>{}}};mixin.mounted.call(root);assert.equal(notifications,0)
})

test('a cross-tab destination replaces only its newly created shell entry and keeps router guards',()=>{
 const calls=[],listeners=new Map(),parent={},host={parent,location:{origin:'http://fixture'},addEventListener:(key,fn)=>listeners.set(key,fn),removeEventListener(){},dispatchEvent:()=>true}
 parent.postMessage=()=>{}
 let mixin;const router={push:hash=>calls.push(['push',hash]),replace:hash=>calls.push(['replace',hash]),afterEach:()=>()=>{}}
 const app={config:{globalProperties:{$router:router}},mixin(value){mixin=value}},root={};root.$root=root
 installWorkspaceRouterSync(app,host);mixin.mounted.call(root);installWorkspaceNavigation(host)
 const send=replace=>listeners.get('message')({source:parent,origin:host.location.origin,data:{type:'workspace.navigate',hash:'#/documents?snapshot=7',replace}})
 send(true);send(false)
 assert.deepEqual(calls,[['replace','/documents?snapshot=7'],['push','/documents?snapshot=7']])
 host.dispatchEvent=()=>false;send(true);assert.equal(calls.length,2)
})
