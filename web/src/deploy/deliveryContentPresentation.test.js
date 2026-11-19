import test from 'node:test'
import assert from 'node:assert/strict'
import {resourceContentGroups,sqlContentSelection,resourceContentSummary,nodeContentTargets,resourceChangePreview} from './deliveryContentPresentation.js'

test('mixed scope keeps resources of a published app and pure resource scope has no artifact items',()=>{
  const changes=[{id:1,kind:'code',service:'api'},{id:2,kind:'artifact',service:'web'},
    {id:3,kind:'nacos',service:'api'},{id:4,kind:'mysql',service:'api'},{id:5,kind:'redis',service:'cache'},{id:6,kind:'manual'}]
  const mixed=resourceContentGroups(changes)
  assert.deepEqual(mixed.map(group=>[group.kind,group.changes.map(change=>change.id)]),[['nacos',[3]],['mysql',[4]],['redis',[5]],['manual',[6]]])
  assert.deepEqual(resourceContentGroups(changes.slice(2)),mixed)
  assert.equal(changes.length,6)
})

test('SQL display selects exact environment override and never presents a skipped default as executable',()=>{
  const change={sql:{content:'UPDATE menu SET enabled=1 WHERE id=1;',environment_overrides:[
    {environment:'test',content:'UPDATE menu SET enabled=1 WHERE id=2;',reason:'测试菜单'},
    {environment:'prod',skip:true,reason:'生产已处理'}]}}
  const before=JSON.stringify(change)
  assert.equal(sqlContentSelection(change,'dev').content,change.sql.content)
  assert.equal(sqlContentSelection(change,'test').content,change.sql.environment_overrides[0].content)
  assert.deepEqual(sqlContentSelection(change,'prod'),{label:'本环境无需执行',content:'',skip:true,reason:'生产已处理'})
  assert.equal(JSON.stringify(change),before)
  assert.equal(sqlContentSelection({},'test').missing,true)
})

test('node navigation follows kind, service and explicit scope, including a node without explicit IDs',()=>{
  const changes=[{id:1,kind:'mysql',service:'api'},{id:2,kind:'mysql',service:'other'},
    {id:3,kind:'nacos',service:'api'},{id:4,kind:'manual',service:'api'}]
  assert.deepEqual(nodeContentTargets({kind:'mysql',service:'api'},changes).changeIds,[1])
  assert.deepEqual(nodeContentTargets({kind:'mysql',change_ids:[2,3]},changes).changeIds,[2])
  assert.deepEqual(nodeContentTargets({kind:'mysql',service:'api',change_ids:[2]},changes).changeIds,[])
  assert.deepEqual(nodeContentTargets({kind:'approval'},changes).changeIds,[])
  assert.deepEqual(nodeContentTargets({kind:'manual'},changes).changeIds,[])
  assert.deepEqual(nodeContentTargets({kind:'manual',change_ids:[4]},changes).changeIds,[4])
  assert.deepEqual(nodeContentTargets({kind:'deploy',service:'api'},changes,['api','web']).applications,['api'])
  assert.deepEqual(nodeContentTargets({kind:'deploy'},changes,['api','web']).applications,['api','web'])
})

test('resource previews retain reviewed target identity while filtering other changes without mutation',()=>{
  const preview={nacos_plans:[{node_id:'n',configs:[{change_id:1,data_id:'actual-test.yaml'},{change_id:2}]}],
    redis_plans:[{node_id:'r',changes:[{change_id:3,db:'2'},{change_id:4}]}],
    sql_plans:[{node_id:'s',database:'test_menu',statements:[{change_id:5,sql:'SELECT 1'},{change_id:6}],
      script_selections:[{change_id:5,skip:true},{change_id:6}],applied:[{change_id:6}]},{node_id:'other',statements:[{change_id:7}]}]}
  const before=JSON.stringify(preview)
  assert.deepEqual(resourceChangePreview(preview,{id:1,kind:'nacos'})[0].configs,[{change_id:1,data_id:'actual-test.yaml'}])
  assert.deepEqual(resourceChangePreview(preview,{id:3,kind:'redis'})[0].changes,[{change_id:3,db:'2'}])
  const sql=resourceChangePreview(preview,{id:5,kind:'mysql'})
  assert.equal(sql.length,1);assert.equal(sql[0].database,'test_menu')
  assert.deepEqual(sql[0].statements,[{change_id:5,sql:'SELECT 1'}]);assert.deepEqual(sql[0].applied,[])
  assert.deepEqual(resourceChangePreview(null,{id:1,kind:'nacos'}),[])
  assert.equal(JSON.stringify(preview),before)
})

test('summaries preserve Nacos restore intent and ignore blank Redis command lines',()=>{
  assert.equal(resourceContentSummary({kind:'nacos',nacos:{restore:{run_id:1}}}), '恢复历史配置快照')
  assert.equal(resourceContentSummary({kind:'redis',redis:{commands:'TTL menu:key\r\n\r\nUNLINK menu:key\n'}}), '2 行命令')
})
