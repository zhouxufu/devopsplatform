import test from 'node:test'
import assert from 'node:assert/strict'
import {executableMode,executableOptions,executableSpec,buildRepairLocation,currentBuildLocation} from './buildExecutable.js'
test('Maven and Gradle share explicit execution choices without changing persisted values',()=>{
 for(const tool of ['maven','gradle']){
  const s=executableSpec(tool)
  assert.deepEqual(executableOptions(tool).map(o=>o.value),['auto',s.wrapper,s.system,'custom'])
  for(const v of ['auto',s.wrapper,s.system])assert.equal(executableMode(tool,v),v)
  assert.equal(executableMode(tool,s.path),'custom')
  assert.equal(executableMode(tool,''),'auto')
 }
})
test('repair opens exact configuration section and current builds are separate from snapshot retry',()=>{
 assert.deepEqual(buildRepairLocation('app / 1',true),{path:'/svc/app%20%2F%201/settings',query:{settings_section:'command',settings_return:'build',build_repair:'gradle'}})
 assert.deepEqual(currentBuildLocation('app'),{path:'/svc/app/build',query:{prepare_build:'1'}})
 assert.equal(buildRepairLocation('app').query.build_repair,undefined)
 assert.equal(buildRepairLocation('app','maven-arguments').query.build_repair,'maven-arguments')
})
