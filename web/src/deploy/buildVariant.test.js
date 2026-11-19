import {test} from 'node:test'
import assert from 'node:assert/strict'
import {normalizeBuildVariant,buildVariantLabel,environmentBuildBranch} from './buildVariant.js'
test('variant normalization keeps unconfigured distinct from explicit common',()=>{
 assert.deepEqual(normalizeBuildVariant(null),{})
 assert.deepEqual(normalizeBuildVariant({name:' '}),{})
 assert.equal(normalizeBuildVariant({name:' shared '}).name,'shared')
 assert.equal(normalizeBuildVariant({maven_profiles:' dev '}).maven_profiles,'dev')
})
test('common Maven execution options do not become environment variables',()=>{
 assert.deepEqual(normalizeBuildVariant({maven_threads:'1C',maven_update_snapshots:true}),{})
})
test('environment build uses its branch without copying runtime variables',()=>{
 const value={build_variant:{name:'test'},code_policy:{mode:'integration',target_branch:'test-release',base_branch:'main'},runtime_env:{SPRING_PROFILES_ACTIVE:'prod'}}
 const before=JSON.stringify(value)
 assert.equal(environmentBuildBranch(value).branch,'test-release')
 assert.equal(JSON.stringify(value),before)
 assert.equal(environmentBuildBranch({...value,code_policy:{mode:'integration',target_branch:'dev'}}).branch,'dev')
 assert.equal(environmentBuildBranch({...value,build_variant:{}}),null)
})
test('missing branch and unresolved isolated branch never fall back to common or baseline',()=>{
 assert.equal(environmentBuildBranch({build_variant:{name:'prod'},code_policy:{mode:'isolated',base_branch:'main',target_branch:'release/{version}'}}).branch,'')
 assert.equal(environmentBuildBranch({build_variant:{name:'prod'}}).branch,'')
 assert.equal(environmentBuildBranch({build_variant:{name:'prod'},code_policy:{mode:'isolated',target_branch:'release/1.2'}}).branch,'release/1.2')
})
test('labels are display only, no guessing environment from artifact version',()=>{
 assert.equal(buildVariantLabel({version:'prod',metadata:{}}),'未记录环境方案')
 assert.equal(buildVariantLabel({metadata:{build_variant:'test'}}),'test')
})
