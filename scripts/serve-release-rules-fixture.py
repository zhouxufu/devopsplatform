#!/usr/bin/env python3
"""Opt-in temporary rule UI + real plugin compiler; no real source connections."""
import json,os,pathlib,signal,subprocess,tempfile,time,urllib.request,sys
repo=pathlib.Path(__file__).resolve().parents[1]
root=pathlib.Path(tempfile.mkdtemp(prefix='release-rules-fixture-'))
for name,body in {'delivery-core':'name: delivery-core\nversion: fixture\ninternal: true\nbinary: /bin/sleep\nargs: ["3600"]\nadmin_addr: 127.0.0.1:19981\n','research':'name: research\ndisplay: 研发工作台\nversion: fixture\nbackend: delivery-core\nui_path: /ui/research\n'}.items():
 p=root/'plugins'/name;p.mkdir(parents=True);(p/'manifest.yaml').write_text(body)
processes=[];logs=[]
def spawn(args,env=None):
 f=(root/(str(len(logs))+'.log')).open('wb');logs.append(f);p=subprocess.Popen(args,env=env,stdout=f,stderr=f);processes.append(p)
def api(path,method='GET',data=None,ctype='application/json'):
 if isinstance(data,dict):data=json.dumps(data).encode()
 req=urllib.request.Request('http://127.0.0.1:19980'+path,data=data,method=method,headers={'Content-Type':ctype})
 with urllib.request.urlopen(req,timeout=10) as r:return json.load(r)
try:
 spawn(['/tmp/rules-browser-verified','-test.run=^TestReleaseRulesBrowserFixture$','-test.timeout=0'],dict(os.environ,DEVOPS_RULES_BROWSER_FIXTURE='1'))
 spawn(['/tmp/platform-release-rules-fixture','-addr','127.0.0.1:19980','-plugins',str(root/'plugins'),'-data',str(root/'data'),'-auth','','-db-driver','sqlite','-db-dsn',str(root/'registry.db')])
 for _ in range(100):
  try:api('/api/health');api('/api/plugins/research/api/health');break
  except Exception:time.sleep(.1)
 package=(repo/'dist-plugins/release-rules-darwin-arm64.zip').read_bytes();boundary='release-rule-fixture'
 body=('--'+boundary+'\r\nContent-Disposition: form-data; name="package"; filename="rules.zip"\r\nContent-Type: application/zip\r\n\r\n').encode()+package+('\r\n--'+boundary+'--\r\n').encode()
 api('/api/extensions','POST',body,'multipart/form-data; boundary='+boundary)
 api('/api/extensions/release-rules/enable','POST',{})
 print(json.dumps({'url':'http://127.0.0.1:19980/api/plugins/research/ui#/settings','root':str(root),'real_source':False}),flush=True)
 signal.signal(signal.SIGTERM,lambda *_:sys.exit(0));signal.signal(signal.SIGINT,lambda *_:sys.exit(0))
 while True:time.sleep(1)
finally:
 for p in reversed(processes):
  p.terminate()
  try:p.wait(timeout=10)
  except subprocess.TimeoutExpired:p.kill();p.wait()
 for f in logs:f.close()
