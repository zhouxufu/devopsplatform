#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
plugin_os="${GOOS:-$(go env GOOS)}"
plugin_arch="${GOARCH:-$(go env GOARCH)}"
plugin_stage="$(mktemp -d)"
trap 'rm -rf "$plugin_stage"' EXIT
mkdir -p "$plugin_stage/bin" dist
if [ "$plugin_os" = darwin ];then
 CGO_ENABLED=1 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w -linkmode=external' -o "$plugin_stage/bin/example-echo" main.go
else
 CGO_ENABLED=0 GOOS="$plugin_os" GOARCH="$plugin_arch" go build -trimpath -ldflags='-s -w' -o "$plugin_stage/bin/example-echo" main.go
fi
python3 - "$plugin_stage" "$plugin_os" "$plugin_arch" <<'PY'
import pathlib,json,hashlib,sys,zipfile
root=pathlib.Path(sys.argv[1]);system,arch=sys.argv[2:]
manifest={'id':'example-echo','name':'示例回显插件','version':'1.0.0','protocol':1,'os':system,'arch':arch,'entrypoint':'bin/example-echo','sha256':hashlib.sha256((root/'bin/example-echo').read_bytes()).hexdigest(),'capabilities':['example.echo.v1'],'access':{'example.echo.v1':'admin'},'configuration':[{'key':'prefix','label':'回复前缀','type':'string'}]}
(root/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2))
out=pathlib.Path('dist')/('example-echo-'+system+'-'+arch+'.zip')
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
 for p in root.rglob('*'):
  if p.is_file():z.write(p,p.relative_to(root).as_posix())
print(out)
PY
