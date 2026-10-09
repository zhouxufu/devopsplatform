import test from 'node:test';
import assert from 'node:assert/strict';
import {deliveryRows,expandDeliveryRows} from './deliveryContent.js';
test('one code input expands per application without rewriting existing shared content',()=>{
 const original={id:1,kind:'code',title:'code',service:'api',reference:'feature/menu',summary:'',risk_level:'medium',rollback_plan:'',owner:'alice',status:'draft',work_items:[{id:1},{id:2}]};
 const rows=deliveryRows([original],()=>true,[{name:'api',git_url:'repo'}]);rows[0].services.push('web');
 const output=expandDeliveryRows(rows,'需求');assert.equal(output.length,1);assert.equal(output[0].service,'web');assert.equal(output[0].id,0);
 rows[0].reference='feature/new';assert.equal(expandDeliveryRows(rows,'需求').length,2);
 rows[0].services=['web'];assert.throws(()=>expandDeliveryRows(rows,'需求'),/不能在此移除/);
});
test('ready, others and derived synchronization snapshots are read-only',()=>{
 const changes=[{id:1,kind:'manual',status:'ready'},{id:2,kind:'mysql',status:'draft',sql:{data_sync:{task_id:1}}},{id:3,kind:'manual',status:'draft',owner:'other'}];
 const rows=deliveryRows(changes,c=>c.owner!=='other');assert(rows.every(r=>r.readonly));assert.equal(expandDeliveryRows(rows,'需求').length,0);
});
test('different repositories do not silently group equal branch names',()=>{
 const changes=['a','b','c'].map((service,i)=>({id:i+1,service,kind:'code',status:'draft',reference:'main',owner:'me'}));
 const rows=deliveryRows(changes,()=>true,[{name:'a',git_url:'repo1'},{name:'b',git_url:'repo2'},{name:'c',git_url:'repo1'}]);assert.equal(rows.length,2);assert.deepEqual(rows[0].services,['a','c']);
});
test('Redis payload edits are preserved and other kinds cannot retain them',()=>{
 const change={id:9,kind:'redis',title:'cache',service:'api',status:'draft',redis:{resource_alias:'cache',commands:'TTL menu:key'}};
 const rows=deliveryRows([change],()=>true);assert.equal(expandDeliveryRows(rows,'需求').length,0);
 rows[0].redis.commands='UNLINK menu:key';const [edited]=expandDeliveryRows(rows,'需求');assert.equal(edited.redis.commands,'UNLINK menu:key');assert.equal(edited.sql,undefined);assert.equal(edited.nacos,undefined);
 rows[0].kind='manual';assert.equal(expandDeliveryRows(rows,'需求')[0].redis,undefined);
});
