import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {navigationGroups,navigationLeaves,navigationOpenKeys,availableGroups,selectedNavigation} from './navigation.js'
const originalDestinations=[{"id":"work","plugin":"research","hash":"#/work"},{"id":"versions","plugin":"research","hash":"#/versions"},{"id":"research-settings","plugin":"research","hash":"#/settings"},{"id":"build","plugin":"deploy","hash":"#/"},{"id":"orchestration","plugin":"deploy","hash":"#/orchestration?area=runs"},{"id":"orchestration-templates","plugin":"deploy","hash":"#/orchestration?area=templates"},{"id":"changes","plugin":"deploy","hash":"#/changes?tab=changes"},{"id":"api-documents","plugin":"api-center","hash":"#/documents"},{"id":"api-test-cases","plugin":"api-center","hash":"#/test-cases"},{"id":"api-testing","plugin":"api-center","hash":"#/test-execution"},{"id":"dbms","plugin":"dbms","hash":"#/query"},{"id":"redis","plugin":"redis","hash":"#/query"},{"id":"sql-change","plugin":"dbms","hash":"#/operations"},{"id":"sql-history","plugin":"dbms","hash":"#/history"},{"id":"sync","plugin":"dbms","hash":"#/sync"},{"id":"redis-change","plugin":"redis","hash":"#/operations"},{"id":"redis-history","plugin":"redis","hash":"#/history"},{"id":"logs","plugin":"logs","hash":"#/search"},{"id":"live","plugin":"logs","hash":"#/live"},{"id":"agents","plugin":"logs","hash":"#/status"},{"id":"subs","plugin":"logs","hash":"#/subs"},{"id":"monitor","plugin":"monitor","hash":"#/"},{"id":"alerts","plugin":"monitor","hash":"#/alerts"},{"id":"machines","plugin":"monitor","hash":"#/machines"},{"id":"api","plugin":"monitor","hash":"#/api"},{"id":"jvm","plugin":"monitor","hash":"#/jvm"},{"id":"trace","plugin":"monitor","hash":"#/trace"},{"id":"rules","plugin":"monitor","hash":"#/rules"}]
originalDestinations.push({id:'api-reports',plugin:'api-center',hash:'#/reports'})
originalDestinations.push({id:'arthas',plugin:'monitor',hash:'#/arthas'})
test('menu organization never changes leaf identity, plugin ownership or destination',()=>{
 const sorted=items=>[...items].sort((a,b)=>a.id.localeCompare(b.id))
 assert.deepEqual(sorted(navigationLeaves().map(({id,plugin,hash})=>({id,plugin,hash}))),sorted(originalDestinations))
 for(const item of originalDestinations)assert.equal(selectedNavigation(item.plugin,item.hash),item.id)
})
test('database and Redis actions are contiguous and owned by their own capability',()=>{
 const data=availableGroups([{name:'dbms'},{name:'redis'}])[0]
 assert.deepEqual(data.items.map(s=>s.label),['数据库','Redis'])
 assert.deepEqual(data.items[0].items.map(s=>s.id),['dbms','sql-change','sql-history','sync'])
 assert.deepEqual(data.items[1].items.map(s=>s.id),['redis','redis-change','redis-history'])
 for(const [plugin,label]of [['dbms','数据库'],['redis','Redis']]){const sections=availableGroups([{name:plugin}])[0].items;assert.equal(sections.length,1);assert.equal(sections[0].label,label);assert.ok(navigationLeaves(sections).every(s=>s.plugin===plugin))}
})
test('section headings do not introduce another expandable navigation level',()=>{
 for(const group of navigationGroups)for(const entry of group.items)if(entry.items)assert.ok(entry.items.every(item=>!item.items))
 for(const [leaf,parent]of [['sql-history','data'],['redis-history','data'],['build','delivery'],['changes','delivery'],['orchestration','delivery'],['rules','monitoring'],['agents','logging']])assert.deepEqual(navigationOpenKeys(leaf),[parent])
 assert.deepEqual(navigationOpenKeys('resources'),['management'])
 assert.deepEqual(navigationOpenKeys('home'),[])
 assert.deepEqual(navigationOpenKeys('dbms',availableGroups([{name:'redis'}])),[])
})
test('visible primary menu icons resolve to distinct official glyphs',()=>{
 const source=readFileSync(new URL('../shared/components/UiIcon.vue',import.meta.url),'utf8')
 const aliases=Object.fromEntries(source.match(/const icons=\{([^}]+)\}/)[1].split(',').map(pair=>pair.trim().match(/^['"]?([^:'"]+)['"]?:([A-Za-z0-9_]+)$/)).filter(Boolean).map(([,key,glyph])=>[key,glyph]))
 const names=['user',...navigationGroups.map(g=>g.icon),'plugins','settings'].map(key=>aliases[key])
 assert.ok(names.every(Boolean));assert.equal(new Set(names).size,names.length)
 assert.equal(aliases.research,'UsersRoundIcon');assert.equal(aliases.deploy,'SendIcon');assert.equal(aliases['api-center'],'BracesIcon');assert.equal(aliases.logs,'LogsIcon')
 assert.equal(aliases['sidebar-open'],'PanelLeftOpenIcon')
})
