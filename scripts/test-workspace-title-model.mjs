import assert from 'node:assert/strict'
import {workspaceTitle} from '../web/src/shell/workspaceTitle.js'
const make=(name,hash,label='',environment='test')=>({plugin:{name,display:name},hash,label,context:{service:'inherited-app',environment}})
const work=make('research','#/work'),version=make('research','#/versions'),live=make('logs','#/live'),search=make('logs','#/search'),sync=make('dbms','#/sync')
assert.equal(workspaceTitle(work,[work,version]),'工作项')
assert.equal(workspaceTitle(version,[work,version]),'发布版本')
assert.equal(workspaceTitle(live,[live,search]),'实时日志')
assert.equal(workspaceTitle(search,[live,search]),'日志查询')
assert.equal(workspaceTitle(sync,[sync]),'数据迁移')
const first=make('research','#/work','工作项'),second=make('research','#/work','工作项')
assert.equal(workspaceTitle(first,[first,second]),'工作项 · 1')
assert.equal(workspaceTitle(second,[first,second]),'工作项 · 2')
for(const name of ['research','dbms','redis','deploy']){const a=make(name,'#/'),b=make(name,'#/');assert(!workspaceTitle(a,[a,b]).includes('inherited-app'));b.label='实际连接 / 目标';assert(workspaceTitle(b,[a,b]).includes(b.label))}
const a=make('logs','#/search'),b=make('logs','#/search','','prod')
assert.equal(workspaceTitle(a,[a,b]),'日志查询 · inherited-app · test')
assert.equal(workspaceTitle(b,[a,b]),'日志查询 · inherited-app · prod')
a.title='本人命名';assert.equal(workspaceTitle(a,[a,b]),'本人命名')
console.log('Workspace titles match leaf menus; duplicate targets and manual names remain distinct')
