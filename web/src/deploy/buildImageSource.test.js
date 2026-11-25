import test from 'node:test'
import assert from 'node:assert/strict'
import {buildImageSourceError} from './buildImageSource.js'
const base={mode:'container',preset:'custom',image:'gis-builder:v1',image_source:'dockerfile',dockerfile:'deploy/jpom/Dockerfile.build',context:'.'}
test('Dockerfile image source requires explicit in-repository paths and writable tag',()=>{
  assert.equal(buildImageSourceError(base),'')
  for(const patch of [{dockerfile:''},{dockerfile:'../Dockerfile'},{dockerfile:'/etc/Dockerfile'},{dockerfile:'.git/config'},{dockerfile:'.'},{dockerfile:'file\nDockerfile'},{context:'../outside'},{context:'/host'},{context:'.git'},{image:'image@sha256:abc'}])assert.notEqual(buildImageSourceError({...base,...patch}),'',JSON.stringify(patch))
  assert.equal(buildImageSourceError({...base,context:''}),'')
  assert.equal(buildImageSourceError({...base,context:'deploy/jpom'}),'')
})
test('legacy registry and local configurations do not require a Dockerfile',()=>{
  assert.equal(buildImageSourceError({mode:'local'}),'')
  assert.equal(buildImageSourceError({mode:'container',preset:'gradle-jdk21'}),'')
  assert.equal(buildImageSourceError({...base,image_source:undefined,dockerfile:''}),'')
  assert.equal(buildImageSourceError({...base,image_source:'registry',dockerfile:'../inactive'}),'')
  assert.notEqual(buildImageSourceError({...base,image:''}),'')
})
