import test from 'node:test'
import assert from 'node:assert/strict'
import {installWorkspaceRouterSync} from './workspaceNavigation.js'
test('only committed router navigations notify the owning console, and unmount removes the hook',()=>{
 const messages=[];let after,removed=0,mixin
 const app={config:{globalProperties:{$router:{afterEach(fn){after=fn;return ()=>removed++}}}},mixin(v){mixin=v}}
 const host={location:{origin:'http://fixture'},parent:{postMessage:(...args)=>messages.push(args)}}
 installWorkspaceRouterSync(app,host)
 const root={};root.$root=root
 const child={$root:root};mixin.mounted.call(child);assert.equal(messages.length,0)
 mixin.mounted.call(root);assert.deepEqual(messages,[[{type:'workspace.route'},'http://fixture']])
 // Both a pushed detail and its return/list replacement must update the shell.
 after({fullPath:'/svc/orders/build'},{fullPath:'/changes?item=9'},undefined)
 after({fullPath:'/'},{fullPath:'/svc/orders/build'},undefined)
 assert.equal(messages.length,3)
 after({}, {}, {type:4});assert.equal(messages.length,3)
 mixin.unmounted.call(child);assert.equal(removed,0);mixin.unmounted.call(root);assert.equal(removed,1)
})
test('standalone workbenches and non-router applications send no route messages',()=>{
 let mixin,notifications=0
 const host={location:{origin:'http://fixture'},parent:{postMessage(){notifications++}}}
 const app={config:{globalProperties:{}},mixin(v){mixin=v}},root={};root.$root=root
 installWorkspaceRouterSync(app,host);mixin.mounted.call(root);assert.equal(notifications,0)
 host.parent=host;app.config.globalProperties.$router={afterEach(){return ()=>{}}};mixin.mounted.call(root);assert.equal(notifications,0)
})
