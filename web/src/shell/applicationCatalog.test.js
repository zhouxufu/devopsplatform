import {test} from 'node:test'
import assert from 'node:assert/strict'
import {currentApplications} from './applicationCatalog.js'

test('current picker excludes deleted apps even with stale deployment metadata', () => {
  const catalog = [
    {name:'deleted', application_id:'old', status:'archived'},
    {name:'logs-only', status:'discovered', display_name:'日志应用'},
    {name:'orders', status:'managed', display_name:'订单'},
  ]
  const result = currentApplications(catalog, [{name:'deleted'}, {name:'orders'}, {name:'history-only'}])
  assert.deepEqual(result.map(app=>[app.name,app.managed]), [['logs-only',false],['orders',true]])
  assert.equal(result[1].display_name, '订单')
  assert.equal(catalog[0].status, 'archived')
  assert.equal(Object.hasOwn(catalog[2], 'managed'), false)
})

test('empty current catalog stays empty despite historical deployment references', () => {
  assert.deepEqual(currentApplications([], [{name:'deleted'}]), [])
  assert.deepEqual(currentApplications([{name:'telemetry',status:'discovered'}]), [{name:'telemetry',status:'discovered',managed:false}])
})
