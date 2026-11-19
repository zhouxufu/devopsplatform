import {test} from 'node:test'
import assert from 'node:assert/strict'
import {validMavenThreads,mavenLockFailure} from './mavenBuild.js'
test('thread validation accepts Maven CPU multipliers and rejects injected options',()=>{
 for(const value of ['', '4','1C','2.5C','0.5C'])assert.equal(validMavenThreads(value),true,value)
 for(const value of ['0','0C','-1','1.5','1C -U','1C;true','$(true)','NaNC','1e2C'])assert.equal(validMavenThreads(value),false,value)
})
test('lock failure is distinct from repository warnings and locates the new options field',()=>{
 const warning='[WARNING] maven.ustc.edu.cn: Name or service not known'
 assert.equal(mavenLockFailure(warning),null)
 const issue=mavenLockFailure(warning+'\n[ERROR] java.lang.IllegalStateException: Could not acquire lock(s)')
 assert.equal(issue.repair,'maven-arguments')
 assert.match(issue.detail,/-e/)
 assert.match(issue.detail,/原快照重试不会使用新配置/)
 assert.ok(mavenLockFailure('Could not acquire exclusive lock for artifacts: example:a:1'))
})
