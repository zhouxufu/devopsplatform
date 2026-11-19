import test from 'node:test'
import assert from 'node:assert/strict'
import {metricNumber,formatMetric,sumMetrics,formatMetricBytes} from './observedMetrics.js'

test('missing and invalid telemetry stays unknown while observed zero stays zero',()=>{
 for(const value of [undefined,null,'','  ',NaN,Infinity,-1,{},true]){
  assert.equal(metricNumber(value),null)
  assert.equal(formatMetric(value,'%'),'—')
  assert.equal(formatMetricBytes(value),'—')
 }
 assert.equal(formatMetric(0,'%'),'0%')
 assert.equal(formatMetric('12.5',' KB/s'),'12.5 KB/s')
 assert.equal(formatMetricBytes(0),'0 B')
})
test('incomplete aggregate telemetry does not claim an empty queue',()=>{
 assert.equal(sumMetrics([0,0]),0)
 assert.equal(sumMetrics([]),0)
 assert.equal(sumMetrics([1024,undefined]),null)
 assert.equal(sumMetrics(null),null)
 assert.equal(sumMetrics([1024,'2048']),3072)
})
test('byte quantities remain readable across small and large storage sizes',()=>{
 assert.equal(formatMetricBytes(1024),'1.0 KB')
 assert.equal(formatMetricBytes(1),'1 B')
 assert.equal(formatMetricBytes(1024**4),'1.0 TB')
 assert.equal(formatMetricBytes(1536),'1.5 KB')
})
