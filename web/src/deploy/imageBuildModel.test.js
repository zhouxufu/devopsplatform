import {test} from 'node:test'
import assert from 'node:assert/strict'
import {defaultJavaImage,dockerStep,imageCompiler,setImageCompiler,selectImageDockerfile,imageBuildError} from './imageBuildModel.js'
const app=()=>defaultJavaImage({name:'fixture',image_name:'fixture-image',artifact_type:'image'})
test('Java image includes a compiler before Docker and a real output glob',()=>{
 const s=app();assert.equal(imageCompiler(s),'maven');assert.deepEqual(s.steps.map(s=>s.step),['git-checkout','java-build','docker-build']);assert.equal(dockerStep(s).params.DOCKERFILE_ARTIFACT_PATH,'target/*.jar');assert.equal(imageBuildError(s),'')
})
test('Gradle selection links the compiler output, preserving image options',()=>{
 const s=app();dockerStep(s).params.PUSH='false';setImageCompiler(s,'gradle');assert.equal(s.gradle_tasks,'clean build');assert.equal(dockerStep(s).params.DOCKERFILE_ARTIFACT_PATH,'build/libs/*.jar');assert.equal(dockerStep(s).params.PUSH,'false');assert.equal(s.steps.filter(s=>s.step==='java-build').length,1)
})
test('self-contained Dockerfile does not add a second compiler',()=>{
 const s=app();selectImageDockerfile(s,'repository');setImageCompiler(s,'dockerfile');assert.deepEqual(s.steps.map(s=>s.step),['git-checkout','docker-build']);assert.equal(imageBuildError(s),'');assert.equal(imageCompiler(s),'dockerfile')
})
test('switching runtime template selects the matching compiler',()=>{
 const s=app();selectImageDockerfile(s,'template','nginx');assert.equal(imageCompiler(s),'node');assert.equal(s.build_cmd,'npm ci && npm run build');assert.equal(dockerStep(s).params.DOCKERFILE_ARTIFACT_PATH,'dist');selectImageDockerfile(s,'template','java21');assert.equal(imageCompiler(s),'maven');assert.equal(dockerStep(s).params.DOCKERFILE_ARTIFACT_PATH,'target/*.jar')
})
test('broken old template is not silently treated as a multi-stage Dockerfile',()=>{
 const s={image_name:'image',build_tool:'command',steps:[{step:'docker-build',params:{DOCKERFILE_SOURCE:'template',DOCKERFILE_TEMPLATE:'java21',DOCKERFILE_ARTIFACT_PATH:'app.jar'}}]};assert(imageBuildError(s));setImageCompiler(s,'maven');assert.equal(imageBuildError(s),'');assert.equal(s.steps[0].step,'java-build')
})
test('custom command requires input; unrelated scripts are retained',()=>{
 const s=app();s.steps.splice(1,0,{step:'shell-script',params:{SCRIPT:'echo keep'}});setImageCompiler(s,'java-command');assert(imageBuildError(s));s.build_cmd='./gradlew clean bootJar';assert.equal(imageBuildError(s),'');assert(s.steps.some(s=>s.step==='shell-script'))
})
test('multiple advanced compilers require explicit manual editing',()=>{
 const s=app();s.steps.splice(1,0,{step:'java-build',params:{COMMAND:'keep'}});const before=JSON.stringify(s);assert.throws(()=>setImageCompiler(s,'gradle'));assert.equal(JSON.stringify(s),before)
})
test('changing Java tool does not move scripts ahead of their compiler',()=>{
 const s=app();s.steps.splice(2,0,{step:'shell-script',params:{SCRIPT:'check jar'}});setImageCompiler(s,'gradle');assert.deepEqual(s.steps.map(s=>s.step),['git-checkout','java-build','shell-script','docker-build'])
})
