import {test} from 'node:test';
import assert from 'node:assert/strict';
import {scopeChanges,scopePayload,frozenChangeIDs} from './versionScope.js';
const changes=[{id:1,work_items:[{id:10}]},{id:2,work_items:[{id:10},{id:20}]},{id:3,work_items:[]}];
test('removing a demand preserves shared and manual changes',()=>{
 assert.deepEqual(scopeChanges(changes,[10,20],[2,3]).map(c=>c.id),[1,2,3]);
 assert.deepEqual(scopeChanges(changes,[20],[2,3]).map(c=>c.id),[2,3]);
 assert.deepEqual(scopeChanges(changes,[],[2,3]).map(c=>c.id),[2,3]);
});
test('draft versions do not lock editing; frozen versions do',()=>{
 assert.deepEqual([...frozenChangeIDs([{status:'draft',changes:[{id:1}]},{status:'ready',changes:[{id:2}]},{status:'completed',changes:[{id:2},{id:3}]}])],[2,3]);
 assert.deepEqual([...frozenChangeIDs(null)],[]);
});
test('empty scope and deduplicated explicit selections',()=>{
 assert.deepEqual(scopeChanges(null,null,null),[]);
 assert.deepEqual(scopePayload({version:'v1',title:'版本'},[10,10],[2,2]),{version:'v1',title:'版本',description:'',work_item_ids:[10],manual_change_ids:[2]});
});
