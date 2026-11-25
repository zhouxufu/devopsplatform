"""Opt-in, isolated compatibility check. Requires the disposable Nacos at 127.0.0.1:28848."""
import json, pathlib, subprocess, tempfile, time, os
ROOT=pathlib.Path(__file__).resolve().parent
JAVA=os.environ.get('DUBBO_TEST_JAVA','/Library/Java/JavaVirtualMachines/jdk-17.jdk/Contents/Home/bin/java')
JAVA8=os.environ.get('DUBBO_TEST_JAVA8','/Users/zhouzheng/Library/Java/JavaVirtualMachines/zulu-8.jdk/Contents/Home/bin/java')
JAR=ROOT.parent/'target/dubbo-executor-1.0.0.jar'
results=[]
host=os.environ.get('DUBBO_TEST_HOST') or subprocess.check_output(['ipconfig','getifaddr','en0'],text=True).strip()
with tempfile.TemporaryDirectory(prefix='dubbo-compat-') as tmp:
 for version,transport in [('2.7.23','dubbo'),('3.3.6','dubbo'),('3.3.6','tri')]:
  home=pathlib.Path(tmp)/(version+'-'+transport);home.mkdir();state=home/'ready';log=open(home/'provider.log','w+')
  target=ROOT/('target-'+version)
  p=subprocess.Popen([JAVA8 if version.startswith('2') else JAVA,'-Dfixture.protocol='+transport,'-Xmx256m','-Duser.home='+str(home),'-Dorg.slf4j.simpleLogger.defaultLogLevel=warn','-cp',str(target/'classes')+':'+str(target/'dependency/*'),'platform.fixture.EchoProvider','nacos://127.0.0.1:28848',str(state),version],stdin=subprocess.PIPE,stdout=log,stderr=log,env={**os.environ,'DUBBO_IP_TO_REGISTRY':host,'DUBBO_IP_TO_BIND':host})
  try:
   for _ in range(90):
    if state.exists():break
    if p.poll() is not None:raise RuntimeError('provider exited '+str(p.returncode))
    time.sleep(1)
   assert state.exists(),'provider startup timeout'
   def execute(action,**extra):
    data={'action':action,'rpc':{'interface':'platform.fixture.EchoProvider$Echo','method':'echo','group':'fixture','version':version,'parameter_types':['java.lang.Long']},'timeout_ms':15000,**extra}
    r=subprocess.run([JAVA,'-Dfixture.protocol='+transport,'-Xmx256m','-Duser.home='+str(home/'client'),'-jar',str(JAR)],input=json.dumps(data),text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=25)
    try:out=json.loads(r.stdout)
    except Exception:raise RuntimeError(r.stdout[-800:]+r.stderr[-800:])
    if out.get('error'):raise RuntimeError(json.dumps(out)+'\n'+'\n'.join(line for line in r.stderr.splitlines() if any(k in line for k in ['Exception','ERROR','Failed','Serialization']))[-6000:])
    return out
   print('provider '+version+' '+state.read_text(),flush=True)
   direct=execute('invoke',endpoint=state.read_text(),arguments=[9223372036854775806]);assert direct.get('body')==9223372036854775806,direct
   discovered=execute('discover',registry={'address':'nacos://127.0.0.1:28848','namespace':'','group':'DEFAULT_GROUP','username':'','password':''})
   assert state.read_text() in discovered.get('providers',[]),discovered
   via=execute('invoke',endpoint=state.read_text(),arguments=[9223372036854775806]);assert via.get('body')==9223372036854775806,via
   results.append({'version':version,'transport':transport,'direct':True,'nacos_discovery':True,'discovered_call':True,'long_precision':True})
   print(json.dumps(results[-1]),flush=True)
  except Exception:
   log.flush();log.seek(0);print(log.read()[-2000:]);raise
  finally:
   p.stdin.close()
   try:p.wait(timeout=12)
   except subprocess.TimeoutExpired:p.terminate();p.wait(timeout=5)
   log.close()
print(json.dumps({'passed':results}))
