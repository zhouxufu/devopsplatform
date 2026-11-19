#!/usr/bin/env python3
"""Exercise the real release/fetch scripts with isolated compiler/network doubles."""
import hashlib,io,json,os,pathlib,shutil,subprocess,tarfile,tempfile,unittest
REPO=pathlib.Path(__file__).resolve().parents[1]
class PackagingTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory(prefix='ctl-release-');self.root=pathlib.Path(self.tmp.name)
  for d in ['scripts','deploy/licenses','plugins','steps','network']:(self.root/d).mkdir(parents=True,exist_ok=True)
  for name in ['release-linux.sh','fetch-victorialogs.py','build-agents.sh']:shutil.copyfile(REPO/'scripts'/name,self.root/'scripts'/name);(self.root/'scripts'/name).chmod(0o755)
  for name in ['devopsplatformctl','victorialogs.service','VICTORIALOGS.md']:shutil.copyfile(REPO/'deploy'/name,self.root/'deploy'/name)
  shutil.copyfile(REPO/'deploy/licenses/VictoriaLogs-LICENSE.txt',self.root/'deploy/licenses/VictoriaLogs-LICENSE.txt')
  (self.root/'plugins/manifest.yaml').write_text('fixture: true\n');(self.root/'steps/step.json').write_text('{}\n')
  self.go=self.root/'go-fixture';self.go.write_text('''#!/usr/bin/env python3
import os,pathlib,sys
out=pathlib.Path(sys.argv[sys.argv.index('-o')+1]);out.parent.mkdir(parents=True,exist_ok=True);out.write_text('#!/bin/sh\\nexit 0\\n');out.chmod(0o755)
''');self.go.chmod(0o755)
  assets=[]
  for arch in ['amd64','arm64']:
   buf=io.BytesIO();content=b'#!/bin/sh\n# fixture official release\nexit 0\n'
   with tarfile.open(fileobj=buf,mode='w:gz') as tar:
    info=tarfile.TarInfo('victoria-logs-prod');info.mode=0o755;info.size=len(content);tar.addfile(info,io.BytesIO(content))
   archive=self.root/'network'/arch;archive.write_bytes(buf.getvalue());assets.append({'name':f'victoria-logs-linux-{arch}-v1.53.0.tar.gz','digest':'sha256:'+hashlib.sha256(archive.read_bytes()).hexdigest(),'browser_download_url':'https://fixture.invalid/'+arch})
  (self.root/'network/release.json').write_text(json.dumps({'assets':assets}))
  (self.root/'network/sitecustomize.py').write_text('''import io,json,os,pathlib,urllib.request
root=pathlib.Path(os.environ['FETCH_FIXTURE'])
def open_fixture(req,**kw):
 url=req.full_url if hasattr(req,'full_url') else req
 if url=='https://api.github.com/repos/VictoriaMetrics/VictoriaLogs/releases/tags/v1.53.0':
  data=json.loads((root/'release.json').read_text())
  if os.environ.get('FETCH_FIXTURE_BAD_DIGEST'):
   for asset in data['assets']:asset['digest']='sha256:'+'0'*64
  return io.BytesIO(json.dumps(data).encode())
 if url in ['https://fixture.invalid/amd64','https://fixture.invalid/arm64']:return io.BytesIO((root/url.rsplit('/',1)[-1]).read_bytes())
 raise RuntimeError('network outside isolated fixture refused')
urllib.request.urlopen=open_fixture
''')
  self.env={**os.environ,'GO_BIN':str(self.go),'SKIP_WEB_BUILD':'1','PYTHONPATH':str(self.root/'network'),'FETCH_FIXTURE':str(self.root/'network')}
 def tearDown(self):self.tmp.cleanup()
 def test_archive_contains_verified_backend_version_controller_and_both_agents(self):
  for arch in ['amd64','arm64']:
   r=subprocess.run(['bash','scripts/release-linux.sh',arch,'fixture'],cwd=self.root,env=self.env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=30);self.assertEqual(r.returncode,0,r.stdout)
   archive=self.root/'dist'/f'devopsplatform-fixture-linux-{arch}.tar.gz';self.assertTrue(pathlib.Path(str(archive)+'.sha256').exists())
   with tarfile.open(archive) as tar:
    prefix=f'devopsplatform-fixture-linux-{arch}/';entries={m.name:m for m in tar.getmembers()};manifest=tar.extractfile(prefix+'SHA256SUMS').read().decode()
    for name in ['bin/victoria-logs-prod','support/VICTORIALOGS-VERSION','devopsplatformctl','VERSION','dist/agents/logagent-linux-amd64','dist/agents/logagent-linux-arm64']:
     self.assertIn(prefix+name,entries);raw=tar.extractfile(prefix+name).read();self.assertIn(hashlib.sha256(raw).hexdigest(),manifest)
    self.assertEqual(tar.extractfile(prefix+'support/VICTORIALOGS-VERSION').read(),b'v1.53.0\n')
 def test_official_digest_failure_does_not_publish_archive(self):
  env={**self.env,'FETCH_FIXTURE_BAD_DIGEST':'1'}
  r=subprocess.run(['bash','scripts/release-linux.sh','amd64','bad-digest'],cwd=self.root,env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=30)
  self.assertNotEqual(r.returncode,0);self.assertFalse(list((self.root/'dist').glob('*.tar.gz')));self.assertFalse(list((self.root/'dist').glob('.release-*')))
if __name__=='__main__':unittest.main(verbosity=2)
