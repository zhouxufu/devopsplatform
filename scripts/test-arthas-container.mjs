// Controlled Linux/JDK integration only. Never connects to a deployment host,
// mounts Docker's socket, publishes ports, or touches an existing container.
import {execFileSync} from 'node:child_process'
import {mkdtemp,readFile,writeFile,rm,mkdir} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {createHash,randomBytes} from 'node:crypto'
import path from 'node:path'
const image='gradle:8.14.3-jdk21',temp=await mkdtemp(path.join(tmpdir(),'arthas-container-'))
const native=process.env.ARTHAS_TEST_NATIVE==='1'
const name='devops-arthas-qa-'+randomBytes(6).toString('hex'),agent='/tmp/devops-arthas-qa',port=28463
let secret='',stage='setup'
const run=(...args)=>{stage=args[0]+(args.includes('arthas-boot.jar')?' attach':'');return execFileSync('docker',args,{encoding:'utf8',timeout:90000,stdio:['ignore','pipe','pipe']}).trim()}
try {
 const source=process.env.ARTHAS_TEST_ZIP||'/tmp/platform-arthas-4.0.5.zip'
 const zip=await readFile(source)
 if(createHash('sha256').update(zip).digest('hex')!=='7dcf453f6986e7bcb5af480ed0cbf5ec36791717cc7d032b58eae5dc16597b5f')throw Error('release checksum mismatch')
 await mkdir(temp+'/agent');execFileSync('unzip',['-q',source,'-d',temp+'/agent'])
 secret=randomBytes(32).toString('hex')
 await writeFile(temp+'/agent/arthas.properties',`arthas.config.overrideAll=true\narthas.ip=127.0.0.1\narthas.httpPort=${port}\narthas.telnetPort=-1\narthas.username=arthas\narthas.password=${secret}\narthas.localConnectionNonAuth=false\narthas.sessionTimeout=900\n`,{mode:0o600})
 await writeFile(temp+'/agent/http.conf',`user = "arthas:${secret}"\n`,{mode:0o600})
 if(native){
  const raw=await readFile('/tmp/jattach-linux-arm64-v22.tgz')
  if(createHash('sha256').update(raw).digest('hex')!=='288ae5ed87ee7fe0e608c06db5a23a096a6217c9878ede53c4e33710bdcaab51')throw Error('native release checksum mismatch')
  execFileSync('tar',['-xzf','/tmp/jattach-linux-arm64-v22.tgz','-C',temp+'/agent'])
 }
 const runtime=native?'jlink --add-modules java.se,jdk.unsupported,jdk.management,jdk.crypto.ec --output /tmp/fixture-jre && exec /tmp/fixture-jre/bin/java':'exec java'
 const start="mkdir -p /tmp/fixture; printf '%s' 'public class ArthasFixture { public static void main(String[] args) throws Exception { while(true) Thread.sleep(1000); } }' > /tmp/fixture/ArthasFixture.java; javac /tmp/fixture/ArthasFixture.java && "+runtime+" -Xms32m -Xmx128m -cp /tmp/fixture ArthasFixture"
 run('run','--rm','-d','--pull=never','--network','none','--memory','768m','--cpus','1','--user','1000:1000','--name',name,'--entrypoint','sh',image,'-c',start)
 run('cp',temp+'/agent',name+':'+agent)
 run('exec','-u','0',name,'chown','-R','1000:1000',agent)
 let java
 for(let i=0;i<200;i++){java=run('exec',name,'readlink','/proc/1/exe');if(java.endsWith('/java'))break;await new Promise(resolve=>setTimeout(resolve,100))}
 if(!java.endsWith('/java'))throw Error('fixture JVM did not start')
 if(native){
  if(!java.startsWith('/tmp/fixture-jre/'))throw Error('fixture must use stripped runtime')
  run('exec',name,'sh','-c','test ! -e /tmp/fixture-jre/bin/jcmd && ! /tmp/fixture-jre/bin/java --list-modules | grep -q jdk.attach')
  run('exec',name,agent+'/jattach','1','load','instrument','false',agent+'/arthas-agent.jar')
 }else run('exec',name,java,'-Xms32m','-Xmx128m','-jar',agent+'/arthas-core.jar','-pid','1','-target-ip','127.0.0.1','-telnet-port','-1','-http-port',String(port),'-core',agent+'/arthas-core.jar','-agent',agent+'/arthas-agent.jar')
 const request=input=>JSON.parse(run('exec',name,'curl','-fsS','--noproxy','*','--max-time','13','--config',agent+'/http.conf','-H','Content-Type: application/json','--data-binary',JSON.stringify(input),'http://127.0.0.1:'+port+'/api'))
 const unauth=run('exec',name,'curl','-s','--noproxy','*','-o','/dev/null','-w','%{http_code}','-H','Content-Type: application/json','--data-binary','{"action":"init_session"}','http://127.0.0.1:'+port+'/api')
 if(unauth!=='401')throw Error('loopback without authentication returned '+unauth)
 const init=request({action:'init_session'});if(init.state!=='SUCCEEDED'||!init.sessionId)throw Error('agent session failed')
 const result=request({action:'exec',sessionId:init.sessionId,consumerId:init.consumerId,command:'memory',execTimeout:10000})
 request({action:'close_session',sessionId:init.sessionId,consumerId:init.consumerId})
 if(result.state!=='SUCCEEDED'||!result.body?.results?.some(r=>r.type==='memory'))throw Error('real memory diagnosis failed')
 const identity=run('inspect','--format','{{.State.Running}} {{.HostConfig.NetworkMode}} {{len .HostConfig.PortBindings}}',name)
 if(identity!=='true none 0')throw Error('fixture runtime altered: '+identity)
 console.log(JSON.stringify({ok:true,arthas:'4.0.5',java:21,mode:native?'JRE without jdk.attach/jcmd':'JDK',platform:'linux',nonRoot:true,unauthenticatedHTTP:401,diagnostic:'memory',network:'none',publishedPorts:0,realBusinessTargetsTouched:0}))
} catch(error) {
 // ChildProcess errors can contain command text and response data. Do not dump
 // their object/arguments; attach logs remain inside the disposable container.
 console.error('Controlled Arthas container check failed at '+stage+': '+(error.status!==undefined?'docker command exit '+error.status:error.message));if(error.stderr)console.error(String(error.stderr).replaceAll(secret,'[redacted]').slice(-2000));if(error.stdout)console.error(String(error.stdout).replaceAll(secret,'[redacted]').slice(-3000));process.exitCode=1
} finally {try{run('rm','-f',name)}catch{}await rm(temp,{recursive:true,force:true})}
