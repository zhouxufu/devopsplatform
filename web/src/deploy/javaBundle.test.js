import {test} from 'node:test'
import assert from 'node:assert/strict'
import {bundleParams,updateBundleParams,javaBundleError} from './javaBundle.js'
test('complete bundle settings retain the build pipeline and required GIS files',()=>{
 const service={artifact_type:'java-bundle',artifact_glob:'build/jpom-release',build_cmd:'bash deploy/jpom/build-release.sh',steps:[{step:'git-checkout',params:{}},{step:'shell-script',params:{SCRIPT:'existing trusted build'}}]}
 updateBundleParams(service,{MAIN_JAR:'lib/estate-gis-service.jar',REQUIRED_PATHS:'bin/estate-gis-service.sh\ntools/changchun-traffic'})
 assert.equal(javaBundleError(service),'')
 assert.equal(service.steps[1].params.SCRIPT,'existing trusted build')
 service.artifact_glob='build/another-release'
 assert.equal(bundleParams(service).ROOT,'build/another-release')
 assert.equal(bundleParams(service).MAIN_JAR,'lib/estate-gis-service.jar')
 for(const bad of ['../outside','/absolute','lib/../../escape.jar','lib/*.jar','lib/a\u0000.jar']){updateBundleParams(service,{MAIN_JAR:bad});assert.notEqual(javaBundleError(service),'')}
})
test('bundle selection creates compilation then archive and does not rewrite jar applications',()=>{
 const s={artifact_type:'java-bundle',artifact_glob:'build/release',build_cmd:'bash build.sh'}
 updateBundleParams(s,{MAIN_JAR:'lib/app.jar'})
 assert.deepEqual(s.steps.map(s=>s.step),['git-checkout','java-build','package-java-bundle'])
 assert.equal(javaBundleError({artifact_type:'jar'}),'')
})
