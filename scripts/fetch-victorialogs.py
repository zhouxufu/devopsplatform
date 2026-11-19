#!/usr/bin/env python3
"""Fetch a pinned official binary and verify the release asset SHA-256."""
import argparse, hashlib, json, os, platform, tempfile, urllib.request, tarfile
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--os', choices=['linux','darwin'], default=platform.system().lower())
p.add_argument('--arch',choices=['amd64','arm64'],default={'x86_64':'amd64','aarch64':'arm64','arm64':'arm64'}.get(platform.machine()))
p.add_argument('--output',default='bin/victoria-logs-prod')
p.add_argument('--version-output',help='write the verified bundled version to this file')
a=p.parse_args()
if not a.arch:p.error('unsupported architecture')
version='v1.53.0'
name=f'victoria-logs-{a.os}-{a.arch}-{version}.tar.gz'
req=urllib.request.Request(f'https://api.github.com/repos/VictoriaMetrics/VictoriaLogs/releases/tags/{version}',headers={'User-Agent':'DevOpsPlatform-dependency-fetcher'})
with urllib.request.urlopen(req,timeout=60) as r:release=json.load(r)
asset=next(x for x in release['assets'] if x['name']==name)
expected=asset.get('digest','')
if not expected.startswith('sha256:'):raise SystemExit('Official asset has no SHA-256 digest; refusing installation')
output=Path(a.output).resolve();output.parent.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='victorialogs-download-') as root:
 archive=Path(root)/name
 with urllib.request.urlopen(asset['browser_download_url'],timeout=180) as src,archive.open('wb') as dst:
  while True:
   chunk=src.read(1024*1024)
   if not chunk:break
   dst.write(chunk)
 actual='sha256:'+hashlib.sha256(archive.read_bytes()).hexdigest()
 if actual!=expected:raise SystemExit('Official asset digest mismatch; refusing installation')
 with tarfile.open(archive) as tar:
  member=next(m for m in tar.getmembers() if m.isfile() and m.name.split('/')[-1]=='victoria-logs-prod')
  data=tar.extractfile(member).read()
 fd,temp=tempfile.mkstemp(prefix='.victoria-logs-',dir=output.parent)
 try:
  with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
  os.chmod(temp,0o755);os.replace(temp,output)
 finally:
  if os.path.exists(temp):os.unlink(temp)
if a.version_output:
 version_output=Path(a.version_output);version_output.parent.mkdir(parents=True,exist_ok=True);version_output.write_text(version+'\n')
print(f'{version} {a.os}/{a.arch} verified {actual}: {output}')
