import test from 'node:test'
import assert from 'node:assert/strict'
import {artifactCodePresentation} from './artifactPresentation.js'
test('prepared artifacts show the frozen target alongside the exact checkout, not current defaults',()=>{
 const commit='a'.repeat(40)
 assert.deepEqual(artifactCodePresentation({metadata:{branch:commit},code_origin:{target_branch:'shared-dev',source_branches:['feature/work'],commit,environment:'dev'}}),{branch:'shared-dev',sources:['feature/work'],commit,environment:'dev',prepared:true})
 assert.equal(artifactCodePresentation({metadata:{branch:commit}}).branch,'分支未记录')
 assert.equal(artifactCodePresentation({metadata:{branch:commit}}).commit,commit)
 assert.equal(artifactCodePresentation({metadata:{branch:'release/2026'}}).branch,'release/2026')
 assert.equal(artifactCodePresentation({metadata:{},branch:'current-default'}).branch,'分支未记录')
})
