#!/usr/bin/env python3
"""Run the real controller against a temporary filesystem and systemd/HTTP doubles.
No host service, account, network endpoint, or /etc path is modified.
"""
import contextlib,hashlib,json,os,pathlib,shutil,subprocess,sys,tarfile,tempfile,time,unittest
REPO=pathlib.Path(__file__).resolve().parents[1]
CTL=REPO/'deploy/devopsplatformctl'
HARNESS=r'''#!/usr/bin/env python3
import fcntl,json,os,pathlib,subprocess,sys,time,signal,shlex,re
name=pathlib.Path(sys.argv[0]).name;a=sys.argv[1:];fixture=pathlib.Path(os.environ['CTL_FIXTURE']);root=pathlib.Path(os.environ['DEVOPSPLATFORM_ROOT']);units=pathlib.Path(os.environ['DEVOPSPLATFORM_SYSTEMD_DIR'])
if name=='flock':
 try:fcntl.flock(int(a[-1]),fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:sys.exit(1)
 sys.exit(0)
if name=='id':
 if a==['-u']:print('0')
 sys.exit(0)
if name in ['chown','useradd','sleep']:sys.exit(0)
if name=='uname':print('Linux' if a==['-s'] else os.environ.get('CTL_ARCH','x86_64'));sys.exit(0)
if name=='mv':
 args=[x for x in a if not x.startswith('-')]
 if len(args)!=2:raise RuntimeError(a)
 src,dst=map(pathlib.Path,args)
 if dst.is_dir() and '-Tf' not in a and not dst.is_symlink():dst=dst/src.name
 os.replace(src,dst);sys.exit(0)
if name=='readlink':print(os.path.realpath(a[-1]));sys.exit(0)
if name=='journalctl':print('isolated journal', ' '.join(a));sys.exit(0)
with open(fixture/'state.lock','a') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 p=fixture/'state.json';s=json.loads(p.read_text());events=s.setdefault('events',[]);active=s.setdefault('active',{});rc=0;output=''
 def save():p.write_text(json.dumps(s))
 if name=='curl':
  url=a[-1];auth=sys.stdin.read() if '--config' in a else ''
  if url.endswith('/api/health'):
   ver=(root/'current/VERSION').read_text().strip() if (root/'current/VERSION').exists() else ''
   ok=active.get('devopsplatform',False) and ver not in s.get('bad_platform_health',[])
   output='{"ok":true}' if ok else '';rc=0 if ok else 22
  else:
   if os.environ.get('CTL_REAL_STORAGE')=='1':
    real=subprocess.run(['/usr/bin/curl',*a],input=auth,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    sys.stdout.write(real.stdout);sys.stderr.write(real.stderr);sys.exit(real.returncode)
   external='external.example' in url
   ok=s.get('external_healthy',True) if external else active.get('victorialogs',False) and not s.get('bad_storage_health',False)
   if external:s['external_authenticated']=bool(auth)
   output='OK' if ok else '';rc=0 if ok else 22
  if '--write-out' in a:output+='\n'+(str(s.get('storage_http_code',200)) if rc==0 else '503')
 elif name=='systemctl':
  op=a[0];service=next((x for x in a[1:] if not x.startswith('-')),'');service=service.removesuffix('.service')
  unit=units/(service+'.service')
  if op in ['daemon-reload','enable']:events.append({'op':op,'service':service})
  elif op=='cat':
   if unit.exists():
    output=unit.read_text()
    for extra in sorted((units/(service+'.service.d')).glob('*.conf')):output+='\n'+extra.read_text()
   else:rc=1
  elif op in ['is-active','status']:
   rc=0 if active.get(service,False) else 3
   if op=='status':output=service+': '+('active' if rc==0 else 'inactive')
  elif op in ['start','restart','stop']:
   version=''
   if service=='victorialogs':
    vp=root/'storage/current/VERSION'
    if vp.exists():version=vp.read_text().strip()
   else:
    vp=root/'current/VERSION'
    if vp.exists():version=vp.read_text().strip()
   events.append({'op':op,'service':service,'version':version})
   if service=='victorialogs' and op in ['restart','stop'] and os.environ.get('CTL_REAL_STORAGE')=='1' and s.get('storage_pid'):
    pid=s.pop('storage_pid')
    try:os.kill(pid,signal.SIGTERM)
    except ProcessLookupError:pass
    for _ in range(100):
     try:os.kill(pid,0)
     except ProcessLookupError:break
     time.sleep(.02)
    active[service]=False
   if op=='stop':active[service]=False
   elif op=='start' and active.get(service,False):pass
   elif not unit.exists():rc=1
   elif service=='victorialogs' and version in s.get('fail_storage_versions',[]):active[service]=False;rc=1
   elif service=='devopsplatform' and version in s.get('fail_platform_versions',[]):active[service]=False;rc=1
   else:
    active[service]=True
    s.setdefault('started_versions',{})[service]=version
    if service=='victorialogs':
     config=root/'config/victorialogs.env'
     s['storage_config_at_start']=config.read_text() if config.exists() else ''
     if os.environ.get('CTL_REAL_STORAGE')=='1':
      values={}
      for line in config.read_text().splitlines():
       if '=' in line:
        key,value=line.split('=',1);values[key]=value.strip('\"')
      command=next(line.split('=',1)[1] for line in unit.read_text().splitlines() if line.startswith('ExecStart='))
      command=re.sub(r'\$\{([^}]+)\}',lambda m:values[m.group(1)],command)
      log=open(fixture/'real-victorialogs.log','ab')
      proc=subprocess.Popen(shlex.split(command),env={**os.environ,'GOMAXPROCS':'2','GOMEMLIMIT':'256MiB'},stdout=log,stderr=log,start_new_session=True)
      s['storage_pid']=proc.pid
  else:raise RuntimeError('unexpected systemctl '+str(a))
 else:raise RuntimeError(name)
 save()
 if output:print(output)
 sys.exit(rc)
'''
class ControllerTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(prefix='ctl-vlogs-');self.base=pathlib.Path(self.tmp.name);self.root=self.base/'root';self.bin=self.base/'bin';self.bin.mkdir();self.units=self.base/'units';self.units.mkdir();self.fixtures=self.base/'fixture';self.fixtures.mkdir();(self.fixtures/'state.json').write_text('{}')
  h=self.bin/'harness';h.write_text(HARNESS);h.chmod(0o755)
  for name in ['flock','id','chown','useradd','uname','mv','readlink','journalctl','sleep','curl','systemctl']:(self.bin/name).symlink_to(h)
  self.env={**os.environ,'PATH':str(self.bin)+':'+os.environ['PATH'],'DEVOPSPLATFORM_ROOT':str(self.root),'DEVOPSPLATFORM_SYSTEMD_DIR':str(self.units),'DEVOPSPLATFORM_CTL_PATH':str(self.base/'installed/devopsplatformctl'),'DEVOPSPLATFORM_HEALTH_ATTEMPTS':'2','CTL_FIXTURE':str(self.fixtures)}
 def tearDown(self):
  if os.environ.get('CTL_KEEP_FIXTURE'):
   state=self.state()
   target=pathlib.Path(os.environ['CTL_KEEP_FIXTURE'])
   if self.env.get('CTL_REAL_STORAGE')=='1':
    if target.exists():shutil.rmtree(target)
    shutil.copytree(self.base,target,symlinks=True)
  state=self.state()
  pid=state.get('storage_pid')
  if pid:
   import signal
   result=subprocess.run(['ps','-p',str(pid),'-o','args='],text=True,stdout=subprocess.PIPE)
   if str(self.root)+'/data/victorialogs' in result.stdout:
    with contextlib.suppress(ProcessLookupError):os.kill(pid,signal.SIGTERM)
    time.sleep(.1)
  self.tmp.cleanup()
 def state(self,**updates):
  p=self.fixtures/'state.json';s=json.loads(p.read_text());s.update(updates);p.write_text(json.dumps(s));return s
 def package(self,version,vl='v1.53.0',missing=None,vl_body=None,vl_binary=None):
  folder=self.base/('build-'+version);p=folder/('devopsplatform-'+version);(p/'bin').mkdir(parents=True);(p/'support').mkdir();(p/'plugins/logs').mkdir(parents=True)
  for name in ['devopsplatform','logserver','victoria-logs-prod']:
   body=vl_body if name=='victoria-logs-prod' else None
   f=p/'bin'/name;f.write_text(body or '#!/bin/sh\n# '+(vl if name=='victoria-logs-prod' else version)+'\nexit 0\n');f.chmod(0o755)
   if name=='victoria-logs-prod' and vl_binary:f.write_bytes(pathlib.Path(vl_binary).read_bytes())
  (p/'devopsplatformctl').write_text(CTL.read_text()+'\n# bundle '+version+'\n');(p/'devopsplatformctl').chmod(0o755);(p/'VERSION').write_text(version+'\n');(p/'support/VICTORIALOGS-VERSION').write_text(vl+'\n');(p/'plugins/logs/manifest.yaml').write_text('name: logs\nargs:\n  - -addr\n  - :9801\n')
  if missing:(p/missing).unlink()
  lines=[]
  for f in sorted(p.rglob('*')):
   if f.is_file():lines.append(hashlib.sha256(f.read_bytes()).hexdigest()+'  '+str(f.relative_to(p)))
  (p/'SHA256SUMS').write_text('\n'.join(lines)+'\n')
  archive=self.base/f'devopsplatform-{version}-linux-amd64.tar.gz'
  with tarfile.open(archive,'w:gz') as tar:tar.add(p,arcname=p.name)
  (pathlib.Path(str(archive)+'.sha256')).write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n');return archive
 def ctl(self,*args,ok=True):
  r=subprocess.run(['bash',str(CTL),*map(str,args)],env=self.env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=30)
  if ok and r.returncode!=0:self.fail('controller failed:\n'+r.stdout)
  if not ok and r.returncode==0:self.fail('controller unexpectedly succeeded:\n'+r.stdout)
  return r
 def config(self,text):
  p=self.root/'config';p.mkdir(parents=True,exist_ok=True);(p/'devopsplatform.env').write_text('DEVOPSPLATFORM_ADDR=0.0.0.0:9800\nDEVOPSPLATFORM_AUTH=fixture-admin\nDEVOPSPLATFORM_DATA_TOKEN=fixture-data\n'+text)
 def test_fresh_install_start_stop_and_defaults(self):
  self.ctl('install',self.package('1'));s=self.state();self.assertTrue(s['active']['victorialogs']);self.assertTrue(s['active']['devopsplatform'])
  starts=[e['service'] for e in s['events'] if e['op']=='start'];self.assertLess(starts.index('victorialogs'),starts.index('devopsplatform'))
  unit=(self.units/'devopsplatform.service').read_text();self.assertIn('ExecStartPre=',unit);self.assertIn('After=victorialogs.service',unit)
  self.assertIn('VLOGS_RETENTION_DAYS=7',(self.root/'config/victorialogs.env').read_text())
  self.ctl('stop');self.assertFalse(self.state()['active']['victorialogs']);self.ctl('start');self.ctl('status');self.assertEqual(self.ctl('version','victorialogs').stdout.strip(),'v1.53.0')
 def test_upgrade_preserves_data_credentials_policy_and_no_storage_restart(self):
  self.config('');self.ctl('install',self.package('1'));cfg=self.root/'config/devopsplatform.env';before=cfg.read_bytes();vlcfg=self.root/'config/victorialogs.env';vlcfg.write_text(vlcfg.read_text().replace('DAYS=7','DAYS=365').replace('20GiB','8GiB'))
  sentinel=self.root/'data/victorialogs/sentinel';sentinel.write_bytes(b'unchanged-log-data');self.ctl('restart');events=len(self.state()['events']);self.ctl('upgrade',self.package('2'))
  self.assertEqual(before,cfg.read_bytes());self.assertEqual(sentinel.read_bytes(),b'unchanged-log-data');self.assertIn('DAYS=365',vlcfg.read_text());self.assertIn('RETENTION_DAYS=365',(self.units/'devopsplatform.service').read_text())
  self.assertFalse(any(e['service']=='victorialogs' and e['op'] in ['stop','restart'] for e in self.state()['events'][events:]),self.state()['events'][events:]);self.assertEqual((self.root/'previous').resolve().name,'1')
 def test_storage_upgrade_and_platform_rollback_keep_new_storage(self):
  self.ctl('install',self.package('1'));self.ctl('upgrade',self.package('2','v1.54.0'));pin=(self.root/'storage/current').resolve();self.ctl('rollback');self.assertEqual((self.root/'current').resolve().name,'1');self.assertEqual(pin,(self.root/'storage/current').resolve());self.assertEqual(self.ctl('version','victorialogs').stdout.strip(),'v1.54.0')
 def test_platform_failure_restores_old_and_reports_failure(self):
  self.ctl('install',self.package('1'));self.state(fail_platform_versions=['2']);self.ctl('upgrade',self.package('2','v1.54.0'),ok=False)
  self.assertEqual((self.root/'current').resolve().name,'1');self.assertEqual(self.ctl('version','victorialogs').stdout.strip(),'v1.54.0');self.assertTrue(self.state()['active']['devopsplatform'])
 def test_storage_failure_no_implicit_downgrade_and_retry_same_archive(self):
  self.ctl('install',self.package('1'));self.state(fail_storage_versions=['v1.54.0']);archive=self.package('2','v1.54.0');self.ctl('upgrade',archive,ok=False)
  self.assertEqual((self.root/'current').resolve().name,'1');self.assertFalse(self.state()['active']['devopsplatform']);self.assertEqual(self.ctl('version','victorialogs').stdout.strip(),'v1.54.0')
  self.state(fail_storage_versions=[]);self.ctl('upgrade',archive);self.assertEqual((self.root/'current').resolve().name,'2')
 def test_downgrade_rejected_before_stop(self):
  self.ctl('install',self.package('1','v1.54.0'));before=self.state()['events'];self.ctl('upgrade',self.package('2','v1.53.0'),ok=False);self.assertEqual(before,self.state()['events'])
 def test_external_storage_auth_and_health_gate(self):
  self.config('DEVOPSPLATFORM_VICTORIALOGS_URL=https://external.example\nDEVOPSPLATFORM_VICTORIALOGS_TOKEN=fixture-secret-token\n');self.ctl('install',self.package('1'))
  self.assertFalse((self.units/'victorialogs.service').exists());self.assertTrue(self.state()['external_authenticated']);self.assertNotIn('fixture-secret-token',self.ctl('status').stdout)
  self.state(external_healthy=False);self.ctl('restart',ok=False);self.assertFalse(self.state()['active']['devopsplatform'])
 def test_missing_payload_and_checksum_failure_have_no_service_actions(self):
  for missing in ['bin/victoria-logs-prod','support/VICTORIALOGS-VERSION']:
   self.ctl('install',self.package('missing-'+missing.split('/')[-1],missing=missing),ok=False)
  archive=self.package('bad');archive.write_bytes(archive.read_bytes()+b'bad');self.ctl('install',archive,ok=False);self.assertEqual(self.state().get('events',[]),[])
 def test_unmanaged_port_and_unknown_unit_are_not_overwritten(self):
  self.state(active={'victorialogs':True});self.ctl('install',self.package('1'),ok=False);self.assertFalse((self.root/'current').exists())
  self.state(active={});unit=self.units/'victorialogs.service';unit.write_text('[Service]\nExecStart=/other/victoria-logs\n');before=unit.read_bytes();self.ctl('install',self.package('2'),ok=False);self.assertEqual(unit.read_bytes(),before)
 def test_unsafe_archive_members_are_rejected_before_service_actions(self):
  for i,(name,kind) in enumerate([('../outside',tarfile.REGTYPE),('/tmp/ctl-outside',tarfile.REGTYPE),('bundle/link',tarfile.SYMTYPE),('bundle/hard',tarfile.LNKTYPE),('bundle/fifo',tarfile.FIFOTYPE)]):
   archive=self.base/f'devopsplatform-unsafe{i}-linux-amd64.tar.gz'
   with tarfile.open(archive,'w:gz') as tar:
    member=tarfile.TarInfo(name);member.type=kind;member.linkname='../outside' if kind in [tarfile.SYMTYPE,tarfile.LNKTYPE] else '';tar.addfile(member)
   result=self.ctl('install',archive,ok=False)
   self.assertIn('发布包包含不安全路径' if i<2 else '发布包不能含链接',result.stdout)
  self.assertEqual(self.state().get('events',[]),[]);self.assertFalse((self.base/'outside').exists())
 def test_unsafe_checksum_manifest_is_rejected(self):
  archive=self.package('badmanifest');folder=self.base/'build-badmanifest/devopsplatform-badmanifest';manifest=folder/'SHA256SUMS';manifest.write_text(manifest.read_text()+'0'*64+'  ../outside\n')
  with tarfile.open(archive,'w:gz') as tar:tar.add(folder,arcname=folder.name)
  pathlib.Path(str(archive)+'.sha256').unlink()
  self.assertIn('校验清单含不安全路径',self.ctl('install',archive,ok=False).stdout);self.assertEqual(self.state().get('events',[]),[])
 def test_invalid_local_port_is_rejected_before_stop(self):
  self.config('DEVOPSPLATFORM_VICTORIALOGS_URL=http://127.0.0.1:65536\nDEVOPSPLATFORM_VICTORIALOGS_MANAGED=1\n')
  self.assertIn('端口必须',self.ctl('install',self.package('badport'),ok=False).stdout);self.assertEqual(self.state().get('events',[]),[])
 def test_failed_fresh_platform_health_leaves_platform_stopped(self):
  self.state(bad_platform_health=['badfresh']);self.ctl('install',self.package('badfresh'),ok=False);self.assertFalse(self.state()['active']['devopsplatform'])
 def test_adopts_shipped_unit_preserves_parameters_and_365_days(self):
  old=self.root/'releases/legacy';(old/'bin').mkdir(parents=True);(old/'plugins/logs').mkdir(parents=True);(old/'VERSION').write_text('legacy\n');(old/'bin/devopsplatform').write_text('old');(old/'bin/devopsplatform').chmod(0o755)
  body='#!/bin/sh\n# v1.53.0\nexit 0\n';(old/'bin/victoria-logs-prod').write_text(body);(old/'bin/victoria-logs-prod').chmod(0o755);(self.root/'current').symlink_to(old)
  unit=self.units/'victorialogs.service';unit.write_text(f'[Unit]\nDescription=existing\n[Service]\nUser=devopsplatform\nGroup=devopsplatform\nExecStart={self.root}/current/bin/victoria-logs-prod -httpListenAddr=127.0.0.1:9428 -storageDataPath=/existing/data -retentionPeriod=365d -memory.allowedBytes=128MiB\n[Install]\nWantedBy=multi-user.target\n')
  self.state(active={'victorialogs':True,'devopsplatform':True});self.ctl('upgrade',self.package('2',vl_body=body));text=unit.read_text();self.assertIn('-storageDataPath=/existing/data',text);self.assertIn('-memory.allowedBytes=128MiB',text);self.assertIn('VLOGS_RETENTION_DAYS',text);self.assertIn('DAYS=365',(self.root/'config/victorialogs.env').read_text());self.assertTrue(list((self.root/'backups').glob('*.service.*')))
 def test_installed_controller_updated_and_repeat_keeps_previous(self):
  self.ctl('install',self.package('1'));archive=self.package('2');self.ctl('upgrade',archive)
  self.assertIn('# bundle 2',(self.base/'installed/devopsplatformctl').read_text());self.ctl('upgrade',archive)
  self.assertEqual((self.root/'previous').resolve().name,'1')
 def test_concurrent_mutation_is_rejected(self):
  import fcntl
  self.ctl('install',self.package('1'));before=self.state()['events']
  with open(self.root/'run/devopsplatformctl.lock','a') as lock:
   fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB);self.ctl('restart',ok=False)
  self.assertEqual(before,self.state()['events'])
 def test_redirect_is_not_a_healthy_storage(self):
  self.state(storage_http_code=302);self.ctl('install',self.package('1'),ok=False)
  self.assertFalse(self.state()['active'].get('devopsplatform',False))
 def test_execstart_override_is_not_silently_replaced(self):
  self.ctl('install',self.package('1'));extra=self.units/'victorialogs.service.d';extra.mkdir();(extra/'override.conf').write_text('[Service]\nExecStart=\nExecStart=/other/backend\n');before=self.state()['events']
  self.ctl('upgrade',self.package('2'),ok=False);self.assertEqual(before,self.state()['events'])
 def test_bad_platform_health_restores_previous(self):
  self.ctl('install',self.package('1'));self.state(bad_platform_health=['2']);self.ctl('upgrade',self.package('2'),ok=False)
  self.assertEqual((self.root/'current').resolve().name,'1');self.assertTrue(self.state()['active']['devopsplatform'])
 def test_legacy_equal_retention_flag_is_preserved(self):
  self.package('legacy');old=self.root/'releases/legacy';old.parent.mkdir(parents=True)
  shutil.copytree(self.base/'build-legacy/devopsplatform-legacy',old);(self.root/'current').symlink_to(old)
  manifest=old/'plugins/logs/manifest.yaml';manifest.write_text(manifest.read_text()+'  - -retention-days=365\n')
  self.ctl('upgrade',self.package('2'))
  self.assertIn('DAYS=365',(self.root/'config/victorialogs.env').read_text())
 def test_missing_managed_config_is_not_reset_to_seven_days(self):
  self.ctl('install',self.package('1'));p=self.root/'config/victorialogs.env';p.unlink();before=self.state()['events']
  self.ctl('restart',ok=False);self.assertFalse(p.exists());self.assertEqual(before,self.state()['events'])
 @unittest.skipUnless(os.environ.get('CTL_VLOGS_TEST_BINARY') and os.environ.get('CTL_LOGSERVER_TEST_BINARY'),'explicit local native binaries required')
 def test_real_storage_restart_upgrade_and_retention_env(self):
  import socket,urllib.request,urllib.parse,datetime
  def port():
   with socket.socket() as sock:sock.bind(('127.0.0.1',0));return sock.getsockname()[1]
  self.env['CTL_REAL_STORAGE']='1';self.env['DEVOPSPLATFORM_HEALTH_ATTEMPTS']='20'
  url='http://127.0.0.1:'+str(port());self.config('DEVOPSPLATFORM_VICTORIALOGS_URL='+url+'\nDEVOPSPLATFORM_VICTORIALOGS_MANAGED=1\n')
  c=self.root/'config/victorialogs.env';c.write_text('VLOGS_LISTEN_ADDR='+url.split('//')[1]+'\nVLOGS_RETENTION_DAYS=365\nVLOGS_DISK_LIMIT=2GiB\nVLOGS_MEMORY_LIMIT=32MiB\n')
  binary=pathlib.Path(os.environ['CTL_VLOGS_TEST_BINARY']).resolve()
  self.ctl('install',self.package('real1',vl_binary=binary));pid=self.state()['storage_pid']
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
  def req(path,body=None):
   content='application/x-www-form-urlencoded' if isinstance(body,dict) else 'application/x-ndjson'
   if isinstance(body,dict):body=urllib.parse.urlencode(body).encode()
   with opener.open(urllib.request.Request(url+path,data=body,headers={'Content-Type':content}),timeout=5) as response:return response.read()
  event={'_msg':'ctl-verification-only','_time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'service':'ctl_fixture','event_key':'fixture-1'}
  req('/insert/jsonline?_stream_fields=service',(json.dumps(event)+'\n').encode());req('/internal/force_flush',b'')
  before=[json.loads(x) for x in req('/select/logsql/query',{'query':'* | stats count() as n'}).splitlines()];self.assertEqual(before[0]['n'],'1')
  self.ctl('upgrade',self.package('real2',vl_binary=binary));self.assertEqual(pid,self.state()['storage_pid'])
  self.ctl('restart','victorialogs');self.assertNotEqual(pid,self.state()['storage_pid'])
  count=[json.loads(x) for x in req('/select/logsql/query',{'query':'* | stats count() as n'}).splitlines()];self.assertEqual(count[0]['n'],'1')
  # Different fixture wrapper, same official engine/version: exercises binary
  # replacement and persistence, not cross-version format compatibility.
  self.ctl('upgrade',self.package('real3',vl_body='#!/bin/sh\nexec '+str(binary)+' "$@"\n'))
  count=[json.loads(x) for x in req('/select/logsql/query',{'query':'* | stats count() as n'}).splitlines()];self.assertEqual(count[0]['n'],'1')
  server_url='http://127.0.0.1:'+str(port())
  retention=next(line.split('=',2)[2] for line in (self.units/'devopsplatform.service').read_text().splitlines() if line.startswith('Environment=DEVOPSPLATFORM_LOG_RETENTION_DAYS='))
  env={k:v for k,v in os.environ.items() if not k.startswith('DEVOPSPLATFORM_')};env.update({'DEVOPSPLATFORM_LOG_RETENTION_DAYS':retention,'DEVOPSPLATFORM_DATA_TOKEN':'fixture-data','GOMAXPROCS':'2'})
  with open(self.fixtures/'real-logserver.log','wb') as log:
   proc=subprocess.Popen([os.environ['CTL_LOGSERVER_TEST_BINARY'],'-addr','127.0.0.1:0','-admin-addr',server_url.split('//')[1],'-data',str(self.base/'logserver-data'),'-victorialogs-url',url],env=env,stdout=log,stderr=log)
   try:
    stats=None
    for _ in range(100):
     try:
      with opener.open(server_url+'/api/stats',timeout=2) as response:stats=json.load(response)
      break
     except OSError:time.sleep(.05)
    self.assertIsNotNone(stats);self.assertEqual(stats['retention_days'],365)
   finally:proc.terminate();proc.wait(timeout=8)
  self.ctl('stop');self.assertFalse(self.state()['active']['victorialogs'])
if __name__=='__main__':unittest.main(verbosity=2)
