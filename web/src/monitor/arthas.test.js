import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {arthasQuickCommands,arthasNeedsConfirmation,arthasScopeChanged,arthasStateLabel,appendArthasOutput,matchesArthasSample} from './arthas.js'
import {navigationLeaves,availableGroups,selectedNavigation,workspaceNavigation} from '../shell/navigation.js'
test('Arthas belongs to monitoring, with its own stable workspace title and route',()=>{
 const group=availableGroups([{name:'monitor'}])[0]
 const item=navigationLeaves([group]).find(i=>i.id==='arthas');assert.equal(item.hash,'#/arthas');assert.equal(item.plugin,'monitor');assert.equal(item.icon,'arthas')
 assert.equal(selectedNavigation('monitor','#/arthas'),'arthas');assert.equal(workspaceNavigation({plugin:{name:'monitor'},hash:'#/arthas'}).label,'Arthas 诊断')
 assert.equal(navigationLeaves(availableGroups([{name:'deploy'}])).some(i=>i.id==='arthas'),false)
})
test('non-exact quick commands always require explicit confirmation',()=>{
 for(const command of arthasQuickCommands)assert.equal(arthasNeedsConfirmation(command),false)
 for(const command of ['ognl @java.lang.System@exit(0)','stop','heapdump /tmp/a','version;stop','thread -n 50','help | grep secret'])assert.equal(arthasNeedsConfirmation(command),true)
})
test('scope mismatches freeze command submission without relabeling the existing session',()=>{
 const target={service:'orders',environment:'test'}
 assert.equal(arthasScopeChanged(target,'billing','test'),true);assert.equal(arthasScopeChanged(target,'orders','prod'),true)
 assert.equal(arthasScopeChanged(target,'orders','test'),false);assert.equal(arthasScopeChanged(target,'',''),false)
 assert.equal(arthasStateLabel('unknown'),'结果待核对');assert.notEqual(arthasStateLabel('timeout'),'已完成')
})
test('diagnostic samples must match the exact selected instance, scope and revision',()=>{
 const target={id:'one',service:'orders',environment:'test',revision:2}
 assert.equal(matchesArthasSample({target:{...target}},target),true)
 for(const change of [{id:'two'},{service:'billing'},{environment:'prod'},{revision:3},{revision:'2'}])assert.equal(matchesArthasSample({target:{...target,...change}},target),false)
 assert.equal(matchesArthasSample({},target),false);assert.equal(matchesArthasSample(null,target),false)
})
test('console history is bounded and output is rendered as text, not executable HTML',()=>{
 let history=[];for(let i=0;i<50;i++)history=appendArthasOutput(history,{id:i});assert.equal(history.length,20);assert.equal(history[0].id,30)
 const source=readFileSync(new URL('./pages/ArthasView.vue',import.meta.url),'utf8')
 assert.doesNotMatch(source,/v-html|localStorage|sessionStorage|<iframe|async_exec|setInterval/)
 assert.match(source,/<pre v-if="item.output">\{\{item.output\}\}<\/pre>/)
 assert.match(source,/current!==generation/);assert.match(source,/password.value=''/);assert.match(source,/role==='admin'/)
 assert.match(source,/api\('sessions\/'.*'DELETE'/);assert.match(source,/onBeforeUnmount/)
})

import {arthasDiagnosticSections,arthasDiagnosticHighlights} from './arthas.js'
test('diagnostic rendering groups actual values including zero, missing and unknown metrics',()=>{
 const result={diagnostic:'overview',rows:[
  {section:'RUNTIME 1',label:'JVM-VERSION',value:'21.0.6'},
  {section:'THREAD 1',label:'THREAD-COUNT',value:0},
  {section:'THREAD 1',label:'futureMetric',value:'<script>text only</script>'},
  {section:'内存 / HEAP 1',label:'used',value:'128.00 MiB'},
  {section:'内存 / HEAP 1',label:'max',value:null},
 ]}
 const groups=arthasDiagnosticSections(result)
 assert.equal(groups.length,3)
 assert.equal(groups[0].label,'运行时')
 assert.deepEqual(groups[1].rows.map(r=>[r.label,r.value]),[['线程数','0'],['futureMetric','<script>text only</script>']])
 assert.equal(groups[2].label,'堆内存 1');assert.equal(groups[2].rows[1].value,'未返回')
 assert.deepEqual(arthasDiagnosticHighlights(result),[{label:'JVM 版本',value:'21.0.6'},{label:'线程数',value:'0'}])
 assert.deepEqual(arthasDiagnosticSections({rows:[]}),[])
 assert.deepEqual(arthasDiagnosticHighlights({diagnostic:'blocked_threads',rows:[]}),[])
})
test('thread samples remain distinct and memory units are not converted twice',()=>{
 const sections=arthasDiagnosticSections({rows:[{section:'busyThreads 1',label:'id',value:'42'},{section:'busyThreads 2',label:'id',value:'43'},{section:'内存 / NON_HEAP 1',label:'used',value:'64.00 MiB'}]})
 assert.equal(sections.length,3)
 assert.equal(sections[0].label,'热点线程 1')
 assert.equal(sections[2].rows[0].value,'64.00 MiB')
})

import {arthasDiagnosticBars} from './arthas.js'
test('memory plots use observed percentages or compatible byte units, never missing limits',()=>{
 const rows=[{section:'HEAP 1',label:'used',value:'128.00 MiB'},{section:'HEAP 1',label:'max',value:'0.50 GiB'},{section:'NON_HEAP 1',label:'used',value:'64.00 MiB'},{section:'NON_HEAP 1',label:'max',value:'未设置'}]
 assert.equal(arthasDiagnosticBars({diagnostic:'memory',rows}).length,1)
 assert.equal(arthasDiagnosticBars({diagnostic:'memory',rows})[0].percent,25)
 assert.equal(arthasDiagnosticBars({diagnostic:'memory',rows:[{section:'zero',label:'usage',value:'0%'}]})[0].percent,0)
 for(const value of ['',null,'NaN','-1','not measured'])assert.deepEqual(arthasDiagnosticBars({diagnostic:'memory',rows:[{section:'bad',label:'usage',value}]}),[])
})
test('CPU ranking preserves thread identity and sorts actual measurements without a fake time series',()=>{
 const rows=[{section:'busyThreads 1',label:'name',value:'slow'},{section:'busyThreads 1',label:'cpu',value:'0.1'},{section:'busyThreads 2',label:'name',value:'busy'},{section:'busyThreads 2',label:'cpuUsage',value:'12.8'}]
 assert.deepEqual(arthasDiagnosticBars({diagnostic:'hot_threads',rows}).map(b=>[b.label,b.percent]),[['busy',12.8],['slow',0.1]])
 assert.deepEqual(arthasDiagnosticBars({diagnostic:'overview',rows}),[])
})
