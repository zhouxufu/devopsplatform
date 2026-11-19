import test from 'node:test'
import assert from 'node:assert/strict'
import {filterResourceConnections as filter,resourceKindLabels,hasConnectionParameters,connectionDatabaseLabel,instanceConnectionConfig} from './resourceConnections.js'
const rows=[{name:'shared-test',kind:'mysql',endpoint:'db.test:3306',config:{database:'orders',secret:'hidden-value'}},{name:'shared-prod',kind:'mysql',endpoint:'db.prod:3306',config:{database:'orders'}},{name:'cache',kind:'redis',endpoint:'cache:6379'}]
test('legacy and new connections share the instance-only editor contract',()=>{
 const connections=[
  {name:'AI测试库',kind:'mysql',endpoint:'192.0.2.29:3306',config:{database:'test_estate_ai'}},
  {name:'dev_estate_auth',kind:'mysql',endpoint:'192.0.2.24:3306',config:{database:'dev_estate_auth'}},
  {name:'test_estate_auth',kind:'mysql',endpoint:'192.0.2.7:3306',config:{database:'test_estate_auth'}}
 ]
 const before=structuredClone(connections)
 assert.deepEqual(connections.map(connectionDatabaseLabel),['绑定时选库','绑定时选库','绑定时选库'])
 for(const c of connections)assert.deepEqual(instanceConnectionConfig(c.kind,c.config),{})
 assert.deepEqual(instanceConnectionConfig('mysql',{database:'old',tls:'false'}),{tls:'false'})
 assert.deepEqual(instanceConnectionConfig('redis',{db:'3'}),{db:'3'})
 assert.deepEqual(connections,before)
 assert.equal(connectionDatabaseLabel({kind:'mysql',config:{}}),'绑定时选库')
 assert.equal(connectionDatabaseLabel({kind:'mysql',config:{database:'  '}}),'绑定时选库')
 assert.equal(connectionDatabaseLabel({kind:'redis',config:{db:'3'}}),'DB 3')
 assert.equal(connectionDatabaseLabel({kind:'git'}),'—')
})
test('parameter tab is available only for types with parameter fields',()=>{
 for(const kind of ['mysql','redis','nacos','zentao','maven','aliyun-sae'])assert.equal(hasConnectionParameters(kind),true,kind)
 for(const kind of ['git','docker','',undefined,'future-plugin'])assert.equal(hasConnectionParameters(kind),false,String(kind))
})
test('generic database label, searchable public fields, no credential search',()=>{
 assert.equal(resourceKindLabels.mysql,'MySQL 数据库')
 assert.equal(filter(rows,['mysql','redis'],'','ORDERS').length,0)
 assert.equal(filter(rows,['mysql','redis'],'','db.prod').length,1)
 assert.equal(filter(rows,['mysql','redis'],'','hidden-value').length,0)
})
test('type filtering preserves plugin scope and distinct environment connections',()=>{
 assert.deepEqual(filter(rows,['mysql']).map(c=>c.name),['shared-test','shared-prod'])
 assert.equal(filter(rows,['mysql'],'redis').length,0)
 assert.equal(filter(rows,['mysql','redis'],'redis').length,1)
 assert.equal(rows.length,3)
})
