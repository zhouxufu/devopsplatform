#!/usr/bin/env python3
"""Disposable loopback-only end-to-end fixture; never contacts real Splunk."""
import gzip,http.server,json,pathlib,signal,subprocess,tempfile,threading,time,urllib.request,urllib.parse,sys
repo=pathlib.Path(__file__).resolve().parents[1]
root=pathlib.Path(tempfile.mkdtemp(prefix='extension-fixture-'))
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def reply(self,data):
  raw=json.dumps(data).encode();self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers();self.wfile.write(raw)
 def do_GET(self):
  p=urllib.parse.urlparse(self.path);route=p.path
  if route=='/services/server/info':return self.reply({'entry':[{'content':{'version':'6.6.3'}}]})
  if route.startswith('/services/search/jobs/'):
   if route.endswith('/results'):
    rows=[{'__lp_ts':str(int(time.time()*1000)-i*1000),'_raw':'本地模拟日志 timeout #'+str(i),'host':'fixture-node','source':'/logs/demo.log','level':'ERROR','traceId':'fixture-trace'} for i in range(101)]
    q=urllib.parse.parse_qs(p.query);offset=int(q.get('offset',['0'])[0]);count=int(q.get('count',['100'])[0]);return self.reply({'results':rows[offset:offset+count]})
   return self.reply({'entry':[{'content':{'isDone':True,'isFinalized':False,'dispatchState':'DONE'}}]})
  if route=='/ui' or route.startswith('/assets/'):
   file=repo/'web/dist'/('logs.html' if route=='/ui' else route.lstrip('/'))
   self.send_response(200);self.send_header('Content-Type','text/html' if file.suffix=='.html' else 'text/javascript' if file.suffix=='.js' else 'text/css');self.end_headers();self.wfile.write(file.read_bytes());return
  if route=='/api/search':return self.reply({'hits':[],'has_more':False})
  if route=='/api/stats':return self.reply({'agents':[]})
  return self.reply([])
 def do_POST(self):
  raw=self.rfile.read(int(self.headers.get('Content-Length',0)))
  if self.path=='/services/auth/login':return self.reply({'sessionKey':'fixture-session'})
  if self.path=='/services/search/jobs':return self.reply({'sid':'fixture-job'})
  return self.reply({})
 def do_DELETE(self):return self.reply({})
server=http.server.ThreadingHTTPServer(('127.0.0.1',19971),Handler);threading.Thread(target=server.serve_forever,daemon=True).start()
pluginroot=root/'plugins/logs';pluginroot.mkdir(parents=True);(pluginroot/'manifest.yaml').write_text('name: logs\ndisplay: 日志查询\nversion: fixture\nbinary: /tmp/devops-logserver-extensions-realtime\nargs: '+json.dumps(['-addr','127.0.0.1:19974','-admin-addr','127.0.0.1:19973','-data',str(root/'native-logs'),'-token','fixture-ingest-token'])+'\nadmin_addr: 127.0.0.1:19973\n')
log=(root/'platform.log').open('wb');proc=subprocess.Popen(['/tmp/devops-platform-extensions','-addr','127.0.0.1:19970','-plugins',str(root/'plugins'),'-data',str(root/'data')],stdout=log,stderr=log)
def api(path,method='GET',data=None,ctype='application/json'):
 if isinstance(data,dict):data=json.dumps(data).encode()
 req=urllib.request.Request('http://127.0.0.1:19970'+path,data=data,method=method,headers={'Content-Type':ctype})
 with urllib.request.urlopen(req,timeout=10) as r:return json.load(r)
stop=threading.Event()
def feed():
 n=0
 while not stop.is_set():
  n+=1
  payload={'agent_id':'fixture','batch_id':str(n),'events':[{'id':'native-'+str(n),'ts':int(time.time()*1000),'service':'native-fixture','environment':'prod','level':'INFO','message':'本地实时验证 #'+str(n)}]}
  req=urllib.request.Request('http://127.0.0.1:19974/ingest',data=gzip.compress(json.dumps(payload).encode()),headers={'Content-Type':'application/json','Content-Encoding':'gzip','X-Token':'fixture-ingest-token'})
  try:
   with urllib.request.urlopen(req,timeout=2) as r:r.read()
  except Exception:pass
  stop.wait(1)
try:
 for _ in range(60):
  try:api('/api/health');break
  except Exception:time.sleep(.2)
 package=(repo/'dist-plugins/splunk-logs-darwin-arm64.zip').read_bytes();boundary='fixture-upload-boundary'
 body=('--'+boundary+'\r\nContent-Disposition: form-data; name="package"; filename="splunk.zip"\r\nContent-Type: application/zip\r\n\r\n').encode()+package+('\r\n--'+boundary+'--\r\n').encode()
 api('/api/extensions','POST',body,'multipart/form-data; boundary='+boundary)
 api('/api/extensions/splunk-logs/config','PUT',{'endpoint':'http://127.0.0.1:19971','username':'fixture-reader','password':'fixture-password','users':['local'],'bindings':[{'service':'demo-api','environment':'prod','index':'demo'}],'fields':{'level':'level','trace_id':'traceId'}})
 threading.Thread(target=feed,daemon=True).start()
 print(json.dumps({'url':'http://127.0.0.1:19970/','root':str(root),'online':False}),flush=True)
 signal.signal(signal.SIGTERM,lambda *_:sys.exit(0));signal.signal(signal.SIGINT,lambda *_:sys.exit(0))
 while True:time.sleep(1)
finally:
 stop.set()
 proc.terminate()
 try:proc.wait(timeout=10)
 except subprocess.TimeoutExpired:proc.kill()
 server.shutdown();log.close()
